// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/tidwall/gjson"
	"golang.org/x/exp/rand"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/version"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

var logger = log.New(os.Stdout, "[testupstream] ", 0)

// main starts a server that listens on port 1063 and responds with the expected response body and headers
// set via responseHeadersKey and responseBodyHeaderKey.
//
// This also checks if the request content matches the expected headers, path, and body specified in
// expectedHeadersKey, expectedPathHeaderKey, and expectedRequestBodyHeaderKey.
//
// This is useful to test the external processor request to the Envoy Gateway LLM Controller.
func main() {
	logger.Println("Version: ", version.Parse())
	// Note: Do not use "TESTUPSTREAM_PORT" as it will conflict with an automatic environment variable
	// set by K8s, which results in a very hard-to-debug issue during e2e.
	port := os.Getenv("LISTENER_PORT")
	if port == "" {
		port = "8080"
	}
	l, err := net.Listen("tcp", ":"+port) // nolint: gosec
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	defer l.Close()
	// Emit startup message when the listener is ready.
	log.Println("Test upstream is ready")
	doMain(l)
}

var streamingInterval = 200 * time.Millisecond

func doMain(l net.Listener) {
	if raw := os.Getenv("STREAMING_INTERVAL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			streamingInterval = d
		}
	}
	http.HandleFunc("/health", func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusOK) })
	http.HandleFunc("/", handler)
	if err := http.Serve(l, nil); err != nil { // nolint: gosec
		logger.Printf("failed to serve: %v", err)
	}
}

// logAndSendError logs the error and sends a proper error response with details
func logAndSendError(w http.ResponseWriter, code int, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	logger.Printf("ERROR [%d]: %s", code, msg)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-TestUpstream-Error", "true")
	w.WriteHeader(code)
	fmt.Fprintf(w, format+"\n", a...) //nolint:errcheck
}

