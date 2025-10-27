// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tests

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/envoyproxy/ai-gateway/api/v1alpha1"
	fakeclientset "github.com/envoyproxy/ai-gateway/pkg/client/clientset/versioned/fake"
)

func TestAIGatewayRouteClient(t *testing.T) {
	ctx := context.Background()
	client := fakeclientset.NewSimpleClientset()

	t.Run("Create AIGatewayRoute", func(t *testing.T) {
		route := &v1alpha1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-route",
				Namespace: "default",
			},
			Spec: v1alpha1.AIGatewayRouteSpec{
				Rules: []v1alpha1.AIGatewayRouteRule{
					{
						BackendRefs: []v1alpha1.AIGatewayRouteRuleBackendRef{
							{
								Name: "test-backend",
							},
						},
					},
				},
			},
		}

		created, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").Create(ctx, route, metav1.CreateOptions{})
		require.NoError(t, err)
		assert.Equal(t, "test-route", created.Name)
		assert.Equal(t, "default", created.Namespace)
	})

	t.Run("Get AIGatewayRoute", func(t *testing.T) {
		route := &v1alpha1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-get-route",
				Namespace: "default",
			},
		}

		_, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").Create(ctx, route, metav1.CreateOptions{})
		require.NoError(t, err)

		fetched, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").Get(ctx, "test-get-route", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "test-get-route", fetched.Name)
	})

	t.Run("List AIGatewayRoutes", func(t *testing.T) {
		// Create multiple routes
		for i := 0; i < 3; i++ {
			route := &v1alpha1.AIGatewayRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-list-route-" + string(rune('a'+i)),
					Namespace: "default",
				},
			}
			_, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").Create(ctx, route, metav1.CreateOptions{})
			require.NoError(t, err)
		}

		list, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").List(ctx, metav1.ListOptions{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(list.Items), 3)
	})

	t.Run("Update AIGatewayRoute", func(t *testing.T) {
		route := &v1alpha1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-update-route",
				Namespace: "default",
			},
		}

		created, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").Create(ctx, route, metav1.CreateOptions{})
		require.NoError(t, err)

		created.Spec.Rules = []v1alpha1.AIGatewayRouteRule{
			{
				BackendRefs: []v1alpha1.AIGatewayRouteRuleBackendRef{
					{
						Name: "updated-backend",
					},
				},
			},
		}

		updated, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").Update(ctx, created, metav1.UpdateOptions{})
		require.NoError(t, err)
		assert.Equal(t, "updated-backend", updated.Spec.Rules[0].BackendRefs[0].Name)
	})

	t.Run("Delete AIGatewayRoute", func(t *testing.T) {
		route := &v1alpha1.AIGatewayRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-delete-route",
				Namespace: "default",
			},
		}

		_, err := client.AigatewayV1alpha1().AIGatewayRoutes("default").Create(ctx, route, metav1.CreateOptions{})
		require.NoError(t, err)

		err = client.AigatewayV1alpha1().AIGatewayRoutes("default").Delete(ctx, "test-delete-route", metav1.DeleteOptions{})
		require.NoError(t, err)

		_, err = client.AigatewayV1alpha1().AIGatewayRoutes("default").Get(ctx, "test-delete-route", metav1.GetOptions{})
		assert.Error(t, err)
	})
}

