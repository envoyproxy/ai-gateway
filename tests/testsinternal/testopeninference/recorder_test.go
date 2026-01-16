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
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/stretchr/testify/require"
	collecttracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/envoyproxy/ai-gateway/tests/testsinternal/testopenai"
)

func TestLoadCachedSpan(t *testing.T) {
	validSpanJSON := `{
        "name": "ChatCompletion",
        "kind": "SPAN_KIND_INTERNAL",
        "attributes": [
            {
                "key": "llm.system",
                "value": {"stringValue": "openai"}
            }
        ]
    }`

	tests := []struct {
		name        string
		files       map[string]*fstest.MapFile
		expectSpan  *tracev1.Span
		expectError string
	}{
		{
			name:  "exists",
			files: map[string]*fstest.MapFile{"spans/chat-basic.json": {Data: []byte(validSpanJSON)}},
			expectSpan: &tracev1.Span{
				Name: "ChatCompletion",
				Kind: tracev1.Span_SPAN_KIND_INTERNAL,
				Attributes: []*commonv1.KeyValue{
					{Key: "llm.system", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "openai"}}},
				},
			},
		},
		{
			name:  "missing",
			files: map[string]*fstest.MapFile{},
		},
		{
			name:        "invalid json",
			files:       map[string]*fstest.MapFile{"spans/chat-basic.json": {Data: []byte("invalid")}},
			expectError: "failed to unmarshal span from spans/chat-basic.json: proto: syntax error (line 1:1): invalid value invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &spanRecorder{
				spansFS:  fstest.MapFS(tt.files),
				writeDir: t.TempDir(),
				logger:   log.New(io.Discard, "", 0),
			}
			span, found, err := r.loadCachedSpan(testopenai.CassetteChatBasic)
			if tt.expectError != "" {
				// protojson error includes non-breaking spaces.
				normalizedError := strings.ReplaceAll(err.Error(), "\u00a0", " ")
				require.Equal(t, tt.expectError, normalizedError)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expectSpan != nil, found)
			RequireSpanEqual(t, tt.expectSpan, span)
		})
	}
}

func TestStartOTLPCollector(t *testing.T) {
	r := &spanRecorder{logger: log.New(io.Discard, "", 0)}
	srv, ch := r.startOTLPCollector()
	defer srv.Close()

	trace := &collecttracev1.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{
			{ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{{Name: "test-span"}}}}},
		},
	}
	data, err := proto.Marshal(trace)
	require.NoError(t, err)

	resp, err := http.Post(srv.URL+"/v1/traces", "application/x-protobuf", bytes.NewReader(data))
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	select {
	case rs := <-ch:
		require.Len(t, rs.ScopeSpans, 1)
		require.Len(t, rs.ScopeSpans[0].Spans, 1)
		require.Equal(t, "test-span", rs.ScopeSpans[0].Spans[0].Name)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("timeout")
	}
}

func TestStartOTLPCollector_Errors(t *testing.T) {
	r := &spanRecorder{logger: log.New(io.Discard, "", 0)}
	srv, _ := r.startOTLPCollector()
	defer srv.Close()

	tests := []struct {
		name     string
		body     io.Reader
		wantCode int
	}{
		{"invalid body", bytes.NewReader([]byte("invalid")), http.StatusBadRequest},
		{"no resource spans", bytes.NewReader(mustMarshal(t, &collecttracev1.ExportTraceServiceRequest{})), http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Post(srv.URL+"/v1/traces", "application/x-protobuf", tt.body)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, tt.wantCode, resp.StatusCode)
		})
	}

	t.Run("read error", func(t *testing.T) {
		pipeR, pipeW := io.Pipe()
		pipeW.CloseWithError(io.ErrUnexpectedEOF)

		req, err := http.NewRequest("POST", srv.URL+"/v1/traces", pipeR)
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/x-protobuf")
		req.ContentLength = 100

		client := &http.Client{Timeout: 100 * time.Millisecond}
		resp, err := client.Do(req)
		if err != nil {
			require.Contains(t, err.Error(), "EOF")
			return
		}
		defer resp.Body.Close()
		require.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})
}

