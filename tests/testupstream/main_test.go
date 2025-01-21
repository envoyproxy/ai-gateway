package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/stretchr/testify/require"
)

func Test_main(t *testing.T) {
	t.Setenv("TESTUPSTREAM_ID", "aaaaaaaaa")
	t.Setenv("STREAMING_INTERVAL", "200ms")

	l, err := net.Listen("tcp", ":0") // nolint: gosec
	require.NoError(t, err)
	go func() {
		doMain(l)
	}()

	t.Run("sse", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequest("GET", "http://"+l.Addr().String()+"/sse", nil)
		require.NoError(t, err)
		request.Header.Set(responseBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte(strings.Join([]string{"1", "2", "3", "4", "5"}, "\n"))))

		now := time.Now()
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusOK, response.StatusCode)

		reader := bufio.NewReader(response.Body)
		for i := 0; i < 5; i++ {
			eventLine, err := reader.ReadString('\n')
			require.NoError(t, err)
			require.NoError(t, err)
			require.Equal(t, "event: some event in testupstream\n", eventLine)

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
		request, err := http.NewRequest("GET", "http://"+l.Addr().String()+"/health", nil)
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
		request, err := http.NewRequest("GET",
			"http://"+l.Addr().String()+"/thisisrealpath", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(expectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/foobar")))

		request.Header.Set(expectedRequestBodyHeaderKey,
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
		request, err := http.NewRequest("GET",
			"http://"+l.Addr().String()+"/", bytes.NewBuffer([]byte("not expected request body")))
		require.NoError(t, err)

		request.Header.Set(expectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))
		request.Header.Set(expectedPathHeaderKey,
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
		request, err := http.NewRequest("GET",
			"http://"+l.Addr().String()+"/", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		request.Header.Set(expectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/")))
		request.Header.Set(nonExpectedRequestHeadersKey,
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
		request, err := http.NewRequest("GET",
			"http://"+l.Addr().String()+"/foobar", bytes.NewBuffer([]byte("expected request body")))
		require.NoError(t, err)

		expectedHeaders := []byte("x-foo:bar,x-baz:qux")
		request.Header.Set(expectedHeadersKey,
			base64.StdEncoding.EncodeToString(expectedHeaders))
		request.Header.Set(responseStatusKey, "404")
		request.Header.Set("x-foo", "bar")
		request.Header.Set("x-baz", "qux")

		request.Header.Set(expectedPathHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("/foobar")))
		request.Header.Set(expectedRequestBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("expected request body")))
		request.Header.Set(responseBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte("response body")))
		request.Header.Set(responseHeadersKey,
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

	t.Run("aws-event-stream", func(t *testing.T) {
		t.Parallel()
		request, err := http.NewRequest("GET", "http://"+l.Addr().String()+"/aws-event-stream", nil)
		require.NoError(t, err)
		request.Header.Set(responseBodyHeaderKey,
			base64.StdEncoding.EncodeToString([]byte(strings.Join([]string{"1", "2", "3", "4", "5"}, "\n"))))

		now := time.Now()
		response, err := http.DefaultClient.Do(request)
		require.NoError(t, err)
		defer func() {
			_ = response.Body.Close()
		}()
		require.Equal(t, http.StatusOK, response.StatusCode)

		decoder := eventstream.NewDecoder()
		for i := 0; i < 5; i++ {
			message, err := decoder.Decode(response.Body, nil)
			require.NoError(t, err)
			require.Equal(t, "content", message.Headers.Get("event-type").String())
			require.Equal(t, fmt.Sprintf("%d", i+1), string(message.Payload))

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
}
