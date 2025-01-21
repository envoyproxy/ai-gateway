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
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"

	"github.com/envoyproxy/ai-gateway/internal/version"
)

const (
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
	// expectedHostKey is the key for the expected host in the request.
	expectedHostKey = "x-expected-host"
)

// main starts a server that listens on port 1063 and responds with the expected response body and headers
// set via responseHeadersKey and responseBodyHeaderKey.
//
// This also checks if the request content matches the expected headers, path, and body specified in
// expectedHeadersKey, expectedPathHeaderKey, and expectedRequestBodyHeaderKey.
//
// This is useful to test the external process request to the Envoy Gateway LLM Controller.
func main() {
	fmt.Println("Version: ", version.Version)
	l, err := net.Listen("tcp", ":8080") // nolint: gosec
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}
	defer l.Close()
	doMain(l)
}

var streamingInterval = time.Second

func doMain(l net.Listener) {
	if raw := os.Getenv("STREAMING_INTERVAL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			streamingInterval = d
		}
	}
	defer l.Close()
	http.HandleFunc("/health", func(writer http.ResponseWriter, request *http.Request) { writer.WriteHeader(http.StatusOK) })
	http.HandleFunc("/", handler)
	http.HandleFunc("/sse", sseHandler)
	http.HandleFunc("/aws-event-stream", awsEventStreamHandler)
	if err := http.Serve(l, nil); err != nil { // nolint: gosec
		log.Printf("failed to serve: %v", err)
	}
}