func TestSaveSpanToFile(t *testing.T) {
	tmpDir := t.TempDir()
	r := &spanRecorder{writeDir: tmpDir, logger: log.New(io.Discard, "", 0)}

	span := &tracev1.Span{
		Name:    "ChatCompletion",
		Kind:    tracev1.Span_SPAN_KIND_INTERNAL,
		TraceId: []byte{1, 2, 3},
		SpanId:  []byte{4, 5, 6},
		Attributes: []*commonv1.KeyValue{{
			Key:   "llm.system",
			Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "openai"}},
		}},
	}

	err := r.saveSpanToFile(testopenai.CassetteChatBasic, span)
	require.NoError(t, err)

	path := filepath.Join(tmpDir, "chat-basic.json")
	data, err := os.ReadFile(path)
	require.NoError(t, err)

	var saved tracev1.Span
	err = protojson.Unmarshal(data, &saved)
	require.NoError(t, err)
	require.Equal(t, "ChatCompletion", saved.Name)
	require.Equal(t, tracev1.Span_SPAN_KIND_INTERNAL, saved.Kind)
	require.Nil(t, saved.TraceId)
	require.Nil(t, saved.SpanId)
	require.Equal(t, uint64(0), saved.StartTimeUnixNano)
	require.Equal(t, uint64(0), saved.EndTimeUnixNano)
}

func TestSaveSpanToFile_Errors(t *testing.T) {
	r := &spanRecorder{writeDir: "/invalid", logger: log.New(io.Discard, "", 0)}
	err := r.saveSpanToFile(testopenai.CassetteChatBasic, &tracev1.Span{})
	require.EqualError(t, err, "failed to write span file: open /invalid/chat-basic.json: no such file or directory")
}

func TestRecordSpan(t *testing.T) {
	tmpDir := t.TempDir()
	r := &spanRecorder{
		spansFS:    fstest.MapFS{},
		writeDir:   tmpDir,
		logger:     log.New(io.Discard, "", 0),
		startProxy: mockProxy,
	}

	span, err := r.recordSpan(t.Context(), testopenai.CassetteChatBasic)
	require.NoError(t, err)
	require.Equal(t, "ChatCompletion", span.Name)
	require.Equal(t, tracev1.Span_SPAN_KIND_INTERNAL, span.Kind)

	path := filepath.Join(tmpDir, "chat-basic.json")
	require.FileExists(t, path)
}

func TestRecordSpan_Streaming(t *testing.T) {
	tmpDir := t.TempDir()
	r := &spanRecorder{
		spansFS:    fstest.MapFS{},
		writeDir:   tmpDir,
		logger:     log.New(io.Discard, "", 0),
		startProxy: mockStreamingProxy,
	}

	span, err := r.recordSpan(t.Context(), testopenai.CassetteChatStreaming)
	require.NoError(t, err)
	require.Equal(t, "ChatCompletion", span.Name)
}

func TestRecordSpan_Errors(t *testing.T) {
	tmpDir := t.TempDir()

	tests := []struct {
		name        string
		startProxy  proxyFunc
		expectError string
	}{
		{
			name: "proxy fail",
			startProxy: func(_ context.Context, _ *log.Logger, _ testopenai.Cassette, _, _ string) (string, func(), error) {
				return "", nil, fmt.Errorf("proxy fail")
			},
			expectError: "proxy fail",
		},
		{
			name:        "no spans",
			startProxy:  mockNoSpansProxy,
			expectError: "timeout waiting for span",
		},
		{
			name:        "request fail",
			startProxy:  mockErrorProxy,
			expectError: "mock proxy error",
		},
		{
			name:        "conflict status",
			startProxy:  mockConflictProxy,
			expectError: "failed to call proxy (status 409): Cassette mismatch error",
		},
		{
			name:        "internal server error",
			startProxy:  mockInternalErrorProxy,
			expectError: "failed to call proxy (status 500): Internal server error",
		},
		{
			name:        "internal server error empty body",
			startProxy:  mockInternalErrorEmptyProxy,
			expectError: "failed to call proxy (status 500): <empty body>",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &spanRecorder{
				spansFS:    fstest.MapFS{},
				writeDir:   tmpDir,
				logger:     log.New(io.Discard, "", 0),
				startProxy: tt.startProxy,
			}
			_, err := r.recordSpan(t.Context(), testopenai.CassetteChatBasic)
			require.EqualError(t, err, tt.expectError)
		})
	}
}

