// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/version"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func Test_aiGatewayRouteIndexFunc(t *testing.T) {
	c := requireNewFakeClientWithIndexes(t)

	// Create an AIGatewayRoute.
	aiGatewayRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myroute",
			Namespace: "default",
		},
		Spec: aigv1a1.AIGatewayRouteSpec{
			ParentRefs: []gwapiv1a2.ParentReference{
				{Name: "mytarget", Kind: ptr.To(gwapiv1a2.Kind("Gateway"))},
				{Name: "mytarget2", Kind: ptr.To(gwapiv1a2.Kind("HTTPRoute"))},
			},
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					Matches: []aigv1a1.AIGatewayRouteRuleMatch{},
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "backend1", Weight: ptr.To[int32](1)},
						{Name: "backend2", Weight: ptr.To[int32](1)},
					},
				},
			},
		},
	}
	require.NoError(t, c.Create(t.Context(), aiGatewayRoute))

	var aiGatewayRoutes aigv1a1.AIGatewayRouteList
	err := c.List(t.Context(), &aiGatewayRoutes,
		client.MatchingFields{k8sClientIndexBackendToReferencingAIGatewayRoute: "backend1.default"})
	require.NoError(t, err)
	require.Len(t, aiGatewayRoutes.Items, 1)
	require.Equal(t, aiGatewayRoute.Name, aiGatewayRoutes.Items[0].Name)

	err = c.List(t.Context(), &aiGatewayRoutes,
		client.MatchingFields{k8sClientIndexBackendToReferencingAIGatewayRoute: "backend2.default"})
	require.NoError(t, err)
	require.Len(t, aiGatewayRoutes.Items, 1)
	require.Equal(t, aiGatewayRoute.Name, aiGatewayRoutes.Items[0].Name)
}

