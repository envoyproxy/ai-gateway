//go:build test_extproc

package extproc

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterconfig"
)

// TestWithTestUpstream tests the end-to-end flow of the external processor with Envoy and the test upstream.
//
// This does not require any environment variables to be set as it relies on the test upstream.
func TestWithTestUpstream(t *testing.T) {
	requireBinaries(t)
	accessLogPath := t.TempDir() + "/access.log"
	requireRunEnvoy(t, accessLogPath)
	configPath := t.TempDir() + "/extproc-config.yaml"
	requireTestUpstream(t)

	requireWriteFilterConfig(t, configPath, &filterconfig.Config{
		MetadataNamespace: "ai_gateway_llm_ns",
		LLMRequestCosts: []filterconfig.LLMRequestCost{
			{MetadataKey: "used_token", Type: filterconfig.LLMRequestCostTypeInputToken},
		},
		Schema: openAISchema,
		// This can be any header key, but it must match the envoy.yaml routing configuration.
		SelectedBackendHeaderKey: "x-selected-backend-name",
		ModelNameHeaderKey:       "x-model-name",
		Rules: []filterconfig.RouteRule{
			{
				Backends: []filterconfig.Backend{{Name: "testupstream", Schema: openAISchema}},
				Headers:  []filterconfig.HeaderMatch{{Name: "x-test-backend", Value: "openai"}},
			},
			{
				Backends: []filterconfig.Backend{{Name: "testupstream", Schema: awsBedrockSchema}},
				Headers:  []filterconfig.HeaderMatch{{Name: "x-test-backend", Value: "aws-bedrock"}},
			},
		},
	})

	requireExtProc(t, os.Stdout, extProcExecutablePath(), configPath)

	for _, tc := range []struct {
		// name is the name of the test case.
		name,
		// backend is the backend to send the request to. Either "openai" or "aws-bedrock" (matching the headers in the config).
		backend,
		// path is the path to send the request to.
		path,
		// method is the HTTP method to use.
		method,
		// requestBody is the request requestBody.
		requestBody,
		// responseBody is the response body to return from the test upstream.
		responseBody,
		// responseType is either empty, "sse" or "aws-event-stream" as implemented by the test upstream.
		responseType,
		// responseStatus is the HTTP status code of the response.
		responseStatus,
		// responseHeaders are the headers sent in the HTTP response
		// The value is a base64 encoded string of comma separated key-value pairs.
		// E.g. "key1:value1,key2:value2".
		responseHeaders,
		// expPath is the expected path to be sent to the test upstream.
		expPath string
		// expRequestBody is the expected body to be sent to the test upstream.
		// This can be used to test the request body translation.
		expRequestBody string
		// expStatus is the expected status code from the gateway.
		expStatus int
		// expResponseBody is the expected body from the gateway to the client.
		expResponseBody string
	}{
		{
			name:           "unknown path",
			backend:        "openai",
			path:           "/unknown",
			method:         http.MethodPost,
			requestBody:    `{"prompt": "hello"}`,
			responseBody:   `{"error": "unknown path"}`,
			expPath:        "/unknown",
			responseStatus: "500",
			expStatus:      http.StatusInternalServerError,
		},
		{
			name:            "aws system role - /v1/chat/completions",
			backend:         "aws-bedrock",
			path:            "/v1/chat/completions",
			requestBody:     `{"model":"something","messages":[{"role":"system","content":"You are a chatbot."}]}`,
			expPath:         "/model/something/converse",
			responseBody:    `{"output":{"message":{"content":[{"text":"response"},{"text":"from"},{"text":"assistant"}],"role":"assistant"}},"stopReason":null,"usage":{"inputTokens":10,"outputTokens":20,"totalTokens":30}}`,
			expRequestBody:  `{"inferenceConfig":{},"messages":[],"modelId":null,"system":[{"text":"You are a chatbot."}]}`,
			expStatus:       http.StatusOK,
			expResponseBody: `{"choices":[{"finish_reason":"stop","index":0,"logprobs":{},"message":{"content":"response","role":"assistant"}},{"finish_reason":"stop","index":1,"logprobs":{},"message":{"content":"from","role":"assistant"}},{"finish_reason":"stop","index":2,"logprobs":{},"message":{"content":"assistant","role":"assistant"}}],"object":"chat.completion","usage":{"completion_tokens":20,"prompt_tokens":10,"total_tokens":30}}`,
		},
		{
			name:            "openai - /v1/chat/completions",
			backend:         "openai",
			path:            "/v1/chat/completions",
			method:          http.MethodPost,
			requestBody:     `{"model":"something","messages":[{"role":"system","content":"You are a chatbot."}]}`,
			expPath:         "/v1/chat/completions",
			responseBody:    `{"choices":[{"message":{"content":"This is a test."}}]}`,
			expStatus:       http.StatusOK,
			expResponseBody: `{"choices":[{"message":{"content":"This is a test."}}]}`,
		},
		{
			name:           "aws - /v1/chat/completions - streaming",
			backend:        "aws-bedrock",
			path:           "/v1/chat/completions",
			responseType:   "aws-event-stream",
			method:         http.MethodPost,
			requestBody:    `{"model":"something","messages":[{"role":"system","content":"You are a chatbot."}], "stream": true}`,
			expRequestBody: `{"inferenceConfig":{},"messages":[],"modelId":null,"system":[{"text":"You are a chatbot."}]}`,
			expPath:        "/model/something/converse-stream",
			responseBody: `{"role":"assistant"}
{"start":{"toolUse":{"name":"cosine","toolUseId":"tooluse_QklrEHKjRu6Oc4BQUfy7ZQ"}}}
{"delta":{"text":"Don"}}
{"delta":{"text":"'t worry,  I'm here to help. It"}}
{"delta":{"text":" seems like you're testing my ability to respond appropriately"}}
{"stopReason":"tool_use"}
{"usage":{"inputTokens":41, "outputTokens":36, "totalTokens":77}}
`,
			expStatus: http.StatusOK,
			expResponseBody: `data: {"choices":[{"delta":{"content":"","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"id":"tooluse_QklrEHKjRu6Oc4BQUfy7ZQ","function":{"arguments":"","name":"cosine"},"type":"function"}]}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":"Don","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":"'t worry,  I'm here to help. It","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" seems like you're testing my ability to respond appropriately","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":"","role":"assistant"},"finish_reason":"tool_calls"}],"object":"chat.completion.chunk"}

data: {"object":"chat.completion.chunk","usage":{"completion_tokens":36,"prompt_tokens":41,"total_tokens":77}}

data: [DONE]
`,
		},
		{
			name:         "openai - /v1/chat/completions - streaming",
			backend:      "openai",
			path:         "/v1/chat/completions",
			responseType: "sse",
			method:       http.MethodPost,
			requestBody:  `{"model":"something","messages":[{"role":"system","content":"You are a chatbot."}], "stream": true}`,
			expPath:      "/v1/chat/completions",
			responseBody: `
{"id":"chatcmpl-foo","object":"chat.completion.chunk","created":1731618222,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_0ba0d124f1","choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-foo","object":"chat.completion.chunk","created":1731618222,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_0ba0d124f1","choices":[],"usage":{"prompt_tokens":13,"completion_tokens":12,"total_tokens":25,"prompt_tokens_details":{"cached_tokens":0,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":0,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}}}
[DONE]
`,
			expStatus: http.StatusOK,
			expResponseBody: `data: {"id":"chatcmpl-foo","object":"chat.completion.chunk","created":1731618222,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_0ba0d124f1","choices":[{"index":0,"delta":{"role":"assistant","content":"","refusal":null},"logprobs":null,"finish_reason":null}],"usage":null}

data: {"id":"chatcmpl-foo","object":"chat.completion.chunk","created":1731618222,"model":"gpt-4o-mini-2024-07-18","system_fingerprint":"fp_0ba0d124f1","choices":[],"usage":{"prompt_tokens":13,"completion_tokens":12,"total_tokens":25,"prompt_tokens_details":{"cached_tokens":0,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":0,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}}}

data: [DONE]

`,
		},
		{
			name:            "openai - /v1/chat/completions - error response",
			backend:         "openai",
			path:            "/v1/chat/completions",
			responseType:    "",
			method:          http.MethodPost,
			requestBody:     `{"model":"something","messages":[{"role":"system","content":"You are a chatbot."}], "stream": true}`,
			expPath:         "/v1/chat/completions",
			responseStatus:  "400",
			expStatus:       http.StatusBadRequest,
			responseBody:    `{"error": {"message": "missing required field", "type": "BadRequestError", "code": "400"}}`,
			expResponseBody: `{"error": {"message": "missing required field", "type": "BadRequestError", "code": "400"}}`,
		},
		{
			name:            "aws-bedrock - /v1/chat/completions - error response",
			backend:         "aws-bedrock",
			path:            "/v1/chat/completions",
			responseType:    "",
			method:          http.MethodPost,
			requestBody:     `{"model":"something","messages":[{"role":"system","content":"You are a chatbot."}], "stream": true}`,
			expPath:         "/model/something/converse-stream",
			responseStatus:  "429",
			expStatus:       http.StatusTooManyRequests,
			responseHeaders: "x-amzn-errortype:ThrottledException",
			responseBody:    `{"message": "aws bedrock rate limit exceeded"}`,
			expResponseBody: `{"type":"error","error":{"type":"ThrottledException","code":"429","message":"aws bedrock rate limit exceeded"}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Eventually(t, func() bool {
				req, err := http.NewRequest(tc.method, listenerAddress+tc.path, strings.NewReader(tc.requestBody))
				require.NoError(t, err)
				req.Header.Set("x-test-backend", tc.backend)
				req.Header.Set("x-response-body", base64.StdEncoding.EncodeToString([]byte(tc.responseBody)))
				req.Header.Set("x-expected-path", base64.StdEncoding.EncodeToString([]byte(tc.expPath)))
				req.Header.Set("x-response-status", tc.responseStatus)
				if tc.responseType != "" {
					req.Header.Set("x-response-type", tc.responseType)
				}
				if tc.responseHeaders != "" {
					req.Header.Set("x-response-headers", base64.StdEncoding.EncodeToString([]byte(tc.responseHeaders)))
				}
				if tc.expRequestBody != "" {
					req.Header.Set("x-expected-request-body", base64.StdEncoding.EncodeToString([]byte(tc.expRequestBody)))
				}

				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					t.Logf("error: %v", err)
					return false
				}
				defer func() { _ = resp.Body.Close() }()
				if resp.StatusCode != tc.expStatus {
					t.Logf("unexpected status code: %d", resp.StatusCode)
					return false
				}
				if tc.expResponseBody != "" {
					body, err := io.ReadAll(resp.Body)
					require.NoError(t, err)
					if string(body) != tc.expResponseBody {
						fmt.Printf("unexpected response:\n%s", cmp.Diff(string(body), tc.expResponseBody))
						return false
					}
				}
				return true
			}, 10*time.Second, 500*time.Millisecond)
		})
	}
}