func TestRecordSpan_ContextCancelled(t *testing.T) {
	tmpDir := t.TempDir()

	r := &spanRecorder{
		spansFS:    fstest.MapFS{},
		writeDir:   tmpDir,
		logger:     log.New(io.Discard, "", 0),
		startProxy: mockProxy,
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := r.recordSpan(ctx, testopenai.CassetteChatBasic)
	require.ErrorContains(t, err, "context canceled")
}

func TestClearVariableFields(t *testing.T) {
	span := &tracev1.Span{
		TraceId:      []byte{1, 2, 3},
		SpanId:       []byte{4, 5, 6},
		ParentSpanId: []byte{7, 8, 9},
		Events:       []*tracev1.Span_Event{{TimeUnixNano: 111}},
		Links:        []*tracev1.Span_Link{{TraceId: []byte{10, 11}, SpanId: []byte{12, 13}}},
	}
	clearVariableFields(span)
	require.Nil(t, span.TraceId)
	require.Nil(t, span.SpanId)
	require.Nil(t, span.ParentSpanId)
	require.Equal(t, uint64(0), span.Events[0].TimeUnixNano)
	require.Nil(t, span.Links[0].TraceId)
	require.Nil(t, span.Links[0].SpanId)
}

func TestGetPort(t *testing.T) {
	tests := []struct {
		url  string
		want int
	}{
		{"http://example.com", 80},
		{"https://example.com", 443},
		{"http://example.com:8080", 8080},
		{"://invalid", 0},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			require.Equal(t, tt.want, getPort(tt.url))
		})
	}
}

func mustMarshal(t *testing.T, msg proto.Message) []byte {
	data, err := proto.Marshal(msg)
	require.NoError(t, err)
	return data
}

func mockProxy(_ context.Context, logger *log.Logger, _ testopenai.Cassette, _, otlp string) (string, func(), error) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Printf("Mock: %s %s", r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"test"}}]}`))
		sendSpan(otlp, &tracev1.Span{Name: "ChatCompletion", Kind: tracev1.Span_SPAN_KIND_INTERNAL})
	}))
	return srv.URL + "/v1", srv.Close, nil
}

func mockStreamingProxy(_ context.Context, logger *log.Logger, _ testopenai.Cassette, _, otlp string) (string, func(), error) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Printf("Mock: %s %s", r.Method, r.URL.Path)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		w.(http.Flusher).Flush()
		sendSpan(otlp, &tracev1.Span{Name: "ChatCompletion", Kind: tracev1.Span_SPAN_KIND_INTERNAL})
	}))
	return srv.URL + "/v1", srv.Close, nil
}

func mockNoSpansProxy(_ context.Context, logger *log.Logger, _ testopenai.Cassette, _, _ string) (string, func(), error) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Printf("Mock: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"test"}}]}`))
	}))
	return srv.URL + "/v1", srv.Close, nil
}

func mockErrorProxy(_ context.Context, _ *log.Logger, _ testopenai.Cassette, _, _ string) (string, func(), error) {
	return "", nil, fmt.Errorf("mock proxy error")
}

func mockConflictProxy(_ context.Context, logger *log.Logger, _ testopenai.Cassette, _, _ string) (string, func(), error) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Printf("Mock: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte("Cassette mismatch error"))
	}))
	return srv.URL + "/v1", srv.Close, nil
}

func mockInternalErrorProxy(_ context.Context, logger *log.Logger, _ testopenai.Cassette, _, _ string) (string, func(), error) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Printf("Mock: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("Internal server error"))
	}))
	return srv.URL + "/v1", srv.Close, nil
}

func mockInternalErrorEmptyProxy(_ context.Context, logger *log.Logger, _ testopenai.Cassette, _, _ string) (string, func(), error) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		logger.Printf("Mock: %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusInternalServerError)
		// Don't write any body.
	}))
	return srv.URL + "/v1", srv.Close, nil
}

func sendSpan(otlp string, span *tracev1.Span) {
	trace := &collecttracev1.ExportTraceServiceRequest{
		ResourceSpans: []*tracev1.ResourceSpans{{ScopeSpans: []*tracev1.ScopeSpans{{Spans: []*tracev1.Span{span}}}}},
	}
	data, _ := proto.Marshal(trace)
	resp, _ := http.Post(otlp+"/v1/traces", "application/x-protobuf", bytes.NewReader(data))
	if resp != nil {
		_ = resp.Body.Close()
	}
}
