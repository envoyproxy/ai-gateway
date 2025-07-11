// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_extproc

package extproc

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	openaigo "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
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
	now := time.Unix(int64(time.Now().Second()), 0).UTC()

	requireWriteFilterConfig(t, configPath, &filterapi.Config{
		MetadataNamespace: "ai_gateway_llm_ns",
		LLMRequestCosts: []filterapi.LLMRequestCost{
			{MetadataKey: "used_token", Type: filterapi.LLMRequestCostTypeInputToken},
		},
		Schema:             openAISchema,
		ModelNameHeaderKey: "x-model-name",
		Backends: []filterapi.Backend{
			alwaysFailingBackend,
			testUpstreamOpenAIBackend,
			testUpstreamModelNameOverride,
			testUpstreamAAWSBackend,
			testUpstreamAzureBackend,
			testUpstreamGCPVertexAIBackend,
		},
		Models: []filterapi.Model{
			{Name: "some-model1", OwnedBy: "Envoy AI Gateway", CreatedAt: now},
			{Name: "some-model2", OwnedBy: "Envoy AI Gateway", CreatedAt: now},
			{Name: "some-model3", OwnedBy: "Envoy AI Gateway", CreatedAt: now},
		},
	})

	expectedModels := openai.ModelList{
		Object: "list",
		Data: []openai.Model{
			{ID: "some-model1", Object: "model", OwnedBy: "Envoy AI Gateway", Created: openai.JSONUNIXTime(now)},
			{ID: "some-model2", Object: "model", OwnedBy: "Envoy AI Gateway", Created: openai.JSONUNIXTime(now)},
			{ID: "some-model3", Object: "model", OwnedBy: "Envoy AI Gateway", Created: openai.JSONUNIXTime(now)},
		},
	}

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
		// responseStatus is the HTTP status code of the response returned by the test upstream.
		responseStatus,
		// responseHeaders are the headers sent in the HTTP response
		// The value is a base64 encoded string of comma separated key-value pairs.
		// E.g. "key1:value1,key2:value2".
		responseHeaders,
		// expPath is the expected path to be sent to the test upstream.
		expPath string
		// expHost is the expected host to be sent to the test upstream.
		expHost string
		// expHeaders are the expected headers to be sent to the test upstream.
		// The value is a base64 encoded string of comma separated key-value pairs.
		// E.g. "key1:value1,key2:value2".
		expHeaders map[string]string
		// expRequestBody is the expected body to be sent to the test upstream.
		// This can be used to test the request body translation.
		expRequestBody string
		// expStatus is the expected status code from the gateway.
		expStatus int
		// expResponseBody is the expected body from the gateway to the client.
		expResponseBody string
		// expResponseBodyFunc is a function to check the response body. This can be used instead of the expResponseBody field.
		expResponseBodyFunc func(require.TestingT, []byte)
	}{
		{
			name:            "unknown path",
			path:            "/unknown",
			requestBody:     `{"prompt": "hello"}`,
			expStatus:       http.StatusNotFound,
			expResponseBody: `unsupported path: /unknown`,
		},
		{
			name:            "aws system role - /v1/chat/completions",
			backend:         "aws-bedrock",
			path:            "/v1/chat/completions",
			requestBody:     `{"model":"something","messages":[{"role":"system","content":"You are a chatbot."}]}`,
			expPath:         "/model/something/converse",
			responseBody:    `{"output":{"message":{"content":[{"text":"response"},{"text":"from"},{"text":"assistant"}],"role":"assistant"}},"stopReason":null,"usage":{"inputTokens":10,"outputTokens":20,"totalTokens":30}}`,
			expRequestBody:  `{"inferenceConfig":{},"messages":[],"system":[{"text":"You are a chatbot."}]}`,
			expStatus:       http.StatusOK,
			expResponseBody: `{"choices":[{"finish_reason":"stop","index":0,"logprobs":{},"message":{"content":"response","role":"assistant"}}],"object":"chat.completion","usage":{"completion_tokens":20,"prompt_tokens":10,"total_tokens":30}}`,
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
			name:            "openai - /v1/chat/completions - gzip",
			backend:         "openai",
			responseType:    "gzip",
			path:            "/v1/chat/completions",
			method:          http.MethodPost,
			requestBody:     `{"model":"something","messages":[{"role":"system","content":"You are a chatbot."}]}`,
			expPath:         "/v1/chat/completions",
			responseBody:    `{"choices":[{"message":{"content":"This is a test."}}]}`,
			expStatus:       http.StatusOK,
			expResponseBody: `{"choices":[{"message":{"content":"This is a test."}}]}`,
		},
		{
			name:            "azure-openai - /v1/chat/completions",
			backend:         "azure-openai",
			path:            "/v1/chat/completions",
			method:          http.MethodPost,
			requestBody:     `{"model":"something","messages":[{"role":"system","content":"You are a chatbot."}]}`,
			expPath:         "/openai/deployments/something/chat/completions",
			responseBody:    `{"choices":[{"message":{"content":"This is a test."}}]}`,
			expStatus:       http.StatusOK,
			expResponseBody: `{"choices":[{"message":{"content":"This is a test."}}]}`,
		},
		{
			name:            "gcp-vertexai - /v1/chat/completions",
			backend:         "gcp-vertexai",
			path:            "/v1/chat/completions",
			method:          http.MethodPost,
			requestBody:     `{"model":"gemini-1.5-pro","messages":[{"role":"system","content":"You are a helpful assistant."}]}`,
			expRequestBody:  `{"contents":null,"tools":null,"generation_config":{},"system_instruction":{"parts":[{"text":"You are a helpful assistant."}]}}`,
			expHost:         "gcp-region-aiplatform.googleapis.com",
			expPath:         "/v1/projects/gcp-project-name/locations/gcp-region/publishers/google/models/gemini-1.5-pro:generateContent",
			expHeaders:      map[string]string{"Authorization": "Bearer " + fakeGCPAuthToken},
			responseStatus:  strconv.Itoa(http.StatusOK),
			responseBody:    `{"candidates":[{"content":{"parts":[{"text":"This is a test response from Gemini."}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":15,"candidatesTokenCount":10,"totalTokenCount":25}}`,
			expStatus:       http.StatusOK,
			expResponseBody: `{"choices":[{"finish_reason":"stop","index":0,"logprobs":{},"message":{"content":"This is a test response from Gemini.","role":"assistant"}}],"object":"chat.completion","usage":{"completion_tokens":10,"prompt_tokens":15,"total_tokens":25}}`,
		},
		{
			name:            "modelname-override - /v1/chat/completions",
			backend:         "modelname-override",
			path:            "/v1/chat/completions",
			method:          http.MethodPost,
			requestBody:     `{"model":"requested-model","messages":[{"role":"system","content":"You are a chatbot."}]}`,
			expRequestBody:  `{"model":"override-model","messages":[{"role":"system","content":"You are a chatbot."}]}`,
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
			expRequestBody: `{"inferenceConfig":{},"messages":[],"system":[{"text":"You are a chatbot."}]}`,
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
		{
			name:            "gcp-vertexai - /v1/chat/completions - error response",
			backend:         "gcp-vertexai",
			path:            "/v1/chat/completions",
			responseType:    "",
			method:          http.MethodPost,
			requestBody:     `{"model":"gemini-1.5-pro","messages":[{"role":"system","content":"You are a helpful assistant."}]}`,
			expPath:         "/v1/projects/gcp-project-name/locations/gcp-region/publishers/google/models/gemini-1.5-pro:generateContent",
			responseStatus:  "400",
			expStatus:       http.StatusBadRequest,
			responseBody:    `{"error":{"code":400,"message":"Invalid request: missing required field","status":"INVALID_ARGUMENT"}}`,
			expResponseBody: `{"error":{"code":400,"message":"Invalid request: missing required field","status":"INVALID_ARGUMENT"}}`,
		},
		{
			name:            "openai - /v1/embeddings",
			backend:         "openai",
			path:            "/v1/embeddings",
			method:          http.MethodPost,
			requestBody:     `{"model":"text-embedding-ada-002","input":"The food was delicious and the waiter..."}`,
			expPath:         "/v1/embeddings",
			responseBody:    `{"object":"list","data":[{"object":"embedding","embedding":[0.0023064255,-0.009327292,-0.0028842222],"index":0}],"model":"text-embedding-ada-002","usage":{"prompt_tokens":8,"total_tokens":8}}`,
			expStatus:       http.StatusOK,
			expResponseBody: `{"object":"list","data":[{"object":"embedding","embedding":[0.0023064255,-0.009327292,-0.0028842222],"index":0}],"model":"text-embedding-ada-002","usage":{"prompt_tokens":8,"total_tokens":8}}`,
		},
		{
			name:            "openai - /v1/embeddings - gzip",
			backend:         "openai",
			responseType:    "gzip",
			path:            "/v1/embeddings",
			method:          http.MethodPost,
			requestBody:     `{"model":"text-embedding-ada-002","input":"The food was delicious and the waiter..."}`,
			expPath:         "/v1/embeddings",
			responseBody:    `{"object":"list","data":[{"object":"embedding","embedding":[0.0023064255,-0.009327292,-0.0028842222],"index":0}],"model":"text-embedding-ada-002","usage":{"prompt_tokens":8,"total_tokens":8}}`,
			expStatus:       http.StatusOK,
			expResponseBody: `{"object":"list","data":[{"object":"embedding","embedding":[0.0023064255,-0.009327292,-0.0028842222],"index":0}],"model":"text-embedding-ada-002","usage":{"prompt_tokens":8,"total_tokens":8}}`,
		},
		{
			name:            "openai - /v1/embeddings - error response",
			backend:         "openai",
			path:            "/v1/embeddings",
			responseType:    "",
			method:          http.MethodPost,
			requestBody:     `{"model":"text-embedding-ada-002","input":""}`,
			expPath:         "/v1/embeddings",
			responseStatus:  "400",
			expStatus:       http.StatusBadRequest,
			responseBody:    `{"error": {"message": "input cannot be empty", "type": "BadRequestError", "code": "400"}}`,
			expResponseBody: `{"error": {"message": "input cannot be empty", "type": "BadRequestError", "code": "400"}}`,
		},
		{
			name:                "openai - /v1/models",
			backend:             "openai",
			path:                "/v1/models",
			method:              http.MethodGet,
			expStatus:           http.StatusOK,
			expResponseBodyFunc: checkModels(expectedModels),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Eventually(t, func() bool {
				req, err := http.NewRequest(tc.method, listenerAddress+tc.path, strings.NewReader(tc.requestBody))
				require.NoError(t, err)
				req.Header.Set("x-test-backend", tc.backend)
				req.Header.Set(testupstreamlib.ResponseBodyHeaderKey, base64.StdEncoding.EncodeToString([]byte(tc.responseBody)))
				req.Header.Set(testupstreamlib.ExpectedPathHeaderKey, base64.StdEncoding.EncodeToString([]byte(tc.expPath)))
				req.Header.Set(testupstreamlib.ResponseStatusKey, tc.responseStatus)

				var expHeaders []string
				for k, v := range tc.expHeaders {
					expHeaders = append(expHeaders, fmt.Sprintf("%s:%s", k, v))
				}
				if len(expHeaders) > 0 {
					req.Header.Set(
						testupstreamlib.ExpectedHeadersKey,
						base64.StdEncoding.EncodeToString(
							[]byte(strings.Join(expHeaders, ","))),
					)
				}

				if tc.expHost != "" {
					req.Header.Set(testupstreamlib.ExpectedHostKey, tc.expHost)
				}
				if tc.responseType != "" {
					req.Header.Set(testupstreamlib.ResponseTypeKey, tc.responseType)
				}
				if tc.responseHeaders != "" {
					req.Header.Set(testupstreamlib.ResponseHeadersKey, base64.StdEncoding.EncodeToString([]byte(tc.responseHeaders)))
				}
				if tc.expRequestBody != "" {
					req.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey, base64.StdEncoding.EncodeToString([]byte(tc.expRequestBody)))
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
				} else if tc.expResponseBodyFunc != nil {
					body, err := io.ReadAll(resp.Body)
					require.NoError(t, err)
					tc.expResponseBodyFunc(t, body)
				}

				return true
			}, eventuallyTimeout, eventuallyInterval)
		})
	}

	t.Run("stream non blocking", func(t *testing.T) {
		// This receives a stream of 20 event messages. The testuptream server sleeps 200 ms between each message.
		// Therefore, if envoy fails to process the response in a streaming manner, the test will fail taking more than 4 seconds.
		client := openaigo.NewClient(
			option.WithBaseURL(listenerAddress+"/v1/"),
			option.WithHeader("x-test-backend", "openai"),
			option.WithHeader(testupstreamlib.ResponseTypeKey, "sse"),
			option.WithHeader(testupstreamlib.ResponseBodyHeaderKey,
				base64.StdEncoding.EncodeToString([]byte(
					`
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" This"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" is"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" a"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":"."},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" This"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" is"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" a"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":"."},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" This"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" is"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" a"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":"."},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" This"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" is"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" a"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":" test"},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[{"index":0,"delta":{"content":"."},"logprobs":null,"finish_reason":null}],"usage":null}
{"id":"chatcmpl-B8ZKlXBoEXZVTtv3YBmewxuCpNW7b","object":"chat.completion.chunk","created":1741382147,"model":"gpt-4o-mini-2024-07-18","service_tier":"default","system_fingerprint":"fp_06737a9306","choices":[],"usage":{"prompt_tokens":25,"completion_tokens":61,"total_tokens":86,"prompt_tokens_details":{"cached_tokens":0,"audio_tokens":0},"completion_tokens_details":{"reasoning_tokens":0,"audio_tokens":0,"accepted_prediction_tokens":0,"rejected_prediction_tokens":0}}}
[DONE]
`,
				))),
		)

		// NewStreaming below will block until the first event is received, so take the time before calling it.
		start := time.Now()
		stream := client.Chat.Completions.NewStreaming(t.Context(), openaigo.ChatCompletionNewParams{
			Messages: []openaigo.ChatCompletionMessageParamUnion{
				openaigo.UserMessage("Say this is a test"),
			},
			Model: "something",
		})
		defer func() {
			_ = stream.Close()
		}()

		asserted := false
		for stream.Next() {
			chunk := stream.Current()
			if len(chunk.Choices) == 0 || chunk.Choices[0].Delta.Content == "" {
				continue
			}
			t.Logf("%v: %v", time.Now(), chunk.Choices[0].Delta.Content)
			// Check each event is received less than a second after the previous one.
			require.Less(t, time.Since(start), time.Second)
			start = time.Now()
			asserted = true
		}
		require.True(t, asserted)
		require.NoError(t, stream.Err())
	})
	t.Run("metrics", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, "http://localhost:1064/metrics", nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		require.Containsf(t, string(body),
			`gen_ai_server_time_per_output_token_seconds_bucket{gen_ai_operation_name="chat",gen_ai_request_model="something",gen_ai_system_name="aws.bedrock",otel_scope_name="envoyproxy/ai-gateway"`,
			"expected metrics in response body:\n%s", string(body),
		)
	})
}

func checkModels(want openai.ModelList) func(t require.TestingT, body []byte) {
	return func(t require.TestingT, body []byte) {
		var models openai.ModelList
		require.NoError(t, json.Unmarshal(body, &models))
		require.Len(t, models.Data, len(want.Data))
		require.Equal(t, want, models)
	}
}