func handler(w http.ResponseWriter, r *http.Request) {
	for k, v := range r.Header {
		logger.Printf("header %q: %s\n", k, v)
	}
	if v := r.Header.Get(testupstreamlib.ExpectedHostKey); v != "" {
		if r.Host != v {
			logAndSendError(w, http.StatusBadRequest, "unexpected host: got %q, expected %q", r.Host, v)
			return
		}
		logger.Println("host matched:", v)
	} else {
		logger.Println("no expected host: got", r.Host)
	}
	if v := r.Header.Get(testupstreamlib.ExpectedHeadersKey); v != "" {
		expectedHeaders, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			logAndSendError(w, http.StatusBadRequest, "failed to decode the expected headers: %v", err)
			return
		}
		logger.Println("expected headers", string(expectedHeaders))

		// Comma separated key-value pairs.
		for kv := range bytes.SplitSeq(expectedHeaders, []byte(",")) {
			parts := bytes.SplitN(kv, []byte(":"), 2)
			if len(parts) != 2 {
				logAndSendError(w, http.StatusBadRequest, "invalid header key-value pair: %s", string(kv))
				return
			}
			key := string(parts[0])
			value := string(parts[1])
			if r.Header.Get(key) != value {
				logAndSendError(w, http.StatusBadRequest, "unexpected header %q: got %q, expected %q", key, r.Header.Get(key), value)
				return
			}
			logger.Printf("header %q matched %s\n", key, value)
		}
	} else {
		logger.Println("no expected headers")
	}

	if v := r.Header.Get(testupstreamlib.NonExpectedRequestHeadersKey); v != "" {
		nonExpectedHeaders, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			logAndSendError(w, http.StatusBadRequest, "failed to decode the non-expected headers: %v", err)
			return
		}
		logger.Println("non-expected headers", string(nonExpectedHeaders))

		// Comma separated key-value pairs.
		for kv := range bytes.SplitSeq(nonExpectedHeaders, []byte(",")) {
			key := string(kv)
			if r.Header.Get(key) != "" {
				logAndSendError(w, http.StatusBadRequest, "unexpected header %q presence with value %q", key, r.Header.Get(key))
				return
			}
			logger.Printf("header %q absent\n", key)
		}
	} else {
		logger.Println("no non-expected headers in the request")
	}

	if v := r.Header.Get(testupstreamlib.ExpectedTestUpstreamIDKey); v != "" {
		if os.Getenv("TESTUPSTREAM_ID") != v {
			msg := fmt.Sprintf("unexpected testupstream-id: received by '%s' but expected '%s'\n", os.Getenv("TESTUPSTREAM_ID"), v)
			logAndSendError(w, http.StatusBadRequest, "%s", msg)
			return
		}
		logger.Println("testupstream-id matched:", v)
	} else {
		logger.Println("no expected testupstream-id")
	}

	if expectedPath := r.Header.Get(testupstreamlib.ExpectedPathHeaderKey); expectedPath != "" {
		expectedPath, err := base64.StdEncoding.DecodeString(expectedPath)
		if err != nil {
			logAndSendError(w, http.StatusBadRequest, "failed to decode the expected path: %v", err)
			return
		}

		if r.URL.Path != string(expectedPath) {
			logAndSendError(w, http.StatusBadRequest, "unexpected path: got %s, expected %s", r.URL.Path, string(expectedPath))
			return
		}
	}

	if expectedRawQuery := r.Header.Get(testupstreamlib.ExpectedRawQueryHeaderKey); expectedRawQuery != "" {
		if r.URL.RawQuery != expectedRawQuery {
			logAndSendError(w, http.StatusBadRequest, "unexpected raw query: got %s, expected %s", r.URL.RawQuery, expectedRawQuery)
			return
		}
	}

	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		logAndSendError(w, http.StatusInternalServerError, "failed to read the request body: %v", err)
		return
	}
	logger.Printf("Request body (%d bytes): %s", len(requestBody), string(requestBody))

	// At least for the endpoints we want to support, all requests should have a Content-Length header
	// and should not use chunked transfer encoding.
	if r.Header.Get("Content-Length") == "" {
		// Endpoint pickers mutate the request body by sending them back to the client (due to the use of DUPLEX mode),
		// and it will clear the Content-Length header. It should be fine to assume that these locally hosted endpoints
		// are capable of reading the chunked transfer encoding unlike the GCP Anthropic.
		if r.Header.Get(internalapi.EndpointPickerHeaderKey) == "" {
			logAndSendError(w, http.StatusBadRequest, "no Content-Length header, using request body length: %d", len(requestBody))
			return
		}
	}

	if expectedReqBody := r.Header.Get(testupstreamlib.ExpectedRequestBodyHeaderKey); expectedReqBody != "" {
		var expectedBody []byte
		expectedBody, err = base64.StdEncoding.DecodeString(expectedReqBody)
		if err != nil {
			logAndSendError(w, http.StatusBadRequest, "failed to decode the expected request body: %v", err)
			return
		}

		if string(expectedBody) != string(requestBody) {
			logAndSendError(w, http.StatusBadRequest, "unexpected request body: got %s, expected %s", string(requestBody), string(expectedBody))
			return
		}
	} else {
		logger.Println("no expected request body")
	}

	if v := r.Header.Get(testupstreamlib.ResponseHeadersKey); v != "" {
		var responseHeaders []byte
		responseHeaders, err = base64.StdEncoding.DecodeString(v)
		if err != nil {
			logAndSendError(w, http.StatusBadRequest, "failed to decode the response headers: %v", err)
			return
		}
		logger.Println("response headers", string(responseHeaders))

		// Comma separated key-value pairs.
		for kv := range bytes.SplitSeq(responseHeaders, []byte(",")) {
			parts := bytes.SplitN(kv, []byte(":"), 2)
			if len(parts) != 2 {
				logAndSendError(w, http.StatusBadRequest, "invalid header key-value pair: %s", string(kv))
				return
			}
			key := string(parts[0])
			value := string(parts[1])
			w.Header().Set(key, value)
			logger.Printf("response header %q set to %s\n", key, value)
		}
	} else {
		logger.Println("no response headers")
	}
	w.Header().Set("testupstream-id", os.Getenv("TESTUPSTREAM_ID"))
	status := http.StatusOK
	if v := r.Header.Get(testupstreamlib.ResponseStatusKey); v != "" {
		status, err = strconv.Atoi(v)
		if err != nil {
			logAndSendError(w, http.StatusBadRequest, "failed to parse the response status: %v", err)
			return
		}
	}

	// Do the best-effort model detection for logging and verification.
	model := gjson.GetBytes(requestBody, "model")
	if model.Exists() {
		logger.Println("detected model in the request:", model)
		// Set the model in the response header for verification.
		w.Header().Set("X-Model", model.String())
	}

	switch r.Header.Get(testupstreamlib.ResponseTypeKey) {
	case "sse":
		w.Header().Set("Content-Type", "text/event-stream")

		var expResponseBody []byte
		expResponseBody, err = base64.StdEncoding.DecodeString(r.Header.Get(testupstreamlib.ResponseBodyHeaderKey))
		if err != nil {
			logAndSendError(w, http.StatusBadRequest, "failed to decode the response body: %v", err)
			return
		}

		w.WriteHeader(status)

		// Auto-detect the SSE format. If the body contains the event message separator "\n\n",
		// we treat it as a stream of pre-formatted "raw" SSE events. Otherwise, we treat it
		// as a simple line-by-line stream that needs to be formatted.
		if bytes.Contains(expResponseBody, []byte("\n\n")) {
			eventBlocks := bytes.SplitSeq(expResponseBody, []byte("\n\n"))

			for block := range eventBlocks {
				// Skip any empty blocks that can result from splitting.
				if len(bytes.TrimSpace(block)) == 0 {
					continue
				}
				time.Sleep(streamingInterval)

				// Write the complete event block followed by the required double newline delimiter.
				if _, err = w.Write(append(block, "\n\n"...)); err != nil {
					logger.Println("failed to write the response body")
					return
				}

				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				} else {
					panic("expected http.ResponseWriter to be an http.Flusher")
				}
				logger.Println("response block sent:", string(block))
			}

		} else {
			logger.Println("detected line-by-line stream, formatting as SSE")
			lines := bytes.SplitSeq(expResponseBody, []byte("\n"))

			for line := range lines {
				if len(line) == 0 {
					continue
				}
				time.Sleep(streamingInterval)

				// Format the line as an SSE 'data' message.
				if _, err = fmt.Fprintf(w, "data: %s\n\n", line); err != nil {
					logger.Println("failed to write the response body")
					return
				}

				if f, ok := w.(http.Flusher); ok {
					f.Flush()
				} else {
					panic("expected http.ResponseWriter to be an http.Flusher")
				}
				logger.Println("response line sent:", string(line))
			}
		}

		logger.Println("response sent")
		r.Context().Done()
	case "aws-event-stream":
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")

		var expResponseBody []byte
		expResponseBody, err = base64.StdEncoding.DecodeString(r.Header.Get(testupstreamlib.ResponseBodyHeaderKey))
		if err != nil {
			logAndSendError(w, http.StatusBadRequest, "failed to decode the response body: %v", err)
			return
		}

		w.WriteHeader(status)
		e := eventstream.NewEncoder()
		for line := range bytes.SplitSeq(expResponseBody, []byte("\n")) {
			// Write each line as a chunk with AWS Event Stream format.
			if len(line) == 0 {
				continue
			}
			time.Sleep(streamingInterval)
			var bedrockStreamEvent map[string]any
			err = json.Unmarshal(line, &bedrockStreamEvent)
			if err != nil {
				logger.Println("failed to decode the response body")
			}
			var eventType string
			if _, ok := bedrockStreamEvent["role"]; ok {
				eventType = "messageStart"
			} else if _, ok = bedrockStreamEvent["start"]; ok {
				eventType = "contentBlockStart"
			} else if _, ok = bedrockStreamEvent["delta"]; ok {
				eventType = "contentBlockDelta"
			} else if _, ok = bedrockStreamEvent["stopReason"]; ok {
				eventType = "messageStop"
			} else if _, ok = bedrockStreamEvent["usage"]; ok {
				eventType = "metadata"
			} else if _, ok = bedrockStreamEvent["contentBlockIndex"]; ok {
				eventType = "contentBlockStop"
			}
			if err = e.Encode(w, eventstream.Message{
				Headers: eventstream.Headers{{Name: ":event-type", Value: eventstream.StringValue(eventType)}},
				Payload: line,
			}); err != nil {
				logger.Println("failed to encode the response body")
			}
			w.(http.Flusher).Flush()
			logger.Println("response line sent:", string(line))
		}

		if err = e.Encode(w, eventstream.Message{
			Headers: eventstream.Headers{{Name: "event-type", Value: eventstream.StringValue("end")}},
			Payload: []byte("this-is-end"),
		}); err != nil {
			logger.Println("failed to encode the response body")
		}

		logger.Println("response sent")
		r.Context().Done()
	default:
		isGzip := r.Header.Get(testupstreamlib.ResponseTypeKey) == "gzip"
		if isGzip {
			w.Header().Set("content-encoding", "gzip")
		}
		w.Header().Set("content-type", "application/json")
		var responseBody []byte
		if expResponseBody := r.Header.Get(testupstreamlib.ResponseBodyHeaderKey); expResponseBody == "" {
			// If the expected response body is not set, get the fake response if the path is known.
			responseBody, err = getFakeResponse(r.URL.Path)
			if err != nil {
				logAndSendError(w, http.StatusBadRequest, "failed to get the fake response for path %s: %v", r.URL.Path, err)
				return
			}
		} else {
			responseBody, err = base64.StdEncoding.DecodeString(expResponseBody)
			if err != nil {
				logAndSendError(w, http.StatusBadRequest, "failed to decode the response body: %v", err)
				return
			}
		}

		w.WriteHeader(status)
		if isGzip {
			var buf bytes.Buffer
			gz := gzip.NewWriter(&buf)
			_, _ = gz.Write(responseBody)
			_ = gz.Close()
			responseBody = buf.Bytes()
		}
		_, _ = w.Write(responseBody)
		logger.Println("response sent:", string(responseBody))
	}
}

