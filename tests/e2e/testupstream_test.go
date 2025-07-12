// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_e2e

package e2e

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// TestWithTestUpstream tests the end-to-end functionality of the AI Gateway with the testupstream server.
func TestWithTestUpstream(t *testing.T) {
	const manifest = "testdata/testupstream.yaml"
	require.NoError(t, kubectlApplyManifest(t.Context(), manifest))
	// Wait for the testupstream servers to be ready.
	require.NoError(t, kubectlWaitForDeploymentReady("default", "testupstream"))
	require.NoError(t, kubectlWaitForDeploymentReady("default", "testupstream-canary"))

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=testupstream"
	requireWaitForGatewayPodReady(t, egSelector)

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
				expResponseBody: `No matching route found. It is likely that the model specified your request is not configured in the Gateway.`,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				require.Eventually(t, func() bool {
					fwd := requireNewHTTPPortForwarder(t, egNamespace, egSelector, egDefaultServicePort)
					defer fwd.kill()

					req, err := http.NewRequest(http.MethodPost, fwd.address()+"/v1/chat/completions", strings.NewReader(fmt.Sprintf(
						`{"messages":[{"role":"user","content":"Say this is a test"}],"model":"%s"}`,
						tc.modelName)))
					require.NoError(t, err)
					req.Header.Set(testupstreamlib.ResponseBodyHeaderKey, base64.StdEncoding.EncodeToString([]byte(tc.fakeResponseBody)))
					req.Header.Set(testupstreamlib.ExpectedPathHeaderKey, base64.StdEncoding.EncodeToString([]byte(tc.expPath)))
					req.Header.Set(testupstreamlib.ExpectedHostKey, tc.expHost)
					req.Header.Set(testupstreamlib.ExpectedTestUpstreamIDKey, tc.expTestUpstreamID)

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

	t.Run("load-balancing-weights", func(t *testing.T) {
		reqCountsPerTestUpstreamID := make(map[string]int)
		const numRequests = 1000
		fwd := requireNewHTTPPortForwarder(t, egNamespace, egSelector, egDefaultServicePort)
		defer fwd.kill()

		// Send multiple requests with a load-balancing-model to trigger load balancing with weights.
		for i := 0; i < numRequests; i++ {
			req, err := http.NewRequest(http.MethodPost, fwd.address()+"/v1/chat/completions",
				strings.NewReader(`{"messages":[{"role":"user","content":"Test load balancing"}],"model":"load-balancing-model"}`))
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			_, err = io.ReadAll(resp.Body)
			require.NoError(t, err)
			_ = resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)

			// Check which backend handled the request.
			testUpstreamID := resp.Header.Get(testupstreamlib.TestUpstreamIDResponseHeaderKey)
			if testUpstreamID != "primary" && testUpstreamID != "canary" {
				t.Fatalf("Unexpected test upstream ID: %s. Expected 'primary' or 'canary'", testUpstreamID)
			}
			reqCountsPerTestUpstreamID[testUpstreamID]++
		}

		require.Lenf(t, reqCountsPerTestUpstreamID, 2,
			"Expected requests to be distributed between 2 backends: %v", reqCountsPerTestUpstreamID)

		// Check for reasonable distribution with tolerance for statistical variation.
		for backend, hits := range reqCountsPerTestUpstreamID {
			t.Logf("Backend %s received %d/%d requests", backend, hits, numRequests)
			require.Greater(t, hits, 0, "Backend %s should receive some requests", backend)

			// Expect reasonable distribution (allow for some deviation).
			expectedPerBackend := numRequests / 2
			allowedDeviation := float64(expectedPerBackend) * 0.01 // 1% deviation tolerance.
			require.InDelta(t, expectedPerBackend, hits, allowedDeviation,
				"Backend %s request count should be within acceptable deviation range", backend)
		}
	})
}
