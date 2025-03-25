// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_e2e

package e2e

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_Examples_InferenceExtension(t *testing.T) {
	t.Skip("skipping test for now")
	const manifest = "../../examples/token_ratelimit/inference_extension.yaml"
	require.NoError(t, kubectlApplyManifest(t.Context(), manifest))

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-token-ratelimit"
	requireWaitForPodReady(t, egNamespace, egSelector)

	require.Eventually(t, func() bool {
		fwd := requireNewHTTPPortForwarder(t, egNamespace, egSelector, egDefaultPort)
		defer fwd.kill()

		requestBody := fmt.Sprintf(`{"messages":[{"role":"user","content":"Say this is a test"}],"model":"mistral:latest"}`)
		req, err := http.NewRequest(http.MethodPost, fwd.address()+"/v1/chat/completions", strings.NewReader(requestBody))
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		t.Logf("response: status=%d; %s", resp.StatusCode, string(body))
		if resp.StatusCode != http.StatusOK {
			return false
		}
		return true
	}, 5*time.Minute, 5*time.Second)
}