func Test_backendSecurityPolicyIndexFunc(t *testing.T) {
	for _, bsp := range []struct {
		name                  string
		backendSecurityPolicy *aigv1a1.BackendSecurityPolicy
		expKey                string
	}{
		{
			name: "api key with namespace",
			backendSecurityPolicy: &aigv1a1.BackendSecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-1", Namespace: "ns"},
				Spec: aigv1a1.BackendSecurityPolicySpec{
					Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
					APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
						SecretRef: &gwapiv1.SecretObjectReference{
							Name:      "some-secret1",
							Namespace: ptr.To[gwapiv1.Namespace]("foo"),
						},
					},
				},
			},
			expKey: "some-secret1.foo",
		},
		{
			name: "api key without namespace",
			backendSecurityPolicy: &aigv1a1.BackendSecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-2", Namespace: "ns"},
				Spec: aigv1a1.BackendSecurityPolicySpec{
					Type: aigv1a1.BackendSecurityPolicyTypeAPIKey,
					APIKey: &aigv1a1.BackendSecurityPolicyAPIKey{
						SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret2"},
					},
				},
			},
			expKey: "some-secret2.ns",
		},
		{
			name: "aws credentials with namespace",
			backendSecurityPolicy: &aigv1a1.BackendSecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-3", Namespace: "ns"},
				Spec: aigv1a1.BackendSecurityPolicySpec{
					Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
					AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
						CredentialsFile: &aigv1a1.AWSCredentialsFile{
							SecretRef: &gwapiv1.SecretObjectReference{
								Name: "some-secret3", Namespace: ptr.To[gwapiv1.Namespace]("foo"),
							},
						},
					},
				},
			},
			expKey: "some-secret3.foo",
		},
		{
			name: "aws credentials without namespace",
			backendSecurityPolicy: &aigv1a1.BackendSecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-4", Namespace: "ns"},
				Spec: aigv1a1.BackendSecurityPolicySpec{
					Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
					AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
						CredentialsFile: &aigv1a1.AWSCredentialsFile{
							SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret4"},
						},
					},
				},
			},
			expKey: "some-secret4.ns",
		},
		{
			name: "Azure api key with namespace",
			backendSecurityPolicy: &aigv1a1.BackendSecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-5", Namespace: "ns"},
				Spec: aigv1a1.BackendSecurityPolicySpec{
					Type: aigv1a1.BackendSecurityPolicyTypeAzureAPIKey,
					AzureAPIKey: &aigv1a1.BackendSecurityPolicyAzureAPIKey{
						SecretRef: &gwapiv1.SecretObjectReference{
							Name:      "some-secret5",
							Namespace: ptr.To[gwapiv1.Namespace]("foo"),
						},
					},
				},
			},
			expKey: "some-secret5.foo",
		},
		{
			name: "Azure api key without namespace",
			backendSecurityPolicy: &aigv1a1.BackendSecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-6", Namespace: "ns"},
				Spec: aigv1a1.BackendSecurityPolicySpec{
					Type: aigv1a1.BackendSecurityPolicyTypeAzureAPIKey,
					AzureAPIKey: &aigv1a1.BackendSecurityPolicyAzureAPIKey{
						SecretRef: &gwapiv1.SecretObjectReference{Name: "some-secret6"},
					},
				},
			},
			expKey: "some-secret6.ns",
		},
		{
			name: "Azure credentials with namespace",
			backendSecurityPolicy: &aigv1a1.BackendSecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-7", Namespace: "ns"},
				Spec: aigv1a1.BackendSecurityPolicySpec{
					Type: aigv1a1.BackendSecurityPolicyTypeAzureCredentials,
					AzureCredentials: &aigv1a1.BackendSecurityPolicyAzureCredentials{
						ClientSecretRef: &gwapiv1.SecretObjectReference{
							Name:      "some-secret7",
							Namespace: ptr.To[gwapiv1.Namespace]("foo"),
						},
					},
				},
			},
			expKey: "some-secret7.foo",
		},
		{
			name: "Azure credentials without namespace",
			backendSecurityPolicy: &aigv1a1.BackendSecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-8", Namespace: "ns"},
				Spec: aigv1a1.BackendSecurityPolicySpec{
					Type: aigv1a1.BackendSecurityPolicyTypeAzureCredentials,
					AzureCredentials: &aigv1a1.BackendSecurityPolicyAzureCredentials{
						ClientSecretRef: &gwapiv1.SecretObjectReference{
							Name: "some-secret8",
						},
					},
				},
			},
			expKey: "some-secret8.ns",
		},
		{
			name: "AWS OIDC exchange token",
			backendSecurityPolicy: &aigv1a1.BackendSecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-9", Namespace: "foo"},
				Spec: aigv1a1.BackendSecurityPolicySpec{
					Type: aigv1a1.BackendSecurityPolicyTypeAWSCredentials,
					AWSCredentials: &aigv1a1.BackendSecurityPolicyAWSCredentials{
						OIDCExchangeToken: &aigv1a1.AWSOIDCExchangeToken{},
					},
				},
			},
			expKey: "some-backend-security-policy-9.foo",
		},
		{
			name: "Azure OIDC exchange token",
			backendSecurityPolicy: &aigv1a1.BackendSecurityPolicy{
				ObjectMeta: metav1.ObjectMeta{Name: "some-backend-security-policy-10", Namespace: "foo"},
				Spec: aigv1a1.BackendSecurityPolicySpec{
					Type: aigv1a1.BackendSecurityPolicyTypeAzureCredentials,
					AzureCredentials: &aigv1a1.BackendSecurityPolicyAzureCredentials{
						OIDCExchangeToken: &aigv1a1.AzureOIDCExchangeToken{},
					},
				},
			},
			expKey: "some-backend-security-policy-10.foo",
		},
	} {
		t.Run(bsp.name, func(t *testing.T) {
			c := fake.NewClientBuilder().
				WithScheme(Scheme).
				WithIndex(&aigv1a1.BackendSecurityPolicy{}, k8sClientIndexSecretToReferencingBackendSecurityPolicy, backendSecurityPolicyIndexFunc).
				Build()

			require.NoError(t, c.Create(t.Context(), bsp.backendSecurityPolicy))

			var backendSecurityPolicies aigv1a1.BackendSecurityPolicyList
			err := c.List(t.Context(), &backendSecurityPolicies,
				client.MatchingFields{k8sClientIndexSecretToReferencingBackendSecurityPolicy: bsp.expKey})
			require.NoError(t, err)

			require.Len(t, backendSecurityPolicies.Items, 1)
			require.Equal(t, bsp.backendSecurityPolicy.Name, backendSecurityPolicies.Items[0].Name)
			require.Equal(t, bsp.backendSecurityPolicy.Namespace, backendSecurityPolicies.Items[0].Namespace)
		})
	}
}

func Test_getSecretNameAndNamespace(t *testing.T) {
	secretRef := &gwapiv1.SecretObjectReference{
		Name:      "mysecret",
		Namespace: ptr.To[gwapiv1.Namespace]("default"),
	}
	require.Equal(t, "mysecret.default", getSecretNameAndNamespace(secretRef, "foo"))
	secretRef.Namespace = nil
	require.Equal(t, "mysecret.foo", getSecretNameAndNamespace(secretRef, "foo"))
}

