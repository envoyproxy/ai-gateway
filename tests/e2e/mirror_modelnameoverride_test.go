// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
)

// TestMirrorModelNameOverride verifies that ExtProc runs on shadow (mirror) backend
// clusters so per-mirror ModelNameOverride takes effect on the cloned request.
//
// The mirror cluster gets the same upstream ExtProc filter chain as the primary leg,
// so extproc rewrites the model on the shadow leg. The shadow upstream sees
// "shadow-overridden" even though the client sent "client-original".
func TestMirrorModelNameOverride(t *testing.T) {
	const manifest = "testdata/mirror_modelnameoverride.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=mirror-modelnameoverride"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)
	// Wait for both upstream pods so the /last-received-body endpoint is reachable.
	e2elib.RequireWaitForPodReady(t, "default", "app=mirror-primary")
	e2elib.RequireWaitForPodReady(t, "default", "app=mirror-shadow")

	gwFwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
	defer gwFwd.Kill()

	// Port-forward to the shadow and primary services so we can hit /last-received-body.
	shadowFwd := e2elib.RequireNewHTTPPortForwarder(t, "default", "app=mirror-shadow", 80)
	defer shadowFwd.Kill()
	primaryFwd := e2elib.RequireNewHTTPPortForwarder(t, "default", "app=mirror-primary", 80)
	defer primaryFwd.Kill()

	// Drive a primary request with the original model name. The mirror filter on
	// the route clones the request to mirror-shadow; ExtProc on the shadow cluster
	// should rewrite the model to "shadow-overridden".
	requestBody := `{"messages":[{"role":"user","content":"Say this is a test"}],"model":"client-original"}`
	require.Eventually(t, func() bool {
		req, err := http.NewRequest(http.MethodPost, gwFwd.Address()+"/v1/chat/completions", strings.NewReader(requestBody))
		if err != nil {
			t.Logf("build req: %v", err)
			return false
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("post: %v", err)
			return false
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Logf("gateway returned %d, body: %s", resp.StatusCode, body)
			return false
		}
		return true
	}, 30*time.Second, 1*time.Second, "gateway did not return 200 for primary request")

	// Verify the primary upstream saw "client-original" (no override on the primary).
	require.Eventually(t, func() bool {
		body, err := fetchLastReceived(t.Context(), primaryFwd.Address())
		if err != nil {
			t.Logf("primary fetch: %v", err)
			return false
		}
		if !strings.Contains(body, `"model":"client-original"`) {
			t.Logf("primary body did not contain client-original: %s", body)
			return false
		}
		return true
	}, 15*time.Second, 500*time.Millisecond, "primary upstream did not record expected body")

	// Verify the shadow upstream saw the OVERRIDDEN model. This is the assertion
	// that fails if ExtProc does not run on the mirror cluster (the body would be
	// the original client payload).
	require.Eventually(t, func() bool {
		body, err := fetchLastReceived(t.Context(), shadowFwd.Address())
		if err != nil {
			t.Logf("shadow fetch: %v", err)
			return false
		}
		if !strings.Contains(body, `"model":"shadow-overridden"`) {
			t.Logf("shadow body did not contain shadow-overridden: %s", body)
			return false
		}
		if strings.Contains(body, `"model":"client-original"`) {
			t.Logf("shadow body still contains client-original (override did not fire): %s", body)
			return false
		}
		return true
	}, 30*time.Second, 1*time.Second, "shadow upstream did not observe the overridden model")
}

func fetchLastReceived(ctx context.Context, addr string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr+"/last-received-body", nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, body)
	}
	return string(body), nil
}
