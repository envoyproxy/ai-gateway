// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
)

// TestGatewayController_awsBackendHostname covers the Backend-FQDN resolution used
// to sign AWS SigV4 over the real upstream host: FQDN resolution, the
// Backend/gateway.envoyproxy.io kind guard (a Service ref must not pick up a
// same-named Backend CR), no-FQDN endpoints, and a missing Backend.
func TestGatewayController_awsBackendHostname(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()
	c := NewGatewayController(fakeClient, kube, ctrl.Log, "envoy-gateway-system",
		"docker.io/envoyproxy/ai-gateway-extproc:latest", "info", false, nil, true)

	backendRef := func(name string) gwapiv1.BackendObjectReference {
		return gwapiv1.BackendObjectReference{
			Group: ptr.To(gwapiv1.Group("gateway.envoyproxy.io")),
			Kind:  ptr.To(gwapiv1.Kind("Backend")),
			Name:  gwapiv1.ObjectName(name),
		}
	}

	const vpce = "vpce-abc.bedrock-runtime.us-east-1.vpce.amazonaws.com"
	require.NoError(t, fakeClient.Create(t.Context(), &egv1a1.Backend{
		ObjectMeta: metav1.ObjectMeta{Name: "bedrock-vpce", Namespace: "ns"},
		Spec: egv1a1.BackendSpec{Endpoints: []egv1a1.BackendEndpoint{
			{FQDN: &egv1a1.FQDNEndpoint{Hostname: vpce, Port: 443}},
		}},
	}))
	// A Backend with no FQDN endpoint (e.g. IP/Unix) has no signable host.
	require.NoError(t, fakeClient.Create(t.Context(), &egv1a1.Backend{
		ObjectMeta: metav1.ObjectMeta{Name: "no-fqdn", Namespace: "ns"},
		Spec:       egv1a1.BackendSpec{Endpoints: []egv1a1.BackendEndpoint{}},
	}))

	t.Run("resolves FQDN", func(t *testing.T) {
		require.Equal(t, vpce, c.awsBackendHostname(t.Context(), "ns", backendRef("bedrock-vpce")))
	})
	t.Run("no FQDN endpoint -> empty", func(t *testing.T) {
		require.Equal(t, "", c.awsBackendHostname(t.Context(), "ns", backendRef("no-fqdn")))
	})
	t.Run("missing Backend -> empty", func(t *testing.T) {
		require.Equal(t, "", c.awsBackendHostname(t.Context(), "ns", backendRef("missing")))
	})
	t.Run("non-Backend (Service) ref -> empty even if a same-named Backend exists", func(t *testing.T) {
		// Default BackendObjectReference kind is Service; must NOT resolve to the
		// same-named egv1a1.Backend created above.
		svcRef := gwapiv1.BackendObjectReference{Name: gwapiv1.ObjectName("bedrock-vpce")}
		require.Equal(t, "", c.awsBackendHostname(t.Context(), "ns", svcRef))
	})
	t.Run("ref namespace overrides the passed namespace", func(t *testing.T) {
		ref := backendRef("bedrock-vpce")
		ref.Namespace = ptr.To(gwapiv1.Namespace("ns"))
		require.Equal(t, vpce, c.awsBackendHostname(t.Context(), "other", ref))
	})
}

// TestGatewayController_reconcileFilterConfigSecret_AWSSigningHostname is the
// end-to-end wiring proof (credential-free): a reconcile over an AWS-backed route
// whose AIServiceBackend references an Envoy Gateway Backend with a vpce FQDN must
// emit a filter config whose AWSAuth.Hostname is that FQDN. Combined with the
// backendauth handler unit test (which proves SigV4 is signed over AWSAuth.Hostname),
// this covers the whole controller -> filterapi -> signer chain.
func TestGatewayController_reconcileFilterConfigSecret_AWSSigningHostname(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	kube := fake2.NewClientset()
	c := NewGatewayController(fakeClient, kube, ctrl.Log, "envoy-gateway-system",
		"docker.io/envoyproxy/ai-gateway-extproc:latest", "info", false, nil, true)

	const (
		gwNamespace = "ns"
		vpce        = "vpce-0123456789abcdef0-1a2b3c4d.bedrock-runtime.us-east-1.vpce.amazonaws.com"
	)

	// The Envoy Gateway Backend whose FQDN Envoy host-rewrites onto the wire.
	require.NoError(t, fakeClient.Create(t.Context(), &egv1a1.Backend{
		ObjectMeta: metav1.ObjectMeta{Name: "bedrock-be", Namespace: gwNamespace},
		Spec: egv1a1.BackendSpec{Endpoints: []egv1a1.BackendEndpoint{
			{FQDN: &egv1a1.FQDNEndpoint{Hostname: vpce, Port: 443}},
		}},
	}))
	// The AIServiceBackend referencing that Backend by kind=Backend/group=gateway.envoyproxy.io.
	require.NoError(t, fakeClient.Create(t.Context(), &aigv1b1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-be", Namespace: gwNamespace},
		Spec: aigv1b1.AIServiceBackendSpec{
			BackendRef: gwapiv1.BackendObjectReference{
				Group: ptr.To(gwapiv1.Group("gateway.envoyproxy.io")),
				Kind:  ptr.To(gwapiv1.Kind("Backend")),
				Name:  "bedrock-be",
			},
		},
	}))
	// An AWS BSP with only a region uses the default credential chain, so no secret
	// is needed -- bspToFilterAPIBackendAuth returns AWSAuth directly, credential-free.
	require.NoError(t, fakeClient.Create(t.Context(), &aigv1b1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-bsp", Namespace: gwNamespace},
		Spec: aigv1b1.BackendSecurityPolicySpec{
			Type:           aigv1b1.BackendSecurityPolicyTypeAWSCredentials,
			AWSCredentials: &aigv1b1.BackendSecurityPolicyAWSCredentials{Region: "us-east-1"},
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{
				{Group: "aigateway.envoyproxy.io", Kind: "AIServiceBackend", Name: "aws-be"},
			},
		},
	}))

	routes := []aigv1b1.AIGatewayRoute{{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-route", Namespace: gwNamespace},
		Spec: aigv1b1.AIGatewayRouteSpec{
			Rules: []aigv1b1.AIGatewayRouteRule{{
				BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{{Name: "aws-be"}},
			}},
		},
	}}

	const someNamespace = "some-namespace"
	effective, err := c.reconcileFilterConfigSecret(t.Context(), "gw", gwNamespace, someNamespace, routes, nil, "foouuid", nil)
	require.NoError(t, err)
	require.True(t, effective)

	fc := requireFilterConfigFromBundle(t, kube, someNamespace, "gw", gwNamespace)
	require.Len(t, fc.Backends, 1)
	require.NotNil(t, fc.Backends[0].Auth)
	require.NotNil(t, fc.Backends[0].Auth.AWSAuth)
	require.Equal(t, vpce, fc.Backends[0].Auth.AWSAuth.Hostname,
		"the emitted AWS signing host must be the referenced Backend's FQDN")
	require.Equal(t, "us-east-1", fc.Backends[0].Auth.AWSAuth.Region)
}