func TestAIServiceBackendClient(t *testing.T) {
	ctx := context.Background()
	client := fakeclientset.NewSimpleClientset()

	t.Run("Create AIServiceBackend", func(t *testing.T) {
		backend := &v1alpha1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-backend",
				Namespace: "default",
			},
			Spec: v1alpha1.AIServiceBackendSpec{
				APISchema: v1alpha1.VersionedAPISchema{
					Name: v1alpha1.APISchemaOpenAI,
				},
				BackendRef: gwapiv1.BackendObjectReference{
					Name:  "test-service",
					Group: ptrTo(gwapiv1.Group("gateway.envoyproxy.io")),
					Kind:  ptrTo(gwapiv1.Kind("Backend")),
				},
			},
		}

		created, err := client.AigatewayV1alpha1().AIServiceBackends("default").Create(ctx, backend, metav1.CreateOptions{})
		require.NoError(t, err)
		assert.Equal(t, "test-backend", created.Name)
		assert.Equal(t, v1alpha1.APISchemaOpenAI, created.Spec.APISchema.Name)
	})

	t.Run("Get AIServiceBackend", func(t *testing.T) {
		backend := &v1alpha1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-get-backend",
				Namespace: "default",
			},
			Spec: v1alpha1.AIServiceBackendSpec{
				APISchema: v1alpha1.VersionedAPISchema{
					Name: v1alpha1.APISchemaOpenAI,
				},
				BackendRef: gwapiv1.BackendObjectReference{
					Name:  "test-service",
					Group: ptrTo(gwapiv1.Group("gateway.envoyproxy.io")),
					Kind:  ptrTo(gwapiv1.Kind("Backend")),
				},
			},
		}

		_, err := client.AigatewayV1alpha1().AIServiceBackends("default").Create(ctx, backend, metav1.CreateOptions{})
		require.NoError(t, err)

		fetched, err := client.AigatewayV1alpha1().AIServiceBackends("default").Get(ctx, "test-get-backend", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "test-get-backend", fetched.Name)
	})

	t.Run("List AIServiceBackends", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			backend := &v1alpha1.AIServiceBackend{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-list-backend-" + string(rune('a'+i)),
					Namespace: "default",
				},
				Spec: v1alpha1.AIServiceBackendSpec{
					APISchema: v1alpha1.VersionedAPISchema{
						Name: v1alpha1.APISchemaOpenAI,
					},
					BackendRef: gwapiv1.BackendObjectReference{
						Name:  "test-service",
						Group: ptrTo(gwapiv1.Group("gateway.envoyproxy.io")),
						Kind:  ptrTo(gwapiv1.Kind("Backend")),
					},
				},
			}
			_, err := client.AigatewayV1alpha1().AIServiceBackends("default").Create(ctx, backend, metav1.CreateOptions{})
			require.NoError(t, err)
		}

		list, err := client.AigatewayV1alpha1().AIServiceBackends("default").List(ctx, metav1.ListOptions{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(list.Items), 2)
	})

	t.Run("Delete AIServiceBackend", func(t *testing.T) {
		backend := &v1alpha1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-delete-backend",
				Namespace: "default",
			},
			Spec: v1alpha1.AIServiceBackendSpec{
				APISchema: v1alpha1.VersionedAPISchema{
					Name: v1alpha1.APISchemaOpenAI,
				},
				BackendRef: gwapiv1.BackendObjectReference{
					Name:  "test-service",
					Group: ptrTo(gwapiv1.Group("gateway.envoyproxy.io")),
					Kind:  ptrTo(gwapiv1.Kind("Backend")),
				},
			},
		}

		_, err := client.AigatewayV1alpha1().AIServiceBackends("default").Create(ctx, backend, metav1.CreateOptions{})
		require.NoError(t, err)

		err = client.AigatewayV1alpha1().AIServiceBackends("default").Delete(ctx, "test-delete-backend", metav1.DeleteOptions{})
		require.NoError(t, err)
	})
}