func Test_handleFinalizer(t *testing.T) {
	tests := []struct {
		name               string
		hasFinalizer       bool
		hasDeletionTS      bool
		clientUpdateError  bool
		onDeletionFnError  bool
		expectedOnDelete   bool
		expectedFinalizers []string
		expectCallback     bool
	}{
		{
			name:               "add finalizer to new object",
			hasFinalizer:       false,
			hasDeletionTS:      false,
			expectedOnDelete:   false,
			expectedFinalizers: []string{aiGatewayControllerFinalizer},
		},
		{
			name:               "add finalizer to new object witt update error",
			hasFinalizer:       false,
			hasDeletionTS:      false,
			clientUpdateError:  true,
			expectedOnDelete:   false,
			expectedFinalizers: []string{aiGatewayControllerFinalizer},
		},
		{
			name:               "object already has finalizer",
			hasFinalizer:       true,
			hasDeletionTS:      false,
			expectedOnDelete:   false,
			expectedFinalizers: []string{aiGatewayControllerFinalizer},
		},
		{
			name:               "object being deleted, remove finalizer",
			hasFinalizer:       true,
			hasDeletionTS:      true,
			expectedOnDelete:   true,
			expectedFinalizers: []string{},
			expectCallback:     true,
		},
		{
			name:               "object being deleted, callback error",
			hasFinalizer:       true,
			hasDeletionTS:      true,
			onDeletionFnError:  true,
			expectedOnDelete:   true,
			expectedFinalizers: []string{},
			expectCallback:     true,
		},
		{
			name:               "object being deleted, client update error",
			hasFinalizer:       true,
			hasDeletionTS:      true,
			clientUpdateError:  true,
			expectedOnDelete:   true,
			expectedFinalizers: []string{},
			expectCallback:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			obj := &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{Name: "test-object", Namespace: "test-namespace"},
			}

			if tc.hasFinalizer {
				obj.Finalizers = []string{aiGatewayControllerFinalizer}
			}

			if tc.hasDeletionTS {
				obj.DeletionTimestamp = ptr.To(metav1.Now())
			}

			callbackExecuted := false
			var onDeletionFn func(context.Context, *aigv1a1.AIGatewayRoute) error
			if tc.expectCallback {
				onDeletionFn = func(context.Context, *aigv1a1.AIGatewayRoute) error {
					callbackExecuted = true
					if tc.onDeletionFnError {
						return fmt.Errorf("mock deletion error")
					}
					return nil
				}
			}
			onDelete := handleFinalizer(context.Background(),
				&mockClient{updateErr: tc.clientUpdateError}, logr.Discard(), obj, onDeletionFn)
			require.Equal(t, tc.expectedOnDelete, onDelete)
			require.Equal(t, tc.expectedFinalizers, obj.Finalizers)
			require.Equal(t, tc.expectCallback, callbackExecuted)
		})
	}
}

// mockClients implements client.Client with a custom Update method for testing purposes.
type mockClient struct {
	client.Client
	updateErr bool
}

// Updates implements the client.Client interface for the mock client.
func (m *mockClient) Update(context.Context, client.Object, ...client.UpdateOption) error {
	if m.updateErr {
		return fmt.Errorf("mock update error")
	}
	return nil
}

