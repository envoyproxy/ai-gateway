// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

	// Create a AIGatewayRoute.
	aiGatewayRoute := &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myroute",
			Namespace: "default",
		},
		Spec: aigv1a1.AIGatewayRouteSpec{
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget"}},
				{LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{Name: "mytarget2"}},
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