func TestBackendSecurityPolicyClient(t *testing.T) {
	ctx := context.Background()
	client := fakeclientset.NewSimpleClientset()

	t.Run("Create BackendSecurityPolicy", func(t *testing.T) {
		policy := &v1alpha1.BackendSecurityPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-policy",
				Namespace: "default",
			},
			Spec: v1alpha1.BackendSecurityPolicySpec{
				Type: v1alpha1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &v1alpha1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{
						Name: "api-key-secret",
					},
				},
			},
		}

		created, err := client.AigatewayV1alpha1().BackendSecurityPolicies("default").Create(ctx, policy, metav1.CreateOptions{})
		require.NoError(t, err)
		assert.Equal(t, "test-policy", created.Name)
		assert.Equal(t, v1alpha1.BackendSecurityPolicyTypeAPIKey, created.Spec.Type)
	})

	t.Run("Get BackendSecurityPolicy", func(t *testing.T) {
		policy := &v1alpha1.BackendSecurityPolicy{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-get-policy",
				Namespace: "default",
			},
			Spec: v1alpha1.BackendSecurityPolicySpec{
				Type: v1alpha1.BackendSecurityPolicyTypeAPIKey,
				APIKey: &v1alpha1.BackendSecurityPolicyAPIKey{
					SecretRef: &gwapiv1.SecretObjectReference{
						Name: "api-key-secret",
					},
				},
			},
		}

		_, err := client.AigatewayV1alpha1().BackendSecurityPolicies("default").Create(ctx, policy, metav1.CreateOptions{})
		require.NoError(t, err)

		fetched, err := client.AigatewayV1alpha1().BackendSecurityPolicies("default").Get(ctx, "test-get-policy", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "test-get-policy", fetched.Name)
	})
}

func TestMCPRouteClient(t *testing.T) {
	ctx := context.Background()
	client := fakeclientset.NewSimpleClientset()

	t.Run("Create MCPRoute", func(t *testing.T) {
		route := &v1alpha1.MCPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-mcp-route",
				Namespace: "default",
			},
			Spec: v1alpha1.MCPRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{
						Name: "test-gateway",
					},
				},
				BackendRefs: []v1alpha1.MCPRouteBackendRef{
					{
						BackendObjectReference: gwapiv1.BackendObjectReference{
							Name: "mcp-server",
						},
					},
				},
			},
		}

		created, err := client.AigatewayV1alpha1().MCPRoutes("default").Create(ctx, route, metav1.CreateOptions{})
		require.NoError(t, err)
		assert.Equal(t, "test-mcp-route", created.Name)
		assert.Len(t, created.Spec.BackendRefs, 1)
	})

	t.Run("Get MCPRoute", func(t *testing.T) {
		route := &v1alpha1.MCPRoute{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-get-mcp-route",
				Namespace: "default",
			},
			Spec: v1alpha1.MCPRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{
					{
						Name: "test-gateway",
					},
				},
				BackendRefs: []v1alpha1.MCPRouteBackendRef{
					{
						BackendObjectReference: gwapiv1.BackendObjectReference{
							Name: "mcp-server",
						},
					},
				},
			},
		}

		_, err := client.AigatewayV1alpha1().MCPRoutes("default").Create(ctx, route, metav1.CreateOptions{})
		require.NoError(t, err)

		fetched, err := client.AigatewayV1alpha1().MCPRoutes("default").Get(ctx, "test-get-mcp-route", metav1.GetOptions{})
		require.NoError(t, err)
		assert.Equal(t, "test-get-mcp-route", fetched.Name)
	})

	t.Run("List MCPRoutes", func(t *testing.T) {
		for i := 0; i < 2; i++ {
			route := &v1alpha1.MCPRoute{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-list-mcp-route-" + string(rune('a'+i)),
					Namespace: "default",
				},
				Spec: v1alpha1.MCPRouteSpec{
					ParentRefs: []gwapiv1.ParentReference{
						{
							Name: "test-gateway",
						},
					},
					BackendRefs: []v1alpha1.MCPRouteBackendRef{
						{
							BackendObjectReference: gwapiv1.BackendObjectReference{
								Name: "mcp-server",
							},
						},
					},
				},
			}
			_, err := client.AigatewayV1alpha1().MCPRoutes("default").Create(ctx, route, metav1.CreateOptions{})
			require.NoError(t, err)
		}

		list, err := client.AigatewayV1alpha1().MCPRoutes("default").List(ctx, metav1.ListOptions{})
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(list.Items), 2)
	})
}
