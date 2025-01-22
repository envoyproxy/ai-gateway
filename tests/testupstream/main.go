package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"

	"github.com/envoyproxy/ai-gateway/internal/version"
)

var logger = log.New(os.Stdout, "[testupstream] ", 0)

const (
	// responseTypeKey is the key for the response type in the request.
	// This can be either empty, "sse", or "aws-event-stream".
	//	* If this is "sse", the response body is expected to be a Server-Sent Event stream.
	// 	Each line in x-response-body is treated as a separate [data] payload.
	//	* If this is "aws-event-stream", the response body is expected to be an AWS Event Stream.
	// 	Each line in x-response-body is treated as a separate event payload.
	//	* If this is empty, the response body is expected to be a regular JSON response.
	responseTypeKey = "x-response-type"
	// expectedHeadersKey is the key for the expected headers in the request.
	// The value is a base64 encoded string of comma separated key-value pairs.
	// E.g. "key1:value1,key2:value2".
	expectedHeadersKey = "x-expected-headers"
	// expectedPathHeaderKey is the key for the expected path in the request.
	// The value is a base64 encoded.
	expectedPathHeaderKey = "x-expected-path"
	// expectedRequestBodyHeaderKey is the key for the expected request body in the request.
	// The value is a base64 encoded.
	expectedRequestBodyHeaderKey = "x-expected-request-body"
	// responseStatusKey is the key for the response status in the response, default is 200 if not set.
	responseStatusKey = "x-response-status"
	// responseHeadersKey is the key for the response headers in the response.
	// The value is a base64 encoded string of comma separated key-value pairs.
	// E.g. "key1:value1,key2:value2".
	responseHeadersKey = "x-response-headers"
	// responseBodyHeaderKey is the key for the response body in the response.
	// The value is a base64 encoded.
	responseBodyHeaderKey = "x-response-body"
	// nonExpectedHeadersKey is the key for the non-expected request headers.
	// The value is a base64 encoded string of comma separated header keys expected to be absent.
	nonExpectedRequestHeadersKey = "x-non-expected-request-headers"
	// expectedTestUpstreamIDKey is the key for the expected testupstream-id in the request,
	// and the value will be compared with the TESTUPSTREAM_ID environment variable.
	// If the values do not match, the request will be rejected, meaning that the request
	// was routed to the wrong upstream.
	expectedTestUpstreamIDKey = "x-expected-testupstream-id"
)

// main starts a server that listens on port 1063 and responds with the expected response body and headers
// set via responseHeadersKey and responseBodyHeaderKey.
//
// This also checks if the request content matches the expected headers, path, and body specified in
// expectedHeadersKey, expectedPathHeaderKey, and expectedRequestBodyHeaderKey.
//
// This is useful to test the external process request to the Envoy Gateway LLM Controller.
func main() {
	logger.Println("Version: ", version.Version)
	l, err := net.Listen("tcp", ":8080") // nolint: gosec
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	defer l.Close()
	doMain(l)
}

var streamingInterval = 200 * time.Millisecond

