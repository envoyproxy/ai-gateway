// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	openaigo "github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// TestWithTestUpstream tests the end-to-end functionality of the AI Gateway with the testupstream server.
func TestWithTestUpstream(t *testing.T) {
	const manifest = "testdata/testupstream.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=translation-testupstream"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	const dummyToken = "dummy-token"
	t.Run("/chat/completions", func(t *testing.T) {
		for _, tc := range []struct {
			name      string
			modelName string
			// expHost is the expected host that the request should be forwarded to the testupstream server.
			// Assertion will be performed in the testupstream server.
			expHost string
			// expTestUpstreamID is the expected testupstream ID that the request should be forwarded to.
			// This is used to differentiate between different testupstream instances.
			// Assertion will be performed in the testupstream server.
			expTestUpstreamID string
			// expPath is the expected path that the request should be forwarded to the testupstream server.
			// Assertion will be performed in the testupstream server.
			expPath string
			// fakeResponseBody is the body that the testupstream server will return when the request is made.
			fakeResponseBody string
			// expStatus is the expected HTTP status code for the test case.
			expStatus int
			// expResponseBody is the expected response body for the test case. This is optional and can be empty.
			expResponseBody string
			// nonexpectedHeaders are the headers that should NOT be present in the request to the testupstream server.
			nonexpectedHeaders []string
			// reqHeaders are the headers to be included in the request to the AI Gateway.
			reqHeaders map[string]string
		}{
			{
				name:              "openai",
				modelName:         "some-cool-model",
				expTestUpstreamID: "primary",
				expPath:           "/v1/chat/completions",
				expHost:           "testupstream.default.svc.cluster.local",
				fakeResponseBody:  `{"choices":[{"message":{"content":"This is a test."}}]}`,
				expStatus:         200,
			},
			{
				name:              "aws-bedrock",
				modelName:         "another-cool-model",
				expTestUpstreamID: "canary",
				expHost:           "testupstream-canary.default.svc.cluster.local",
				expPath:           "/model/another-cool-model/converse",
				fakeResponseBody:  `{"output":{"message":{"content":[{"text":"response"},{"text":"from"},{"text":"assistant"}],"role":"assistant"}},"stopReason":null,"usage":{"inputTokens":10,"outputTokens":20,"totalTokens":30}}`,
				expStatus:         200,
			},
			{
				name:            "openai",
				modelName:       "non-existent-model",
				expStatus:       404,
				expResponseBody: `No matching route found. It is likely because the model specified in your request is not configured in the Gateway.`,
			},
			{
				name:               "openai-header-mutation",
				modelName:          "some-cool-model",
				expTestUpstreamID:  "primary",
				expPath:            "/v1/chat/completions",
				expHost:            "testupstream.default.svc.cluster.local",
				fakeResponseBody:   `{"choices":[{"message":{"content":"This is a test."}}]}`,
				nonexpectedHeaders: []string{"x-remove-header"},
				reqHeaders:         map[string]string{"x-remove-header": "remove-me"},
				expStatus:          200,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				require.Eventually(t, func() bool {
					fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
					defer fwd.Kill()

					req, err := http.NewRequest(http.MethodPost, fwd.Address()+"/v1/chat/completions", strings.NewReader(fmt.Sprintf(
						`{"messages":[{"role":"user","content":"Say this is a test"}],"model":"%s"}`,
						tc.modelName)))
					require.NoError(t, err)
					req.Header.Set(testupstreamlib.ResponseBodyHeaderKey, base64.StdEncoding.EncodeToString([]byte(tc.fakeResponseBody)))
					req.Header.Set(testupstreamlib.ExpectedPathHeaderKey, base64.StdEncoding.EncodeToString([]byte(tc.expPath)))
					req.Header.Set(testupstreamlib.ExpectedHostKey, tc.expHost)
					req.Header.Set(testupstreamlib.ExpectedTestUpstreamIDKey, tc.expTestUpstreamID)
					for k, v := range tc.reqHeaders {
						req.Header.Set(k, v)
					}
					if tc.modelName == "some-cool-model" {
						req.Header.Set(testupstreamlib.ExpectedHeadersKey,
							base64.StdEncoding.EncodeToString([]byte("Authorization:Bearer "+dummyToken)))
					}

					if len(tc.nonexpectedHeaders) > 0 {
						req.Header.Set(testupstreamlib.NonExpectedRequestHeadersKey, base64.StdEncoding.EncodeToString([]byte(strings.Join(tc.nonexpectedHeaders, ","))))
					}

					resp, err := http.DefaultClient.Do(req)
					if err != nil {
						t.Logf("error: %v", err)
						return false
					}
					defer func() { _ = resp.Body.Close() }()
					body, err := io.ReadAll(resp.Body)
					if err != nil {
						t.Logf("error reading response body: %v", err)
						return false
					}
					if resp.StatusCode != tc.expStatus {
						t.Logf("unexpected status code: %d (expected %d), body: %s", resp.StatusCode, tc.expStatus, body)
						return false
					}
					if tc.expResponseBody != "" && string(body) != tc.expResponseBody {
						t.Logf("unexpected response body: %s (expected %s)", body, tc.expResponseBody)
						return false
					}
					return true
				}, 10*time.Second, 1*time.Second)
			})
		}
	})

	t.Run("non-llm-route", func(t *testing.T) {
		// We should be able to make requests to /non-llm routes as well.
		//
		// If this route is intercepted by the AI Gateway ExtProc, which is unexpected, it would result in 404
		// since "/non-llm-route" is not a valid route at least for the OpenAI API.
		require.Eventually(t, func() bool {
			fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
			defer fwd.Kill()

			req, err := http.NewRequest(http.MethodGet, fwd.Address()+"/non-llm-route", strings.NewReader("somebody"))
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Logf("error: %v", err)
				return false
			}
			defer func() { _ = resp.Body.Close() }()
			body, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Logf("error reading response body: %v", err)
				return false
			}
			if resp.StatusCode != 200 {
				t.Logf("unexpected status code: %d (expected 200), body: %s", resp.StatusCode, body)
				return false
			}
			if string(body) != `{"message":"This is a non-LLM endpoint response"}` {
				t.Logf("unexpected response body: %s", body)
				return false
			}
			return true
		}, 10*time.Second, 1*time.Second)
	})

	// This is a regression test that ensures that stream=true requests are processed in a streaming manner.
	// https://github.com/envoyproxy/ai-gateway/pull/1026
	//
	// We have almost identical test in the tests/extproc.
	t.Run("stream non blocking", func(t *testing.T) {
		fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
		defer fwd.Kill()
		// This receives a stream of 20 event messages. The testuptream server sleeps 200 ms between each message.
		// Therefore, if envoy fails to process the response in a streaming manner, the test will fail taking more than 4 seconds.
		client := openaigo.NewClient(
			option.WithBaseURL(fwd.Address()+"/v1/"),
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
			Model: "whatever-model",
		})

		defer func() {
			_ = stream.Close()
		}()

		asserted := false
		for stream.Next() {
			chunk := stream.Current()
			fmt.Println(chunk)
			if len(chunk.Choices) == 0 || chunk.Choices[0].Delta.Content == "" {
				continue
			}
			t.Logf("%v: %v", time.Now(), chunk.Choices[0].Delta.Content)
			// Check each event is received less than a second after the previous one.
			require.Less(t, time.Since(start), time.Second)
			start = time.Now()
			asserted = true
		}
		require.NoError(t, stream.Err())
		require.True(t, asserted)
	})

	t.Run("secret update propagation", func(t *testing.T) {
		const secretName = "translation-testupstream-default"
		// Verify that the apiKey still exists in the filter-config.yaml secret with the existing value.
		internaltesting.RequireEventuallyNoError(t, func() error {
			secret, err := extractFilterConfigFromSecret(t.Context(), secretName)
			if err != nil {
				return err
			}
			if !strings.Contains(secret, dummyToken) {
				return fmt.Errorf("filter-config.yaml does not contain %s", dummyToken)
			}
			return nil
		}, 10*time.Second, 1*time.Second, "initial secret not found in filter-config.yaml")

		// Update the secret used by the BackendSecurityPolicy to have a new apiKey value.
		const updatedKey = "pikachu"
		secretUpdated := fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: translation-testupstream-cool-model-backend-api-key
  namespace: default
type: Opaque
stringData:
  apiKey: "%s"`, updatedKey)
		require.NoError(t, e2elib.KubectlApplyManifestStdin(t.Context(), secretUpdated))

		// Verify that the new apiKey is propagated to the filter-config.yaml secret.
		internaltesting.RequireEventuallyNoError(t, func() error {
			secret, err := extractFilterConfigFromSecret(t.Context(), secretName)
			if err != nil {
				return err
			}
			if !strings.Contains(secret, updatedKey) {
				return fmt.Errorf("filter-config.yaml does not contain %s", updatedKey)
			}
			return nil
		}, 20*time.Second, 1*time.Second, "updated secret not propagated to filter-config.yaml")
	})
}

// extractFilterConfigFromSecret extracts the filter-config.yaml content from the given secret name.
func extractFilterConfigFromSecret(ctx context.Context, name string) (string, error) {
	ctrl := e2elib.Kubectl(ctx, "get", "secrets", "-n", e2elib.EnvoyGatewayNamespace,
		name, "-o",
		`jsonpath='{.data.filter-config\.yaml}'`)
	ctrl.Stderr = nil
	ctrl.Stdout = nil
	output, err := ctrl.Output()
	if err != nil {
		return "", fmt.Errorf("failed to get filter-config.yaml from secret: %w", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.Trim(string(output), "'"))
	if err != nil {
		return "", fmt.Errorf("failed to base64 decode filter-config.yaml: %w", err)
	}
	return string(decoded), nil
}
