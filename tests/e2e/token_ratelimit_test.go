// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_e2e

package e2e

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

func Test_Examples_TokenRateLimit(t *testing.T) {
	const manifest = "../../examples/token_ratelimit/token_ratelimit.yaml"
	require.NoError(t, kubectlApplyManifest(t.Context(), manifest))

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-token-ratelimit"
	requireWaitForGatewayPodReady(t, egSelector)

	const modelName = "rate-limit-funky-model"
	makeRequest := func(usedID string, input, output, total int, expStatus int) {
		fwd := requireNewHTTPPortForwarder(t, egNamespace, egSelector, egDefaultServicePort)
		defer fwd.kill()

		requestBody := fmt.Sprintf(`{"messages":[{"role":"user","content":"Say this is a test"}],"model":"%s"}`, modelName)
		const fakeResponseBodyTemplate = `{"choices":[{"message":{"content":"This is a test.","role":"assistant"}}],"stopReason":null,"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d}}`
		fakeResponseBody := fmt.Sprintf(fakeResponseBodyTemplate, input, output, total)

		req, err := http.NewRequest(http.MethodPut, fwd.address()+"/v1/chat/completions", strings.NewReader(requestBody))
		require.NoError(t, err)
		req.Header.Set(testupstreamlib.ResponseBodyHeaderKey, base64.StdEncoding.EncodeToString([]byte(fakeResponseBody)))
		req.Header.Set(testupstreamlib.ExpectedPathHeaderKey, base64.StdEncoding.EncodeToString([]byte("/v1/chat/completions")))
		req.Header.Set("x-user-id", usedID)
		req.Header.Set("Host", "openai.com")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		if resp.StatusCode == http.StatusOK {
			var oaiBody openai.ChatCompletion
			require.NoError(t, json.Unmarshal(body, &oaiBody))
			// Sanity check the fake response is correctly parsed.
			require.Equal(t, "This is a test.", oaiBody.Choices[0].Message.Content)
			require.Equal(t, int64(input), oaiBody.Usage.PromptTokens)
			require.Equal(t, int64(output), oaiBody.Usage.CompletionTokens)
			require.Equal(t, int64(total), oaiBody.Usage.TotalTokens)
		}
		require.Equal(t, expStatus, resp.StatusCode)
	}

	// Test the input token limit.
	baseID := int(time.Now().UnixNano()) // To avoid collision with previous runs.
	usedID := strconv.Itoa(baseID)
	// This input number exceeds the limit.
	makeRequest(usedID, 10000, 0, 0, 200)
	// Any request with the same user ID should be rejected.
	makeRequest(usedID, 0, 0, 0, 429)

	// Test the output token limit.
	usedID = strconv.Itoa(baseID + 1)
	// This output number exceeds the input limit, but should still be allowed.
	makeRequest(usedID, 0, 20, 0, 200)
	// This output number exceeds the output limit.
	makeRequest(usedID, 0, 10000, 0, 200)
	// Any request with the same user ID should be rejected.
	makeRequest(usedID, 0, 0, 0, 429)

	// Test the total token limit.
	usedID = strconv.Itoa(baseID + 2)
	// This total number exceeds the input limit, but should still be allowed.
	makeRequest(usedID, 0, 0, 20, 200)
	// This total number exceeds the output limit, but should still be allowed.
	makeRequest(usedID, 0, 0, 200, 200)
	// This total number exceeds the total limit.
	makeRequest(usedID, 0, 0, 1000000, 200)
	// Any request with the same user ID should be rejected.
	makeRequest(usedID, 0, 0, 0, 429)

	// Test the CEL token limit.
	usedID = strconv.Itoa(baseID + 3)
	// When the input number is 3, the CEL expression returns 100000000 which exceeds the limit.
	makeRequest(usedID, 3, 0, 0, 200)
	// Any request with the same user ID should be rejected.
	makeRequest(usedID, 0, 0, 0, 429)

	require.Eventually(t, func() bool {
		fwd := requireNewHTTPPortForwarder(t, "monitoring", "app=prometheus", 9090)
		defer fwd.kill()
		const query = `sum(gen_ai_client_token_usage_token_sum{gateway_envoyproxy_io_owning_gateway_name = "envoy-ai-gateway-token-ratelimit"}) by (gen_ai_request_model, gen_ai_token_type)`
		req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/query?query=%s", fwd.address(), url.QueryEscape(query)), nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("Failed to query Prometheus: %v", err)
			return false
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		t.Logf("Response: status=%d, body=%s", resp.StatusCode, string(body))
		if resp.StatusCode != http.StatusOK {
			t.Logf("Failed to query Prometheus: status=%s", resp.Status)
			return false
		}
		type prometheusResponse struct {
			Status string `json:"status"`
			Data   struct {
				ResultType string `json:"resultType"`
				Result     []struct {
					Metric map[string]string `json:"metric"`
					Value  []interface{}     `json:"value"`
				}
			}
		}
		var pr prometheusResponse
		require.NoError(t, json.Unmarshal(body, &pr))
		require.Equal(t, "success", pr.Status)
		require.Equal(t, "vector", pr.Data.ResultType)
		var actualTypes []string
		for _, result := range pr.Data.Result {
			require.Equal(t, modelName, result.Metric["gen_ai_request_model"])
			typ := result.Metric["gen_ai_token_type"]
			actualTypes = append(actualTypes, typ)
			t.Logf("Type: %s, Value: %v", typ, result.Value)
		}
		// We should see input, output, and total token types.
		require.ElementsMatch(t, []string{"input", "output", "total"}, actualTypes)
		return true
	}, 2*time.Minute, 1*time.Second)
}
