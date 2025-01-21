//go:build test_extproc

package extproc

import (
	"encoding/base64"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

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

	requireWriteExtProcConfig(t, configPath, &filterconfig.Config{
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
		// respBody is the response body to return from the test upstream.
		respBody,
		// expPath is the expected path to be sent to the test upstream.
		expPath string
		// expStatus is the expected status code from the gateway.
		expStatus int
		// expBody is the expected body from the gateway.
		expBody string
	}{
		{
			name:        "unknown path",
			backend:     "openai",
			path:        "/unknown",
			method:      http.MethodPost,
			requestBody: `{"prompt": "hello"}`,
			respBody:    `{"error": "unknown path"}`,
			expPath:     "/unknown",
			expStatus:   http.StatusInternalServerError,
		},
		{
			name:        "aws - /v1/chat/completions",
			backend:     "aws-bedrock",
			path:        "/v1/chat/completions",
			requestBody: `{"model":"something","messages":[{"role":"system","content":"You are a chatbot."}]}`,
			expPath:     "/model/something/converse",
			respBody:    `{"output":{"message":{"content":[{"text":"response"},{"text":"from"},{"text":"assistant"}],"role":"assistant"}},"stopReason":null,"usage":{"inputTokens":10,"outputTokens":20,"totalTokens":30}}`,
			expStatus:   http.StatusOK,
			expBody:     `{"choices":[{"finish_reason":"stop","index":0,"logprobs":{},"message":{"content":"response","role":"assistant"}},{"finish_reason":"stop","index":1,"logprobs":{},"message":{"content":"from","role":"assistant"}},{"finish_reason":"stop","index":2,"logprobs":{},"message":{"content":"assistant","role":"assistant"}}],"object":"chat.completion","usage":{"completion_tokens":20,"prompt_tokens":10,"total_tokens":30}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Eventually(t, func() bool {
				req, err := http.NewRequest(tc.method, listenerAddress+tc.path, strings.NewReader(tc.requestBody))
				require.NoError(t, err)
				req.Header.Set("x-test-backend", tc.backend)
				req.Header.Set("x-response-body", base64.StdEncoding.EncodeToString([]byte(tc.respBody)))
				req.Header.Set("x-expected-path", base64.StdEncoding.EncodeToString([]byte(tc.expPath)))

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
				if tc.expBody != "" {
					body, err := io.ReadAll(resp.Body)
					require.NoError(t, err)
					if string(body) != tc.expBody {
						t.Logf("unexpected requestBody: %s", body)
						return false
					}
				}
				return true
			}, 10*time.Second, 500*time.Millisecond)
		})
	}
}