var chatCompletionFakeResponses = []string{
	`This is a test.`,
	`The quick brown fox jumps over the lazy dog.`,
	`Lorem ipsum dolor sit amet, consectetur adipiscing elit.`,
	`To be or not to be, that is the question.`,
	`All your base are belong to us.`,
	`I am the bone of my sword.`,
	`I am the master of my fate.`,
	`I am the captain of my soul.`,
	`I am the master of my fate, I am the captain of my soul.`,
	`I am the bone of my sword, steel is my body, and fire is my blood.`,
	`The quick brown fox jumps over the lazy dog.`,
	`Lorem ipsum dolor sit amet, consectetur adipiscing elit.`,
	`To be or not to be, that is the question.`,
	`All your base are belong to us.`,
	`Omae wa mou shindeiru.`,
	`Nani?`,
	`I am inevitable.`,
	`May the Force be with you.`,
	`Houston, we have a problem.`,
	`I'll be back.`,
	`You can't handle the truth!`,
	`Here's looking at you, kid.`,
	`Go ahead, make my day.`,
	`I see dead people.`,
	`Hasta la vista, baby.`,
	`You're gonna need a bigger boat.`,
	`E.T. phone home.`,
	`I feel the need - the need for speed.`,
	`I'm king of the world!`,
	`Show me the money!`,
	`You had me at hello.`,
	`I'm the king of the world!`,
	`To infinity and beyond!`,
	`You're a wizard, Harry.`,
	`I solemnly swear that I am up to no good.`,
	`Mischief managed.`,
	`Expecto Patronum!`,
}

func getFakeResponse(path string) ([]byte, error) {
	switch path {
	case "/non-llm-route":
		const template = `{"message":"This is a non-LLM endpoint response"}`
		return []byte(template), nil
	case "/v1/chat/completions":
		const template = `{"choices":[{"message":{"role":"assistant", "content":"%s"}}]}`
		msg := fmt.Sprintf(template,
			//nolint:gosec
			chatCompletionFakeResponses[rand.New(rand.NewSource(uint64(time.Now().UnixNano()))).
				Intn(len(chatCompletionFakeResponses))])
		return []byte(msg), nil
	case "/v1/embeddings":
		const embeddingTemplate = `{"object":"list","data":[{"object":"embedding","embedding":[0.1,0.2,0.3,0.4,0.5],"index":0}],"model":"some-cool-self-hosted-model","usage":{"prompt_tokens":3,"total_tokens":3}}`
		return []byte(embeddingTemplate), nil
	default:
		return nil, fmt.Errorf("unknown path: %s", path)
	}
}