func sseHandler(w http.ResponseWriter, r *http.Request) {
	expResponseBody, err := base64.StdEncoding.DecodeString(r.Header.Get(responseBodyHeaderKey))
	if err != nil {
		fmt.Println("failed to decode the response body")
		http.Error(w, "failed to decode the response body", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("testupstream-id", os.Getenv("TESTUPSTREAM_ID"))

	for _, line := range bytes.Split(expResponseBody, []byte("\n")) {
		line := string(line)
		time.Sleep(streamingInterval)

		if _, err = w.Write([]byte("event: some event in testupstream\n")); err != nil {
			log.Println("failed to write the response body")
			return
		}

		if _, err = w.Write([]byte(fmt.Sprintf("data: %s\n\n", line))); err != nil {
			log.Println("failed to write the response body")
			return
		}

		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		} else {
			panic("expected http.ResponseWriter to be an http.Flusher")
		}
		fmt.Println("response line sent:", line)
	}

	fmt.Println("response sent")
	r.Context().Done()
}

func handler(w http.ResponseWriter, r *http.Request) {
	for k, v := range r.Header {
		fmt.Printf("header %q: %s\n", k, v)
	}
	if v := r.Header.Get(expectedHostKey); v != "" {
		if r.Host != v {
			fmt.Printf("unexpected host: got %q, expected %q\n", r.Host, v)
			http.Error(w, "unexpected host: got "+r.Host+", expected "+v, http.StatusBadRequest)
			return
		}
		fmt.Println("host matched:", v)
	} else {
		fmt.Println("no expected host: got", r.Host)
	}
	if v := r.Header.Get(expectedHeadersKey); v != "" {
		expectedHeaders, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			fmt.Println("failed to decode the expected headers")
			http.Error(w, "failed to decode the expected headers", http.StatusBadRequest)
			return
		}
		fmt.Println("expected headers", string(expectedHeaders))

		// Comma separated key-value pairs.
		for _, kv := range bytes.Split(expectedHeaders, []byte(",")) {
			parts := bytes.SplitN(kv, []byte(":"), 2)
			if len(parts) != 2 {
				fmt.Println("invalid header key-value pair", string(kv))
				http.Error(w, "invalid header key-value pair "+string(kv), http.StatusBadRequest)
				return
			}
			key := string(parts[0])
			value := string(parts[1])
			if r.Header.Get(key) != value {
				fmt.Printf("unexpected header %q: got %q, expected %q\n", key, r.Header.Get(key), value)
				http.Error(w, "unexpected header "+key+": got "+r.Header.Get(key)+", expected "+value, http.StatusBadRequest)
				return
			}
			fmt.Printf("header %q matched %s\n", key, value)
		}
	} else {
		fmt.Println("no expected headers")
	}

	if v := r.Header.Get(nonExpectedRequestHeadersKey); v != "" {
		nonExpectedHeaders, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			fmt.Println("failed to decode the non-expected headers")
			http.Error(w, "failed to decode the non-expected headers", http.StatusBadRequest)
			return
		}
		fmt.Println("non-expected headers", string(nonExpectedHeaders))

		// Comma separated key-value pairs.
		for _, kv := range bytes.Split(nonExpectedHeaders, []byte(",")) {
			key := string(kv)
			if r.Header.Get(key) != "" {
				fmt.Printf("unexpected header %q presence with value %q\n", key, r.Header.Get(key))
				http.Error(w, "unexpected header "+key+" presence with value "+r.Header.Get(key), http.StatusBadRequest)
				return
			}
			fmt.Printf("header %q absent\n", key)
		}
	} else {
		fmt.Println("no non-expected headers in the request")
	}

	if v := r.Header.Get(expectedTestUpstreamIDKey); v != "" {
		if os.Getenv("TESTUPSTREAM_ID") != v {
			msg := fmt.Sprintf("unexpected testupstream-id: received by '%s' but expected '%s'\n", os.Getenv("TESTUPSTREAM_ID"), v)
			fmt.Println(msg)
			http.Error(w, msg, http.StatusBadRequest)
			return
		} else {
			fmt.Println("testupstream-id matched:", v)
		}
	} else {
		fmt.Println("no expected testupstream-id")
	}

	expectedPath, err := base64.StdEncoding.DecodeString(r.Header.Get(expectedPathHeaderKey))
	if err != nil {
		fmt.Println("failed to decode the expected path")
		http.Error(w, "failed to decode the expected path", http.StatusBadRequest)
		return
	}

	if r.URL.Path != string(expectedPath) {
		fmt.Printf("unexpected path: got %q, expected %q\n", r.URL.Path, string(expectedPath))
		http.Error(w, "unexpected path: got "+r.URL.Path+", expected "+string(expectedPath), http.StatusBadRequest)
		return
	}

	requestBody, err := io.ReadAll(r.Body)
	if err != nil {
		fmt.Println("failed to read the request body")
		http.Error(w, "failed to read the request body", http.StatusInternalServerError)
		return
	}

	if r.Header.Get(expectedRequestBodyHeaderKey) != "" {
		expectedBody, err := base64.StdEncoding.DecodeString(r.Header.Get(expectedRequestBodyHeaderKey))
		if err != nil {
			fmt.Println("failed to decode the expected request body")
			http.Error(w, "failed to decode the expected request body", http.StatusBadRequest)
			return
		}

		if string(expectedBody) != string(requestBody) {
			fmt.Println("unexpected request body: got", string(requestBody), "expected", string(expectedBody))
			http.Error(w, "unexpected request body: got "+string(requestBody)+", expected "+string(expectedBody), http.StatusBadRequest)
			return
		}
	} else {
		fmt.Println("no expected request body")
	}

	responseBody, err := base64.StdEncoding.DecodeString(r.Header.Get(responseBodyHeaderKey))
	if err != nil {
		fmt.Println("failed to decode the response body")
		http.Error(w, "failed to decode the response body", http.StatusBadRequest)
		return
	}
	if v := r.Header.Get(responseHeadersKey); v != "" {
		responseHeaders, err := base64.StdEncoding.DecodeString(v)
		if err != nil {
			fmt.Println("failed to decode the response headers")
			http.Error(w, "failed to decode the response headers", http.StatusBadRequest)
			return
		}
		fmt.Println("response headers", string(responseHeaders))

		// Comma separated key-value pairs.
		for _, kv := range bytes.Split(responseHeaders, []byte(",")) {
			parts := bytes.SplitN(kv, []byte(":"), 2)
			if len(parts) != 2 {
				fmt.Println("invalid header key-value pair", string(kv))
				http.Error(w, "invalid header key-value pair "+string(kv), http.StatusBadRequest)
				return
			}
			key := string(parts[0])
			value := string(parts[1])
			w.Header().Set(key, value)
			fmt.Printf("response header %q set to %s\n", key, value)
		}
	} else {
		fmt.Println("no response headers")
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("testupstream-id", os.Getenv("TESTUPSTREAM_ID"))
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(responseBody); err != nil {
		log.Println("failed to write the response body")
	}
	fmt.Println("response sent")
}

func awsEventStreamHandler(w http.ResponseWriter, r *http.Request) {
	expResponseBody, err := base64.StdEncoding.DecodeString(r.Header.Get(responseBodyHeaderKey))
	if err != nil {
		fmt.Println("failed to decode the response body")
		http.Error(w, "failed to decode the response body", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
	w.Header().Set("Transfer-Encoding", "chunked")
	w.Header().Set("testupstream-id", os.Getenv("TESTUPSTREAM_ID"))

	e := eventstream.NewEncoder()
	for _, line := range bytes.Split(expResponseBody, []byte("\n")) {
		// Write each line as a chunk with AWS Event Stream format.
		time.Sleep(streamingInterval)
		if err := e.Encode(w, eventstream.Message{
			Headers: eventstream.Headers{{Name: "event-type", Value: eventstream.StringValue("content")}},
			Payload: line,
		}); err != nil {
			log.Println("failed to encode the response body")
		}
		w.(http.Flusher).Flush()
		fmt.Println("response line sent:", string(line))
	}

	if err := e.Encode(w, eventstream.Message{
		Headers: eventstream.Headers{{Name: "event-type", Value: eventstream.StringValue("end")}},
		Payload: []byte("this-is-end"),
	}); err != nil {
		log.Println("failed to encode the response body")
	}

	fmt.Println("response sent")
	r.Context().Done()
}
