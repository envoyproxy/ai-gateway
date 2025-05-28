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
	t.Skip("TODO")
	const manifest = "../../examples/inference_extension/inference_extension.yaml"
	require.NoError(t, kubectlApplyManifest(t.Context(), manifest))

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=inference-extension-example"
	requireWaitForGatewayPodReady(t, egSelector)

	fwd := requireNewHTTPPortForwarder(t, egNamespace, egSelector, egDefaultPort)
	defer fwd.kill()
	require.Eventually(t, func() bool {
		requestBody := fmt.Sprintf(`{"messages":[{"role":"user","content":"Say this is a test"}],"model":"mistral:latest"}`)
		req, err := http.NewRequest(http.MethodPut, fwd.address()+"/v1/chat/completions", strings.NewReader(requestBody))
		require.NoError(t, err)
		req.Header.Set("x-target-inference-extension", "yes")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		t.Logf("response: status=%d; headers=%v; body=%s", resp.StatusCode, resp.Header, string(body))
		if resp.StatusCode != http.StatusOK {
			return false
		}
		return true
	}, 5*time.Minute, 5*time.Second)
}
