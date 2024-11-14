package e2e

import (
	"cmp"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testUpstreamCase represents a test case to make a request to the testUpstream via the envoy gateway.
type testUpstreamCase struct {
	name,
	backend string
	additionalHeaders map[string]string
	originalBody,
	originalPath,
	expectedPath,
	expectedRequestBody string
	expectedRequestHeaders, nonExpectedRequestHeaders []string
	responseBody,
	responseHeaders,
	expectedResponseBody string
	expectedResponseHeaders map[string]string
}

func (tc *testUpstreamCase) runPortForwardRetry(t *testing.T, selector string, expStatus int) {
	t.Run(cmp.Or(tc.name, tc.backend), func(t *testing.T) {
		require.Eventually(t, func() bool {
			fwd := newPortForwarder(t, egNamespace, selector, egDefaultPort)
			require.NoError(t, fwd.Start())
			defer fwd.Stop()

			return tc.verify(t, fwd.Address(), expStatus)
		}, 15*time.Second, 1*time.Second)
	})
}

// run is almost the same as runPortForwardRetry, but it creates a port forwarder outside the retry loop.
func (tc *testUpstreamCase) run(t *testing.T, selector string, expStatus int) {
	t.Run(cmp.Or(tc.name, tc.backend), func(t *testing.T) {
		fwd := newPortForwarder(t, egNamespace, selector, egDefaultPort)
		require.NoError(t, fwd.Start())
		defer fwd.Stop()

		require.Eventually(t, func() bool {
			return tc.verify(t, fwd.Address(), expStatus)
		}, 15*time.Second, 1*time.Second)
	})
}

func (tc *testUpstreamCase) verify(t *testing.T, addr string, exceptedHTTPCode int) bool {
	base64ExpectedRequestHeaders := base64.StdEncoding.EncodeToString([]byte(strings.Join(tc.expectedRequestHeaders, ",")))
	base64ExpectedRequestBody := base64.StdEncoding.EncodeToString([]byte(cmp.Or(tc.expectedRequestBody, tc.originalBody)))
	base64ExpectedPath := base64.StdEncoding.EncodeToString([]byte(cmp.Or(tc.expectedPath, tc.originalPath)))
	base64NonExpectedRequestHeaders := base64.StdEncoding.EncodeToString([]byte(strings.Join(tc.nonExpectedRequestHeaders, ",")))
	base64ResponseHeaders := base64.StdEncoding.EncodeToString([]byte(tc.responseHeaders))
	base64ResponseBody := base64.StdEncoding.EncodeToString([]byte(tc.responseBody))

	req, err := http.NewRequest(http.MethodPost,
		fmt.Sprintf("http://%s%s", addr, tc.originalPath),
		strings.NewReader(tc.originalBody))
	require.NoError(t, err)
	req.Header.Set("x-ai-gateway-llm-backend", tc.backend)
	for k, v := range tc.additionalHeaders {
		req.Header.Set(k, v)
	}
	req.Header.Set("x-expected-headers", base64ExpectedRequestHeaders)
	req.Header.Set("x-expected-path", base64ExpectedPath)
	req.Header.Set("x-expected-request-body", base64ExpectedRequestBody)
	req.Header.Set("x-response-headers", base64ResponseHeaders)
	req.Header.Set("x-response-body", base64ResponseBody)
	req.Header.Set("x-non-expected-request-headers", base64NonExpectedRequestHeaders)

	client := http.Client{}
	resp, err := client.Do(req)
	require.NoError(t, err)
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode != exceptedHTTPCode {
		t.Logf("unexpected status code: %d", resp.StatusCode)
		return false
	}
	if exceptedHTTPCode != http.StatusOK {
		// didn't care about response headers and body when code is not 200.
		return true
	}

	actualResponseBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	for k, v := range resp.Header {
		t.Logf("response header: %s: %s", k, v)
	}
	t.Logf("response body: %s", string(actualResponseBody))

	for k, v := range tc.expectedResponseHeaders {
		if v != resp.Header.Get(k) {
			t.Logf("unexpected response header %q: got %q, want %q", k, resp.Header.Get(k), v)
			return false
		}
	}
	expBody := cmp.Or(tc.expectedResponseBody, tc.responseBody)
	if expBody != string(actualResponseBody) {
		t.Logf("unexpected response body: got %q, want %q", string(actualResponseBody), expBody)
		return false
	}
	return true
}

const v1ChatCompletionOpenAIResponseBodyTemplate = `
{
  "object": "chat.completion",
  "choices": [
    {
      "index": 0,
      "message": {
        "role": "assistant",
        "content": "This is a test! How can I assist you today?",
        "refusal": null
      },
      "logprobs": null,
      "finish_reason": "stop"
    }
  ],
  "usage": {
    "prompt_tokens": 13,
    "completion_tokens": 12,
    "total_tokens": %d
  }
}`

func newDefaultV1ChatCompletionCase(backendName, modelName string) *testUpstreamCase {
	const (
		v1ChatCompletionsOpenAIPath               = "/v1/chat/completions"
		v1ChatCompletionOpenAIRequestBodyTemplate = `{"model":"%s","messages":[{"role":"system","content":"You are a chatbot."}]}`
	)

	return &testUpstreamCase{
		backend:      backendName,
		originalBody: fmt.Sprintf(v1ChatCompletionOpenAIRequestBodyTemplate, modelName),
		originalPath: v1ChatCompletionsOpenAIPath,
		responseBody: fmt.Sprintf(v1ChatCompletionOpenAIResponseBodyTemplate, 25),
	}
}

func (tc *testUpstreamCase) setName(name string) *testUpstreamCase {
	tc.name = name
	return tc
}

func (tc *testUpstreamCase) setExpectedRequestHeaders(headers ...string) *testUpstreamCase {
	tc.expectedRequestHeaders = headers
	return tc
}

func (tc *testUpstreamCase) setTotalTokens(totalTokens int) *testUpstreamCase {
	tc.responseBody = fmt.Sprintf(v1ChatCompletionOpenAIResponseBodyTemplate, totalTokens)
	return tc
}

func (tc *testUpstreamCase) setRequestHeaders(headers map[string]string) *testUpstreamCase {
	tc.additionalHeaders = headers
	return tc
}

func (tc *testUpstreamCase) setNonExpectedRequestHeaders(headers ...string) *testUpstreamCase {
	tc.nonExpectedRequestHeaders = headers
	return tc
}
