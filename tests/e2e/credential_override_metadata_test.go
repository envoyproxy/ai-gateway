// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/tests/internal/e2elib"
	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// Test_CredentialOverrideFromDynamicMetadata checks the whole fromDynamicMetadata chain on a real
// cluster: ext_authz returns a per-tenant API key as dynamic metadata, and the upstream must see
// that key instead of the static one.
//
// The BackendSecurityPolicy starts without a credentialOverride and gets it applied mid-test.
// That is deliberate: forwarding_namespaces on the ext_proc filter only changes when Envoy
// Gateway re-translates, and EG doesn't watch BackendSecurityPolicies, so the policy update
// reaching the running gateway is the part most worth testing.
func Test_CredentialOverrideFromDynamicMetadata(t *testing.T) {
	const manifest = "testdata/credential_override_metadata.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), manifest))
	t.Cleanup(func() {
		_ = e2elib.KubectlDeleteManifest(context.Background(), manifest)
	})

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=envoy-ai-gateway-credential-override"
	e2elib.RequireWaitForGatewayPodReady(t, egSelector)
	e2elib.RequireWaitForPodReady(t, "default", "app=ext-auth-server-credential-override")
	e2elib.RequireWaitForPodReady(t, "default", "app=envoy-ai-gateway-credential-override-testupstream")

	fwd := e2elib.RequireNewHTTPPortForwarder(t, e2elib.EnvoyGatewayNamespace, egSelector, e2elib.EnvoyGatewayDefaultServicePort)
	defer fwd.Kill()

	// doRequest asserts, via the testupstream, which Authorization header the upstream received.
	// The testupstream answers 400 on a mismatch, so the callers poll until the expectation holds.
	doRequest := func(t *testing.T, tenantID, wantAuthorization string) (int, string, error) {
		t.Helper()
		req, err := http.NewRequest(http.MethodPost, fwd.Address()+"/v1/chat/completions", strings.NewReader(
			`{"messages":[{"role":"user","content":"Say this is a test"}],"model":"credential-override-model"}`))
		require.NoError(t, err)
		req.Header.Set(testupstreamlib.ResponseBodyHeaderKey, base64.StdEncoding.EncodeToString([]byte(
			`{"choices":[{"message":{"content":"This is a test."}}]}`)))
		req.Header.Set(testupstreamlib.ExpectedHeadersKey, base64.StdEncoding.EncodeToString([]byte(
			"Authorization:"+wantAuthorization)))
		if tenantID != "" {
			req.Header.Set("x-tenant-id", tenantID)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return 0, "", err
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return 0, "", err
		}
		return resp.StatusCode, string(body), nil
	}

	requireEventuallyAuthorized := func(t *testing.T, tenantID, wantAuthorization string) {
		t.Helper()
		require.Eventually(t, func() bool {
			status, body, err := doRequest(t, tenantID, wantAuthorization)
			if err != nil {
				t.Logf("request error: %v", err)
				return false
			}
			if status != http.StatusOK {
				t.Logf("status %d: %s", status, body)
				return false
			}
			return true
		}, 3*time.Minute, 2*time.Second)
	}

	// Without the override, the static credential is used even though ext_authz already emits
	// the metadata for tenant-a.
	requireEventuallyAuthorized(t, "tenant-a", "Bearer static-configured-key")

	// Adding the override to the policy must reach the running gateway on its own: the
	// controller updates the HTTPRoute annotation, EG re-translates, and the ext_proc filter
	// starts forwarding the ext_authz namespace.
	const bspManifest = "testdata/credential_override_metadata_bsp.yaml"
	require.NoError(t, e2elib.KubectlApplyManifest(t.Context(), bspManifest))
	requireEventuallyAuthorized(t, "tenant-a", "Bearer tenant-a-per-request-key")

	// A tenant the auth server has no entry for falls back to the static credential.
	requireEventuallyAuthorized(t, "tenant-unknown", "Bearer static-configured-key")

	// So does a request with no tenant header at all.
	requireEventuallyAuthorized(t, "", "Bearer static-configured-key")
}
