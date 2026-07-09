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

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// TestLuaFilterOrder verifies that an EnvoyExtensionPolicy Lua filter annotated with
// aigateway.envoyproxy.io/lua-filter-order: before-extproc runs AFTER the AI Gateway
// ext_proc on the response path.
func TestLuaFilterOrder(t *testing.T) {
	const manifest = "testdata/lua_filter_order.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=lua-filter-order"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)

	const fakeResponseBody = `{"choices":[{"message":{"content":"hello"}}]}`
	// expResponseBody is the body we expect after the Lua filter has mutated it.
	const expResponseBody = `{"choices":[{"message":{"content":"hello - MUTATED"}}]}`

	require.Eventually(t, func() bool {
		fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
		defer fwd.Kill()

		req, err := http.NewRequest(http.MethodPost, fwd.Address()+"/v1/chat/completions",
			strings.NewReader(`{"messages":[{"role":"user","content":"Say this is a test"}],"model":"test-model"}`))
		require.NoError(t, err)

		req.Header.Set(testupstreamlib.ResponseBodyHeaderKey, base64.StdEncoding.EncodeToString([]byte(fakeResponseBody)))
		req.Header.Set(testupstreamlib.ExpectedTestUpstreamIDKey, "lua-filter-order")

		// the test sends a request with a known fake response body and asserts that the Lua
		// script's " - MUTATED" suffix is present in the final response.
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("request error: %v", err)
			return false
		}
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Logf("error reading response body: %v", err)
			return false
		}

		if resp.StatusCode != http.StatusOK {
			t.Logf("unexpected status %d, body: %s", resp.StatusCode, body)
			return false
		}

		if string(body) != expResponseBody {
			t.Logf("unexpected response body:\n  got:  %s\n  want: %s", body, expResponseBody)
			return false
		}
		return true
	}, 30*time.Second, 1*time.Second, fmt.Sprintf("Lua post-extproc mutation not present in response; expected %q", expResponseBody))
}
