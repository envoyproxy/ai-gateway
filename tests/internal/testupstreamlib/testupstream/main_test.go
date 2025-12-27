// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/goccy/go-json"
	"github.com/openai/openai-go"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

func TestMain(m *testing.M) {
	logger = log.New(io.Discard, "", 0)
	os.Exit(m.Run())
}

func Test_main(t *testing.T) {
	t.Setenv("TESTUPSTREAM_ID", "aaaaaaaaa")
	t.Setenv("STREAMING_INTERVAL", "200ms")

	l, err := net.Listen("tcp", ":0") // nolint: gosec
	require.NoError(t, err)
	go func() {
		defer l.Close()
		doMain(l)
	}()

	t.Run("sse", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+l.Addr().String()+"/sse", strings.NewReader("some-body"))
		require.NoError(t, err)
		request.Header.Set(testupstreamlib.ResponseTypeKey, "sse")
		request.Header.Set(testupstreamlib.ResponseBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte(strings.Join([]string{"1", "2", "3", "4", "5"}, "\n"))))

		now := time.Now()
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusOK, response.StatusCode)

		reader := bufio.NewReader(response.Body)
		for i := range 5 {
			dataLine, err := reader.ReadString('\n')
			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf("data: %d\n", i+1), dataLine)
			// Ensure that the server sends the response line every second.
			require.Greater(t, time.Since(now), 100*time.Millisecond, time.Since(now).String())
			require.Less(t, time.Since(now), 300*time.Millisecond, time.Since(now).String())
			now = time.Now()

			// Ignore the additional newline character.
			_, err = reader.ReadString('\n')
			require.NoError(t, err)
		}
	})

	t.Run("health", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+l.Addr().String()+"/health", nil)
		require.NoError(t, err)
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusOK, response.StatusCode)
	})

	t.Run("not expected path", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/thisisrealpath", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/foobar")))

		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()

		require.Equal(t, http.StatusBadRequest, response.StatusCode)

		responseBody, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, "unexpected path: got /thisisrealpath, expected /foobar\n", string(responseBody))
	})

	t.Run("not expected body", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/", bytes.NewBuffer([]byte("not expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))
		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/")))

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusBadRequest, response.StatusCode)

		responseBody, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, "unexpected request body: got not expected request body, expected expected request body\n", string(responseBody))
	})

	t.Run("not expected header", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/")))
		request.Header.Set(testupstreamlib.NonExpectedRequestHeadersKey,
			base64.StdEncoding.EncodeToString([]byte("x-foo")))
		request.Header.Set("x-foo", "not-bar")

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusBadRequest, response.StatusCode)
	})

	t.Run("expected body", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/foobar", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		expectedHeaders := []byte("x-foo:bar,x-baz:qux")
		request.Header.Set(testupstreamlib.ExpectedHeadersKey,
			base64.StdEncoding.EncodeToString(expectedHeaders))
		request.Header.Set(testupstreamlib.ResponseStatusKey, "404")
		request.Header.Set("x-foo", "bar")
		request.Header.Set("x-baz", "qux")

		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/foobar")))
		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))
		request.Header.Set(testupstreamlib.ResponseBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("response body")))
		request.Header.Set(testupstreamlib.ResponseHeadersKey,
			base64.StdEncoding.EncodeToString([]byte("response_header:response_value")))

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()

		require.Equal(t, http.StatusNotFound, response.StatusCode)

		responseBody, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, "response body", string(responseBody))
		require.Equal(t, "response_value", response.Header.Get("response_header"))

		require.Equal(t, "aaaaaaaaa", response.Header.Get("testupstream-id"))
	})

	t.Run("invalid response body", func(t *testing.T) {
		for _, eventType := range []string{"sse", "aws-event-stream"} {
			t.Run(eventType, func(t *testing.T) {
				t.Parallel()
				request, err := http.NewRequestWithContext(t.Context(), "GET",
					"http://"+l.Addr().String()+"/v1/chat/completions", bytes.NewBuffer([]byte("expected request body")))
				require.NoError(t, err)
				request.Header.Set(testupstreamlib.ResponseTypeKey, eventType)
				request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
					base64.StdEncoding.EncodeToString([]byte("/v1/chat/completions")))
				request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
					base64.StdEncoding.EncodeToString([]byte("expected request body")))
				request.Header.Set(testupstreamlib.ResponseBodyHeaderKey, "09i,30qg9i4,gq03,gq0")

				response, err := http.DefaultClient.Do(request)
				require.NoError(t, err)
				defer func() {
					_ = response.Body.Close()
				}()

				require.Equal(t, http.StatusBadRequest, response.StatusCode)
			})
		}
	})

	t.Run("fake response", func(t *testing.T) {
		t.Parallel()
		for _, isGzip := range []bool{false, true} {
			t.Run(fmt.Sprintf("gzip=%t", isGzip), func(t *testing.T) {
				request, err := http.NewRequestWithContext(t.Context(), "GET",
					"http://"+l.Addr().String()+"/v1/chat/completions", bytes.NewBuffer([]byte("expected request body")))
				require.NoError(t, err)

				request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
					base64.StdEncoding.EncodeToString([]byte("/v1/chat/completions")))
				request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
					base64.StdEncoding.EncodeToString([]byte("expected request body")))
				if isGzip {
					request.Header.Set(testupstreamlib.ResponseTypeKey, "gzip")
				}

				response, err := http.DefaultClient.Do(request)
				require.NoError(t, err)
				defer func() {
					_ = response.Body.Close()
				}()

				require.Equal(t, http.StatusOK, response.StatusCode)

				responseBody, err := io.ReadAll(response.Body)
				require.NoError(t, err)

				var chat openai.ChatCompletion
				require.NoError(t, chat.UnmarshalJSON(responseBody))
				// Ensure that the response is one of the fake responses.
				require.Contains(t, chatCompletionFakeResponses, chat.Choices[0].Message.Content)
			})
		}
	})

	t.Run("fake response for embeddings", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequest("POST",
			"http://"+l.Addr().String()+"/v1/embeddings", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/v1/embeddings")))
		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()

		require.Equal(t, http.StatusOK, response.StatusCode)

		responseBody, err := io.ReadAll(response.Body)
		require.NoError(t, err)

		// Verify it's valid JSON with expected structure.
		var embeddingResponse openai.CreateEmbeddingResponse
		require.NoError(t, json.Unmarshal(responseBody, &embeddingResponse))

		// Verify structure and values.
		require.Equal(t, "list", string(embeddingResponse.Object))
		require.Equal(t, "some-cool-self-hosted-model", embeddingResponse.Model)

		require.Len(t, embeddingResponse.Data, 1)

		require.Equal(t, "embedding", string(embeddingResponse.Data[0].Object))
		require.Equal(t, int64(0), embeddingResponse.Data[0].Index)

		require.Equal(t, []float64{0.1, 0.2, 0.3, 0.4, 0.5}, embeddingResponse.Data[0].Embedding)
		require.Equal(t, int64(3), embeddingResponse.Usage.PromptTokens)
		require.Equal(t, int64(3), embeddingResponse.Usage.TotalTokens)
	})

	t.Run("fake response for unknown path", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/foo", nil)
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/foo")))

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()

		require.Equal(t, http.StatusBadRequest, response.StatusCode)
	})

	t.Run("aws-event-stream", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+l.Addr().String()+"/", strings.NewReader("some-body"))
		require.NoError(t, err)
		request.Header.Set(testupstreamlib.ResponseTypeKey, "aws-event-stream")
		request.Header.Set(testupstreamlib.ResponseBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte(strings.Join([]string{
				"{\"contentBlockIndex\": 0, \"delta\":{\"text\":\"1\"}}",
				"{\"contentBlockIndex\": 0, \"delta\":{\"text\":\"2\"}}",
				"{\"contentBlockIndex\": 0, \"delta\":{\"text\":\"3\"}}",
				"{\"contentBlockIndex\": 0, \"delta\":{\"text\":\"4\"}}",
				"{\"contentBlockIndex\": 0, \"delta\":{\"text\":\"5\"}}",
			}, "\n"))))

		now := time.Now()
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusOK, response.StatusCode)

		decoder := eventstream.NewDecoder()
		for i := range 5 {
			var message eventstream.Message
			message, err = decoder.Decode(response.Body, nil)
			require.NoError(t, err)
			require.Equal(t, "contentBlockDelta", message.Headers.Get(":event-type").String())
			require.JSONEq(t, fmt.Sprintf("{\"contentBlockIndex\": 0, \"delta\":{\"text\":\"%d\"}}", i+1), string(message.Payload))

			// Ensure that the server sends the response line every second.
			require.Greater(t, time.Since(now), 100*time.Millisecond, time.Since(now).String())
			require.Less(t, time.Since(now), 300*time.Millisecond, time.Since(now).String())
			now = time.Now()
		}

		// Read the last event.
		event, err := decoder.Decode(response.Body, nil)
		require.NoError(t, err)
		require.Equal(t, "end", event.Headers.Get("event-type").String())

		// Now the reader should return io.EOF.
		_, err = decoder.Decode(response.Body, nil)
		require.Equal(t, io.EOF, err)
	})

	t.Run("expected host not match", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/")))
		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))
		request.Header.Set(testupstreamlib.ExpectedHostKey,
			base64.StdEncoding.EncodeToString([]byte("example.com")))

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()

		require.Equal(t, http.StatusBadRequest, response.StatusCode)
	})
	t.Run("expected raw query not match", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/")))
		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))
		request.Header.Set(testupstreamlib.ExpectedRawQueryHeaderKey, "alt=sse")

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()

		bdy, err := io.ReadAll(response.Body)
		require.NoError(t, err)
		require.Equal(t, http.StatusBadRequest, response.StatusCode)
		require.Contains(t, string(bdy), "unexpected raw query: got , expected alt=sse")
	})

	t.Run("expected host match", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/v1/chat/completions", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Host = "localhost"
		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))
		request.Header.Set(testupstreamlib.ExpectedHostKey, "localhost")

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusOK, response.StatusCode)
	})

	t.Run("expected headers invalid encoding", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/")))
		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))
		request.Header.Set(testupstreamlib.ExpectedHeadersKey, "fewoamfwoajfum092um3f")

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusBadRequest, response.StatusCode)
	})

	t.Run("expected headers invalid pairs", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/")))
		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))
		request.Header.Set(testupstreamlib.ExpectedHeadersKey,
			base64.StdEncoding.EncodeToString([]byte("x-baz"))) // Missing value.

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusBadRequest, response.StatusCode)
	})

	t.Run("expected headers not match", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/")))
		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))
		request.Header.Set(testupstreamlib.ExpectedHeadersKey,
			base64.StdEncoding.EncodeToString([]byte("x-foo:bar,x-baz:qux")))

		request.Header.Set("x-foo", "not-bar")

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusBadRequest, response.StatusCode)
	})

	t.Run("non expected headers invalid encoding", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/")))
		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))
		request.Header.Set(testupstreamlib.NonExpectedRequestHeadersKey, "fewoamfwoajfum092um3f")

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusBadRequest, response.StatusCode)
	})

	t.Run("expected test upstream id", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/v1/chat/completions", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))
		request.Header.Set(testupstreamlib.ExpectedTestUpstreamIDKey, "aaaaaaaaa")

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusOK, response.StatusCode)
	})

	t.Run("expected test upstream id not match", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/v1/chat/completions", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))
		request.Header.Set(testupstreamlib.ExpectedTestUpstreamIDKey, "bbbbbbbbb")

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusBadRequest, response.StatusCode)
	})

	t.Run("expected path invalid encoding", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey, "fewoamfwoajfum092um3f")

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusBadRequest, response.StatusCode)
	})

	t.Run("expected request body invalid encoding", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET",
			"http://"+l.Addr().String()+"/", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey, "fewoamfwoajfum092um3f")

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusBadRequest, response.StatusCode)
	})
	t.Run("sse with distinct event blocks", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+l.Addr().String()+"/sse", strings.NewReader("some-body"))
		require.NoError(t, err)

		// Define two complete SSE events, separated by "\n\n".
		// This structure is designed to hit the `bytes.Split(expResponseBody, []byte("\n\n"))` logic.
		ssePayload := "data: 1\n\ndata: 2"

		request.Header.Set(testupstreamlib.ResponseTypeKey, "sse")
		request.Header.Set(testupstreamlib.ResponseBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte(ssePayload)))

		now := time.Now()
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()

		require.Equal(t, http.StatusOK, response.StatusCode)

		reader := bufio.NewReader(response.Body)
		for i := range 2 {
			dataLine, err := reader.ReadString('\n')
			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf("data: %d\n", i+1), dataLine)
			// Ensure that the server sends the response line every second.
			require.Greater(t, time.Since(now), 100*time.Millisecond, time.Since(now).String())
			require.Less(t, time.Since(now), 300*time.Millisecond, time.Since(now).String())
			now = time.Now()

			// Ignore the additional newline character.
			_, err = reader.ReadString('\n')
			require.NoError(t, err)
		}
	})
	t.Run("sse with empty block should be skipped", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequestWithContext(t.Context(), "GET", "http://"+l.Addr().String()+"/sse", strings.NewReader("some-body"))
		require.NoError(t, err)

		// This payload contains an empty block between two valid SSE messages.
		// The server is expected to split by "\n\n", find the empty block, and skip it.
		ssePayload := "data: first\n\n\n\ndata: second"

		request.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/sse")))
		request.Header.Set(testupstreamlib.ResponseTypeKey, "sse")
		request.Header.Set(testupstreamlib.ResponseBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte(ssePayload)))

		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()

		require.Equal(t, http.StatusOK, response.StatusCode)

		bodyBytes, err := io.ReadAll(response.Body)
		require.NoError(t, err)

		// The expected response should only contain the two valid data blocks.
		// The empty block should have been filtered out by the server.
		expectedBody := "data: first\n\ndata: second\n\n"
		require.Equal(t, expectedBody, string(bodyBytes))
	})
}
