// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopeninference

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/tests/testsinternal/testopenai"
)

// proxyFunc is a function that starts a proxy for recording spans.
type proxyFunc func(ctx context.Context, logger *log.Logger, cassette testopenai.Cassette, openaiBaseUrl, otlpEndpoint string) (url string, closer func(), err error)

// spanRecorder records OpenInference spans for OpenAI API requests.
type spanRecorder struct {
	spansFS    fs.FS
	writeDir   string
	logger     *log.Logger
	startProxy proxyFunc
}

// recordSpan records an OpenInference span for the given cassette.
func (r *spanRecorder) recordSpan(ctx context.Context, cassette testopenai.Cassette) (*tracev1.Span, error) {
	r.logger.Printf("Recording span for cassette: %s", cassette)

	r.logger.Printf("Starting test OpenAI server")
	openAIServer, err := testopenai.NewServer(r.logger.Writer(), 0)
	if err != nil {
		return nil, fmt.Errorf("failed to create test OpenAI server: %w", err)
	}
	defer openAIServer.Close()

	r.logger.Printf("Starting OTLP collector")
	collector, spanCh := r.startOTLPCollector()
	defer collector.Close()

	openaiBaseURL := fmt.Sprintf("http://localhost:%d", openAIServer.Port())
	otlpEndpoint := fmt.Sprintf("http://localhost:%d", getPort(collector.URL))
	r.logger.Printf("Starting proxy container with OTLP endpoint: %s", otlpEndpoint)

	proxyURL, closer, err := r.startProxy(ctx, r.logger, cassette, openaiBaseURL, otlpEndpoint)
	if err != nil {
		return nil, err
	}
	defer closer()

	r.logger.Printf("Sending request to proxy: %s", proxyURL)
	req, err := testopenai.NewRequest(ctx, proxyURL, cassette)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Check if this is a streaming request by examining the body.
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}
	req.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	// Parse request to check for streaming.
	var requestData openai.ChatCompletionRequest
	isStreaming := false
	if unmarshalErr := json.Unmarshal(bodyBytes, &requestData); unmarshalErr == nil {
		isStreaming = requestData.Stream
	}
	r.logger.Printf("Request streaming: %v", isStreaming)

	// Create client without timeout - rely on context.
	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	// Check for error responses that indicate cassette problems.
	if resp.StatusCode == http.StatusConflict || resp.StatusCode == http.StatusInternalServerError {
		body, _ := io.ReadAll(resp.Body)
		bodyStr := strings.TrimSpace(string(body))
		if bodyStr == "" {
			bodyStr = "<empty body>"
		}
		return nil, fmt.Errorf("failed to call proxy (status %d): %s", resp.StatusCode, bodyStr)
	}

	if isStreaming {
		r.logger.Printf("Reading streaming response...")
		// For streaming, we need to consume the entire SSE stream until we see [DONE].
		body := make([]byte, 0)
		buf := make([]byte, 4096)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				body = append(body, buf[:n]...)
				// Check if we've received the [DONE] marker.
				if strings.Contains(string(body), "data: [DONE]") {
					r.logger.Printf("Received [DONE] marker in streaming response")
					break
				}
			}
			if err == io.EOF {
				r.logger.Printf("Reached EOF without [DONE] marker")
				break
			}
			if err != nil {
				return nil, fmt.Errorf("failed to read streaming response: %w", err)
			}
		}
		r.logger.Printf("Streaming response consumed: %d bytes", len(body))
	} else {
		// For non-streaming, just read all.
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read response: %w", err)
		}
		r.logger.Printf("Response received: %d bytes, status: %d", len(body), resp.StatusCode)
	}

	r.logger.Printf("Waiting for span...")
	select {
	case resourceSpans := <-spanCh:
		if len(resourceSpans.ScopeSpans) == 0 || len(resourceSpans.ScopeSpans[0].Spans) == 0 {
			return nil, fmt.Errorf("no spans received")
		}
		span := resourceSpans.ScopeSpans[0].Spans[0]

		r.logger.Printf("Span received: %s", span.Name)

		if err := r.saveSpanToFile(cassette, span); err != nil {
			return nil, fmt.Errorf("failed to save span: %w", err)
		}

		return span, nil
	case <-time.After(1 * time.Second): // OTEL_BSP_SCHEDULE_DELAY + overhead.
		return nil, fmt.Errorf("timeout waiting for span")
	case <-ctx.Done():
		return nil, fmt.Errorf("context cancelled while waiting for span: %w", ctx.Err())
	}
}

