// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"compress/gzip"
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
	"golang.org/x/exp/rand"

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
	http.HandleFunc("/health", func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusOK) })
	http.HandleFunc("/", handler)
	if err := http.Serve(l, nil); err != nil { // nolint: gosec
		logger.Printf("failed to serve: %v", err)
	}
}

func handler(w http.ResponseWriter, r *http.Request) {
	for k, v := range r.Header {
		logger.Printf("header %q: %s\n", k, v)
	}
	if v := r.Header.Get(testupstreamlib.ExpectedHostKey); v != "" {
		if r.Host != v {
			logger.Printf("unexpected host: got %q, expected %q\n", r.Host, v)
			http.Error(w, "unexpected host: got "+r.Host+", expected "+v, http.StatusBadRequest)
			return
		}
		logger.Println("host matched:", v)
	} else {
		logger.Println("no expected host: got", r.Host)
	}
	if v := r.Header.Get(testupstreamlib.ExpectedHeadersKey); v != "" {
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

	if v := r.Header.Get(testupstreamlib.NonExpectedRequestHeadersKey); v != "" {
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

	if v := r.Header.Get(testupstreamlib.ExpectedTestUpstreamIDKey); v != "" {
		if os.Getenv("TESTUPSTREAM_ID") != v {
			msg := fmt.Sprintf("unexpected testupstream-id: received by '%s' but expected '%s'\n", os.Getenv("TESTUPSTREAM_ID"), v)
			logger.Println(msg)
			http.Error(w, msg, http.StatusBadRequest)
			return
		}
		logger.Println("testupstream-id matched:", v)
	} else {
		logger.Println("no expected testupstream-id")
	}

	if expectedPath := r.Header.Get(testupstreamlib.ExpectedPathHeaderKey); expectedPath != "" {
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

	if expectedReqBody := r.Header.Get(testupstreamlib.ExpectedRequestBodyHeaderKey); expectedReqBody != "" {
		var expectedBody []byte
		expectedBody, err = base64.StdEncoding.DecodeString(expectedReqBody)
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

	if v := r.Header.Get(testupstreamlib.ResponseHeadersKey); v != "" {
		var responseHeaders []byte
		responseHeaders, err = base64.StdEncoding.DecodeString(v)
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
	if v := r.Header.Get(testupstreamlib.ResponseStatusKey); v != "" {
		status, err = strconv.Atoi(v)
		if err != nil {
			logger.Println("failed to parse the response status")
			http.Error(w, "failed to parse the response status", http.StatusBadRequest)
			return
		}
	}

	switch r.Header.Get(testupstreamlib.ResponseTypeKey) {
	case "sse":
		w.Header().Set("Content-Type", "text/event-stream")
		var expResponseBody []byte
		expResponseBody, err = base64.StdEncoding.DecodeString(r.Header.Get(testupstreamlib.ResponseBodyHeaderKey))
		if err != nil {
			logger.Println("failed to decode the response body")
			http.Error(w, "failed to decode the response body", http.StatusBadRequest)
			return
		}

		w.WriteHeader(status)
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
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")

		var expResponseBody []byte
		expResponseBody, err = base64.StdEncoding.DecodeString(r.Header.Get(testupstreamlib.ResponseBodyHeaderKey))
		if err != nil {
			logger.Println("failed to decode the response body")
			http.Error(w, "failed to decode the response body", http.StatusBadRequest)
			return
		}

		w.WriteHeader(status)
		e := eventstream.NewEncoder()
		for _, line := range bytes.Split(expResponseBody, []byte("\n")) {
			// Write each line as a chunk with AWS Event Stream format.
			if len(line) == 0 {
				continue
			}
			time.Sleep(streamingInterval)
			if err = e.Encode(w, eventstream.Message{
				Headers: eventstream.Headers{{Name: "event-type", Value: eventstream.StringValue("content")}},
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
				logger.Println("failed to get the fake response")
				http.Error(w, "failed to get the fake response", http.StatusBadRequest)
				return
			}
		} else {
			responseBody, err = base64.StdEncoding.DecodeString(expResponseBody)
			if err != nil {
				logger.Println("failed to decode the response body")
				http.Error(w, "failed to decode the response body", http.StatusBadRequest)
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
	case "/v1/chat/completions":
		const template = `{"choices":[{"message":{"role":"assistant", "content":"%s"}}]}`
		msg := fmt.Sprintf(template,
			//nolint:gosec
			chatCompletionFakeResponses[rand.New(rand.NewSource(uint64(time.Now().UnixNano()))).
				Intn(len(chatCompletionFakeResponses))])
		return []byte(msg), nil
	default:
		return nil, fmt.Errorf("unknown path: %s", path)
	}
}