func doMain(l net.Listener) {
	if raw := os.Getenv("STREAMING_INTERVAL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			streamingInterval = d
		}
	}
	defer l.Close()
	http.HandleFunc("/health", func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(http.StatusOK) })
	http.HandleFunc("/", handler)
	if err := http.Serve(l, nil); err != nil { // nolint: gosec
		logger.Printf("failed to serve: %v", err)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	for k, v := range r.Header {
		logger.Printf("header %q: %s\n", k, v)
	}
	if v := r.Header.Get(expectedHeadersKey); v != "" {
		expectedHeaders, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			logger.Println("failed to decode the expected headers")
			http.Error(w, "failed to decode the expected headers", http.StatusBadRequest)
			return
		}
		logger.Println("expected headers", string(expectedHeaders))

		// Comma separated key-value pairs.
		for _, kv := range bytes.Split(expectedHeaders, []byte(",")) {
			parts := bytes.SplitN(kv, []byte(":"), 2)
			if len(parts) != 2 {
				logger.Println("invalid header key-value pair", string(kv))
				http.Error(w, "invalid header key-value pair "+string(kv), http.StatusBadRequest)
				return
			}
			key := string(parts[0])
			value := string(parts[1])
			if r.Header.Get(key) != value {
				logger.Printf("unexpected header %q: got %q, expected %q\n", key, r.Header.Get(key), value)
				http.Error(w, "unexpected header "+key+": got "+r.Header.Get(key)+", expected "+value, http.StatusBadRequest)
				return
			}
			logger.Printf("header %q matched %s\n", key, value)
		}
	} else {
		logger.Println("no expected headers")
	}

	if v := r.Header.Get(nonExpectedRequestHeadersKey); v != "" {
		nonExpectedHeaders, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			logger.Println("failed to decode the non-expected headers")
			http.Error(w, "failed to decode the non-expected headers", http.StatusBadRequest)
			return
		}
		logger.Println("non-expected headers", string(nonExpectedHeaders))

		// Comma separated key-value pairs.
		for _, kv := range bytes.Split(nonExpectedHeaders, []byte(",")) {
			key := string(kv)
			if r.Header.Get(key) != "" {
				logger.Printf("unexpected header %q presence with value %q\n", key, r.Header.Get(key))
				http.Error(w, "unexpected header "+key+" presence with value "+r.Header.Get(key), http.StatusBadRequest)
				return
			}
			logger.Printf("header %q absent\n", key)
		}
	} else {
		logger.Println("no non-expected headers in the request")
	}

	if v := r.Header.Get(expectedTestUpstreamIDKey); v != "" {
		if os.Getenv("TESTUPSTREAM_ID") != v {
			msg := fmt.Sprintf("unexpected testupstream-id: received by '%s' but expected '%s'\n", os.Getenv("TESTUPSTREAM_ID"), v)
			logger.Println(msg)
			http.Error(w, msg, http.StatusBadRequest)
			return
		} else {
			logger.Println("testupstream-id matched:", v)
		}
	} else {
		logger.Println("no expected testupstream-id")
	}

	if expectedPath := r.Header.Get(expectedPathHeaderKey); expectedPath != "" {
		expectedPath, err := base64.StdEncoding.DecodeString(expectedPath)
		if err != nil {
			logger.Println("failed to decode the expected path")
			http.Error(w, "failed to decode the expected path", http.StatusBadRequest)
			return
		}

		if r.URL.Path != string(expectedPath) {
			logger.Printf("unexpected path: got %q, expected %q\n", r.URL.Path, string(expectedPath))
			http.Error(w, "unexpected path: got "+r.URL.Path+", expected "+string(expectedPath), http.StatusBadRequest)
			return
		}
	}

	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Println("failed to read the request body")
		http.Error(w, "failed to read the request body", http.StatusInternalServerError)
		return
	}

	if expectedReqBody := r.Header.Get(expectedRequestBodyHeaderKey); expectedReqBody != "" {
		expectedBody, err := base64.StdEncoding.DecodeString(expectedReqBody)
		if err != nil {
			logger.Println("failed to decode the expected request body")
			http.Error(w, "failed to decode the expected request body", http.StatusBadRequest)
			return
		}

		if string(expectedBody) != string(requestBody) {
			logger.Println("unexpected request body: got", string(requestBody), "expected", string(expectedBody))
			http.Error(w, "unexpected request body: got "+string(requestBody)+", expected "+string(expectedBody), http.StatusBadRequest)
			return
		}
	} else {
		logger.Println("no expected request body")
	}

	responseBody, err := base64.StdEncoding.DecodeString(r.Header.Get(responseBodyHeaderKey))
	if err != nil {
		logger.Println("failed to decode the response body")
		http.Error(w, "failed to decode the response body", http.StatusBadRequest)
		return
	}
	if v := r.Header.Get(responseHeadersKey); v != "" {
		responseHeaders, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			logger.Println("failed to decode the response headers")
			http.Error(w, "failed to decode the response headers", http.StatusBadRequest)
			return
		}
		logger.Println("response headers", string(responseHeaders))

		// Comma separated key-value pairs.
		for _, kv := range bytes.Split(responseHeaders, []byte(",")) {
			parts := bytes.SplitN(kv, []byte(":"), 2)
			if len(parts) != 2 {
				logger.Println("invalid header key-value pair", string(kv))
				http.Error(w, "invalid header key-value pair "+string(kv), http.StatusBadRequest)
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
	if v := r.Header.Get(responseStatusKey); v != "" {
		status, err = strconv.Atoi(v)
		if err != nil {
			logger.Println("failed to parse the response status")
			http.Error(w, "failed to parse the response status", http.StatusBadRequest)
			return
		}
	}

	switch r.Header.Get(responseTypeKey) {
	case "sse":
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(status)

		expResponseBody, err := base64.StdEncoding.DecodeString(r.Header.Get(responseBodyHeaderKey))
		if err != nil {
			logger.Println("failed to decode the response body")
			http.Error(w, "failed to decode the response body", http.StatusBadRequest)
			return
		}

		for _, line := range bytes.Split(expResponseBody, []byte("\n")) {
			line := string(line)
			if line == "" {
				continue
			}
			time.Sleep(streamingInterval)

			if _, err = w.Write([]byte(fmt.Sprintf("data: %s\n\n", line))); err != nil {
				logger.Println("failed to write the response body")
				return
			}

			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			} else {
				panic("expected http.ResponseWriter to be an http.Flusher")
			}
			logger.Println("response line sent:", line)
		}
		logger.Println("response sent")
		r.Context().Done()
	case "aws-event-stream":
		// w.Header().Set("Transfer-Encoding", "chunked")
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(status)

		expResponseBody, err := base64.StdEncoding.DecodeString(r.Header.Get(responseBodyHeaderKey))
		if err != nil {
			logger.Println("failed to decode the response body")
			http.Error(w, "failed to decode the response body", http.StatusBadRequest)
			return
		}

		e := eventstream.NewEncoder()
		for _, line := range bytes.Split(expResponseBody, []byte("\n")) {
			// Write each line as a chunk with AWS Event Stream format.
			if len(line) == 0 {
				continue
			}
			time.Sleep(streamingInterval)
			if err := e.Encode(w, eventstream.Message{
				Headers: eventstream.Headers{{Name: "event-type", Value: eventstream.StringValue("content")}},
				Payload: line,
			}); err != nil {
				logger.Println("failed to encode the response body")
			}
			w.(http.Flusher).Flush()
			logger.Println("response line sent:", string(line))
		}

		if err := e.Encode(w, eventstream.Message{
			Headers: eventstream.Headers{{Name: "event-type", Value: eventstream.StringValue("end")}},
			Payload: []byte("this-is-end"),
		}); err != nil {
			logger.Println("failed to encode the response body")
		}

		logger.Println("response sent")
		r.Context().Done()
	default:
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := w.Write(responseBody); err != nil {
			logger.Println("failed to write the response body")
		}
		logger.Println("response sent:", string(responseBody))
	}
}