// loadCachedSpan loads a cached span from the embedded filesystem.
func (r *spanRecorder) loadCachedSpan(cassette testopenai.Cassette) (*tracev1.Span, bool, error) {
	filename := fmt.Sprintf("spans/%s.json", cassette)
	data, err := fs.ReadFile(r.spansFS, filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil // Not found.
		}
		return nil, false, fmt.Errorf("failed to read span file %s: %w", filename, err)
	}

	var span tracev1.Span
	if err := protojson.Unmarshal(data, &span); err != nil {
		return nil, false, fmt.Errorf("failed to unmarshal span from %s: %w", filename, err)
	}

	return &span, true, nil
}

// startOTLPCollector starts a simple HTTP server that collects OTLP traces.
func (r *spanRecorder) startOTLPCollector() (*httptest.Server, chan *tracev1.ResourceSpans) {
	spanCh := make(chan *tracev1.ResourceSpans, 10)
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/traces", func(w http.ResponseWriter, hr *http.Request) {
		body, err := io.ReadAll(hr.Body)
		if err != nil {
			http.Error(w, "Failed to read body", http.StatusBadRequest)
			return
		}
		r.logger.Printf("traces request received: %dbytes", len(body))

		var traces collecttracev1.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &traces); err != nil {
			http.Error(w, "Failed to parse traces", http.StatusBadRequest)
			return
		}

		for _, resourceSpans := range traces.ResourceSpans {
			spanCh <- resourceSpans
		}

		w.WriteHeader(http.StatusOK)
	})

	// Create a test server that listens on all interfaces for Docker.
	listener, err := net.Listen("tcp", ":0") // #nosec G102
	if err != nil {
		panic(fmt.Sprintf("failed to listen: %v", err))
	}
	server := &httptest.Server{
		Listener: listener,
		Config:   &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second},
	}
	server.Start()
	return server, spanCh
}

// saveSpanToFile saves the raw OpenInference span to a JSON file.
func (r *spanRecorder) saveSpanToFile(cassette testopenai.Cassette, span *tracev1.Span) error {
	clearVariableFields(span)
	data, err := protojson.MarshalOptions{
		Multiline: true,
		Indent:    "  ",
	}.Marshal(span)
	if err != nil {
		return fmt.Errorf("failed to marshal span: %w", err)
	}

	// Ensure the file ends with a newline to avoid `make precommit` failures.
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}

	filename := filepath.Join(r.writeDir, fmt.Sprintf("%s.json", cassette))
	if err := os.WriteFile(filename, data, 0o600); err != nil {
		return fmt.Errorf("failed to write span file: %w", err)
	}

	return nil
}

// clearVariableFields clears fields that vary between test runs (timestamps, IDs).
func clearVariableFields(span *tracev1.Span) {
	if span == nil {
		return
	}

	span.TraceId = nil
	span.SpanId = nil
	span.ParentSpanId = nil
	span.StartTimeUnixNano = 0
	span.EndTimeUnixNano = 0

	for _, event := range span.Events {
		event.TimeUnixNano = 0
	}

	for _, link := range span.Links {
		link.TraceId = nil
		link.SpanId = nil
	}
}

// getPort extracts the port from a URL string.
func getPort(urlStr string) int {
	u, err := url.Parse(urlStr)
	if err != nil {
		return 0
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			return 443
		}
		return 80
	}
	var p int
	if _, err := fmt.Sscanf(port, "%d", &p); err != nil {
		return 0
	}
	return p
}