func Test_aiGatewayRouteToAttachedGatewayIndexFunc(t *testing.T) {
	tests := []struct {
		name            string
		route           *aigv1a1.AIGatewayRoute
		expectedIndexes []string
	}{
		{
			name: "parentRef cross-namespace reference",
			route: &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ai-route",
					Namespace: "envoy-ai-gateway-system",
				},
				Spec: aigv1a1.AIGatewayRouteSpec{
					ParentRefs: []gwapiv1a2.ParentReference{
						{
							Name:      "my-gateway",
							Namespace: ptr.To[gwapiv1a2.Namespace]("envoy-gateway-system"),
							Kind:      ptr.To(gwapiv1a2.Kind("Gateway")),
						},
					},
				},
			},
			expectedIndexes: []string{"my-gateway.envoy-gateway-system"},
		},
		{
			name: "parentRef same namespace as route",
			route: &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ai-route",
					Namespace: "production",
				},
				Spec: aigv1a1.AIGatewayRouteSpec{
					ParentRefs: []gwapiv1a2.ParentReference{
						{
							Name: "my-gateway",
							Kind: ptr.To(gwapiv1a2.Kind("Gateway")),
						},
					},
				},
			},
			expectedIndexes: []string{"my-gateway.production"},
		},
		{
			name: "multiple parentRefs with mixed namespaces",
			route: &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ai-route",
					Namespace: "app-namespace",
				},
				Spec: aigv1a1.AIGatewayRouteSpec{
					ParentRefs: []gwapiv1a2.ParentReference{
						{
							Name:      "gateway-1",
							Namespace: ptr.To[gwapiv1a2.Namespace]("infra-namespace"),
							Kind:      ptr.To(gwapiv1a2.Kind("Gateway")),
						},
						{
							Name: "gateway-2",
							Kind: ptr.To(gwapiv1a2.Kind("Gateway")),
						},
					},
				},
			},
			expectedIndexes: []string{
				"gateway-1.infra-namespace",
				"gateway-2.app-namespace",
			},
		},
		{
			name: "targetRefs always use route namespace",
			route: &aigv1a1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "ai-route",
					Namespace: "default",
				},
				Spec: aigv1a1.AIGatewayRouteSpec{
					ParentRefs: []gwapiv1a2.ParentReference{
						{
							Name:      "parent-gateway",
							Namespace: ptr.To[gwapiv1a2.Namespace]("system"),
							Kind:      ptr.To(gwapiv1a2.Kind("Gateway")),
						},
					},
				},
			},
			expectedIndexes: []string{"parent-gateway.system"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indexes := aiGatewayRouteToAttachedGatewayIndexFunc(tt.route)
			require.ElementsMatch(t, tt.expectedIndexes, indexes)
		})
	}
}

func Test_mcpRouteToAttachedGatewayIndexFunc(t *testing.T) {
	tests := []struct {
		name            string
		route           *aigv1a1.MCPRoute
		expectedIndexes []string
	}{
		{
			name: "parentRef cross-namespace reference",
			route: &aigv1a1.MCPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mcp-route",
					Namespace: "envoy-mcp-gateway-system",
				},
				Spec: aigv1a1.MCPRouteSpec{
					ParentRefs: []gwapiv1a2.ParentReference{
						{
							Name:      "my-gateway",
							Namespace: ptr.To[gwapiv1a2.Namespace]("envoy-gateway-system"),
							Kind:      ptr.To(gwapiv1a2.Kind("Gateway")),
						},
					},
				},
			},
			expectedIndexes: []string{"my-gateway.envoy-gateway-system"},
		},
		{
			name: "parentRef same namespace as route",
			route: &aigv1a1.MCPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mcp-route",
					Namespace: "production",
				},
				Spec: aigv1a1.MCPRouteSpec{
					ParentRefs: []gwapiv1a2.ParentReference{
						{
							Name: "my-gateway",
							Kind: ptr.To(gwapiv1a2.Kind("Gateway")),
						},
					},
				},
			},
			expectedIndexes: []string{"my-gateway.production"},
		},
		{
			name: "multiple parentRefs with mixed namespaces",
			route: &aigv1a1.MCPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "mcp-route",
					Namespace: "app-namespace",
				},
				Spec: aigv1a1.MCPRouteSpec{
					ParentRefs: []gwapiv1a2.ParentReference{
						{
							Name:      "gateway-1",
							Namespace: ptr.To[gwapiv1a2.Namespace]("infra-namespace"),
							Kind:      ptr.To(gwapiv1a2.Kind("Gateway")),
						},
						{
							Name: "gateway-2",
							Kind: ptr.To(gwapiv1a2.Kind("Gateway")),
						},
					},
				},
			},
			expectedIndexes: []string{
				"gateway-1.infra-namespace",
				"gateway-2.app-namespace",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			indexes := mcpRouteToAttachedGatewayIndexFunc(tt.route)
			require.ElementsMatch(t, tt.expectedIndexes, indexes)
		})
	}
}

func Test_isKubernetes133OrLater(t *testing.T) {
	require.False(t, isKubernetes133OrLater(&version.Info{}, logr.Discard()))
	require.False(t, isKubernetes133OrLater(&version.Info{Major: "invalid"}, logr.Discard()))
	require.False(t, isKubernetes133OrLater(&version.Info{Major: "1", Minor: "invalid"}, logr.Discard()))
	require.False(t, isKubernetes133OrLater(&version.Info{Major: "1", Minor: "1"}, logr.Discard()))
	require.False(t, isKubernetes133OrLater(&version.Info{Major: "1", Minor: "32"}, logr.Discard()))
	require.True(t, isKubernetes133OrLater(&version.Info{Major: "1", Minor: "33"}, logr.Discard()))
	require.True(t, isKubernetes133OrLater(&version.Info{Major: "1", Minor: "40"}, logr.Discard()))
}
