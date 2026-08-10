// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dataplane

import (
	"cmp"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"sigs.k8s.io/yaml"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/version"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// TestDynamicFallback exercises per-request dynamic backend ordering across shared
// per-provider clusters via the matcher cluster specifier + refresh_cluster_on_retry, against
// the "dynamic-fallback*" routes in envoy.yaml. Beyond the standalone Envoy spike, it proves
// the upstream extproc follows the refreshed cluster on each attempt, resolves rule-scoped
// config over SHARED clusters by composing the route's rule key with the cluster's backend
// key, and re-translates the pristine request per attempt.
func TestDynamicFallback(t *testing.T) {
	// The same shared cluster resolves different rule-scoped config per matched route: endpoint
	// metadata supplies the backend half of these names, route metadata the rule half.
	const (
		rule0 = "default/route/dyn/rule/0"
		rule1 = "default/route/dyn/rule/1"
	)
	config := &filterapi.Config{
		Version:                version.Parse(),
		DynamicFallbackEnabled: true,
		Models:                 []filterapi.Model{{Name: "something", FallbackCandidates: []string{"primary", "secondary"}}},
		Backends: []filterapi.Backend{
			{
				// Attempts bound here get a 500 reply, driving the retry walk without
				// tripping expectation checks.
				Name: rule0 + "/backend/default/openai-5xx", Schema: openAISchema, HeaderMutation: &filterapi.HTTPHeaderMutation{
					Set: []filterapi.HTTPHeader{{Name: testupstreamlib.ResponseStatusKey, Value: "500"}},
				},
			},
			{Name: rule0 + "/backend/default/openai", Schema: openAISchema},
			{Name: rule0 + "/backend/default/aws", Schema: awsBedrockSchema},
			// Rule 1 shares the clusters but overrides the model on the AWS backend.
			{Name: rule1 + "/backend/default/openai", Schema: openAISchema},
			{Name: rule1 + "/backend/default/aws", Schema: awsBedrockSchema, ModelNameOverride: "alt-model"},
		},
	}
	configBytes, err := yaml.Marshal(config)
	require.NoError(t, err)
	env := startTestEnvironment(t, string(configBytes), true, false)
	listenerAddress := fmt.Sprintf("http://localhost:%d", env.EnvoyListenerPort())

	createdReg := regexp.MustCompile(`"created":\d+`)

	const (
		openAIRequestBody = `{"model":"something","messages":[{"role":"system","content":"You are a chatbot."}]}`
		// The AWS Bedrock translation of openAIRequestBody, as asserted by existing AWS cases.
		converseRequestBody = `{"inferenceConfig":{},"messages":[],"system":[{"text":"You are a chatbot."}]}`
		conversePath        = "/model/something/converse"
		// Converse-format response the testupstream returns; only parseable by the AWS
		// translator, so a correct final response proves the secondary attempt was bound to
		// the AWS backend of the refreshed cluster.
		converseResponseBody = `{"output":{"message":{"content":[{"text":"response"},{"text":"from"},{"text":"assistant"}],"role":"assistant"}},"stopReason":null,"usage":{"inputTokens":10,"outputTokens":20,"totalTokens":30}}`
		converseResponseHdr  = "x-amzn-requestid:2bc5b090-a26c-4007-9467-ce5adc4ffa1d"
		expOpenAIResponse    = `{"choices":[{"finish_reason":"stop","index":0,"message":{"content":"response","role":"assistant"}}],"id":"2bc5b090-a26c-4007-9467-ce5adc4ffa1d","created":123,"model":"something","object":"chat.completion","usage":{"completion_tokens":20,"prompt_tokens":10,"total_tokens":30}}`
		// With modelNameOverride, the response reports the model that actually served.
		expOpenAIAltModelResponse = `{"choices":[{"finish_reason":"stop","index":0,"message":{"content":"response","role":"assistant"}}],"id":"2bc5b090-a26c-4007-9467-ce5adc4ffa1d","created":123,"model":"alt-model","object":"chat.completion","usage":{"completion_tokens":20,"prompt_tokens":10,"total_tokens":30}}`
	)

	const openAIPassthroughResponse = `{"choices":[{"message":{"content":"This is a test."}}]}`

	for _, tc := range []struct {
		name, backend  string
		headers        map[string]string
		expPath        string
		expRequestBody string
		// responseBody is what the testupstream returns; defaults to the converse-format body
		// (i.e. the case expects the AWS backend to serve the final attempt).
		responseBody string
		// expResponse is the expected final response; defaults to the OpenAI translation of
		// the converse response.
		expResponse string
	}{
		{
			// Attempt 1 hits the 500ing primary; the refreshed attempt 2 must land on the aws
			// cluster and be re-translated.
			name:    "chain header walks to secondary across clusters",
			backend: "dynamic-fallback",
			headers: map[string]string{internalapi.FallbackChainHeader: "primary,secondary"},
		},
		{
			// The healthy openai primary fails the testupstream's expectation checks (they
			// target the AWS translation) with a retriable 400, so the exact translated path
			// and body the secondary received can be asserted.
			name:           "request translation asserted on secondary",
			backend:        "dynamic-fallback-strict",
			headers:        map[string]string{internalapi.FallbackChainHeader: "primary, secondary"},
			expPath:        conversePath,
			expRequestBody: converseRequestBody,
		},
		{
			// No chain header: the walk degrades to the matcher's declared default order.
			name:    "default order used when no chain is supplied",
			backend: "dynamic-fallback",
			headers: map[string]string{},
		},
		{
			// Rule 1 shares the same clusters but resolves different rule-scoped config: its
			// AWS entry carries modelNameOverride "alt-model", proven by the translated path.
			name:           "per-rule override resolved over shared clusters",
			backend:        "dynamic-fallback-alt",
			headers:        map[string]string{internalapi.FallbackChainHeader: "primary,secondary"},
			expPath:        "/model/alt-model/converse",
			expRequestBody: converseRequestBody,
			expResponse:    expOpenAIAltModelResponse,
		},
		{
			// A forged attempt count + slot header trying to steer attempt 1 to aws must be
			// scrubbed: the request then follows the default order onto the openai primary,
			// proven by an OpenAI-format body only an openai-bound attempt passes through.
			name:    "forged matcher inputs are scrubbed",
			backend: "dynamic-fallback-strict",
			headers: map[string]string{
				internalapi.EnvoyAttemptCountHeader:               "1",
				internalapi.DynamicFallbackSlotHeaderPrefix + "1": "secondary",
			},
			responseBody: openAIPassthroughResponse,
			expResponse:  openAIPassthroughResponse,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost,
				listenerAddress+"/v1/chat/completions", strings.NewReader(openAIRequestBody))
			require.NoError(t, err)
			req.Header.Set("x-test-backend", tc.backend)
			responseBody := cmp.Or(tc.responseBody, converseResponseBody)
			req.Header.Set(testupstreamlib.ResponseBodyHeaderKey,
				base64.StdEncoding.EncodeToString([]byte(responseBody)))
			req.Header.Set(testupstreamlib.ResponseHeadersKey,
				base64.StdEncoding.EncodeToString([]byte(converseResponseHdr)))
			if tc.expPath != "" {
				req.Header.Set(testupstreamlib.ExpectedPathHeaderKey,
					base64.StdEncoding.EncodeToString([]byte(tc.expPath)))
			}
			if tc.expRequestBody != "" {
				req.Header.Set(testupstreamlib.ExpectedRequestBodyHeaderKey,
					base64.StdEncoding.EncodeToString([]byte(tc.expRequestBody)))
			}
			for k, v := range tc.headers {
				req.Header.Set(k, v)
			}

			var lastStatusCode int
			var lastBody []byte
			require.Eventually(t, func() bool {
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					return false
				}
				defer func() { _ = resp.Body.Close() }()
				lastBody, err = io.ReadAll(resp.Body)
				if err != nil {
					return false
				}
				lastStatusCode = resp.StatusCode
				return resp.StatusCode == http.StatusOK
			}, eventuallyTimeout, eventuallyInterval,
				"last status code: %d, last body: %s", lastStatusCode, lastBody)

			body := createdReg.ReplaceAllString(string(lastBody), `"created":123`)
			require.JSONEq(t, cmp.Or(tc.expResponse, expOpenAIResponse), body,
				"final response must come from the expected backend's schema binding")
		})
	}
}
