// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	testsinternal "github.com/envoyproxy/ai-gateway/tests/internal"
)

func TestAzureManagedIdentityIntegration(t *testing.T) {
	c, _, _ := testsinternal.NewEnvTest(t)
	ctx := context.Background()

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: "azure-mi-test",
		},
	}
	require.NoError(t, c.Create(ctx, namespace))

	t.Run("user-assigned managed identity", func(t *testing.T) {
		testUserAssignedManagedIdentity(ctx, t, c, namespace.Name)
	})

	t.Run("system-assigned managed identity", func(t *testing.T) {
		testSystemAssignedManagedIdentity(ctx, t, c, namespace.Name)
	})
}

func testUserAssignedManagedIdentity(ctx context.Context, t *testing.T, c client.Client, namespace string) {
	// Create AIServiceBackend.
	asb := &aigv1a1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-mi-user-asb",
			Namespace: namespace,
		},
		Spec: aigv1a1.AIServiceBackendSpec{
			APISchema: aigv1a1.VersionedAPISchema{
				Name:    "AzureOpenAI",
				Version: ptr.To("2025-01-01-preview"),
			},
			BackendRef: gwapiv1.BackendObjectReference{
				Name: "test-backend",
				Port: ptr.To(gwapiv1.PortNumber(80)),
			},
		},
	}
	require.NoError(t, c.Create(ctx, asb))

	// Create BackendSecurityPolicy with user-assigned managed identity.
	bsp := &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-mi-user-bsp",
			Namespace: namespace,
		},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{
				{
					Group: "aigateway.envoyproxy.io",
					Kind:  "AIServiceBackend",
					Name:  gwapiv1.ObjectName(asb.Name),
				},
			},
			Type: aigv1a1.BackendSecurityPolicyTypeAzureCredentials,
			AzureCredentials: &aigv1a1.BackendSecurityPolicyAzureCredentials{
				ClientID:           "test-user-assigned-mi-client-id",
				TenantID:           "test-tenant-id",
				UseManagedIdentity: ptr.To(true),
			},
		},
	}
	require.NoError(t, c.Create(ctx, bsp))

	// Wait for status to be updated.
	require.Eventually(t, func() bool {
		var updatedBSP aigv1a1.BackendSecurityPolicy
		if err := c.Get(ctx, types.NamespacedName{Name: bsp.Name, Namespace: bsp.Namespace}, &updatedBSP); err != nil {
			return false
		}
		return len(updatedBSP.Status.Conditions) > 0
	}, 30*time.Second, 100*time.Millisecond, "BackendSecurityPolicy status should be updated")

	// Verify the BackendSecurityPolicy exists and has the correct configuration.
	var updatedBSP aigv1a1.BackendSecurityPolicy
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: bsp.Name, Namespace: bsp.Namespace}, &updatedBSP))
	require.Equal(t, "test-user-assigned-mi-client-id", updatedBSP.Spec.AzureCredentials.ClientID)
	require.Equal(t, "test-tenant-id", updatedBSP.Spec.AzureCredentials.TenantID)
	require.NotNil(t, updatedBSP.Spec.AzureCredentials.UseManagedIdentity)
	require.True(t, *updatedBSP.Spec.AzureCredentials.UseManagedIdentity)
	require.Nil(t, updatedBSP.Spec.AzureCredentials.ClientSecretRef)
	require.Nil(t, updatedBSP.Spec.AzureCredentials.OIDCExchangeToken)
}

func testSystemAssignedManagedIdentity(ctx context.Context, t *testing.T, c client.Client, namespace string) {
	// Create AIServiceBackend.
	asb := &aigv1a1.AIServiceBackend{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-mi-system-asb",
			Namespace: namespace,
		},
		Spec: aigv1a1.AIServiceBackendSpec{
			APISchema: aigv1a1.VersionedAPISchema{
				Name:    "AzureOpenAI",
				Version: ptr.To("2025-01-01-preview"),
			},
			BackendRef: gwapiv1.BackendObjectReference{
				Name: "test-backend",
				Port: ptr.To(gwapiv1.PortNumber(80)),
			},
		},
	}
	require.NoError(t, c.Create(ctx, asb))

	// Create BackendSecurityPolicy with system-assigned managed identity (no clientID).
	bsp := &aigv1a1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "azure-mi-system-bsp",
			Namespace: namespace,
		},
		Spec: aigv1a1.BackendSecurityPolicySpec{
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{
				{
					Group: "aigateway.envoyproxy.io",
					Kind:  "AIServiceBackend",
					Name:  gwapiv1.ObjectName(asb.Name),
				},
			},
			Type: aigv1a1.BackendSecurityPolicyTypeAzureCredentials,
			AzureCredentials: &aigv1a1.BackendSecurityPolicyAzureCredentials{
				// No ClientID for system-assigned managed identity.
				TenantID:           "test-tenant-id",
				UseManagedIdentity: ptr.To(true),
			},
		},
	}
	require.NoError(t, c.Create(ctx, bsp))

	// Wait for status to be updated.
	require.Eventually(t, func() bool {
		var updatedBSP aigv1a1.BackendSecurityPolicy
		if err := c.Get(ctx, types.NamespacedName{Name: bsp.Name, Namespace: bsp.Namespace}, &updatedBSP); err != nil {
			return false
		}
		return len(updatedBSP.Status.Conditions) > 0
	}, 30*time.Second, 100*time.Millisecond, "BackendSecurityPolicy status should be updated")

	// Verify the BackendSecurityPolicy exists and has the correct configuration.
	var updatedBSP aigv1a1.BackendSecurityPolicy
	require.NoError(t, c.Get(ctx, types.NamespacedName{Name: bsp.Name, Namespace: bsp.Namespace}, &updatedBSP))
	require.Empty(t, updatedBSP.Spec.AzureCredentials.ClientID) // No clientID for system-assigned.
	require.Equal(t, "test-tenant-id", updatedBSP.Spec.AzureCredentials.TenantID)
	require.NotNil(t, updatedBSP.Spec.AzureCredentials.UseManagedIdentity)
	require.True(t, *updatedBSP.Spec.AzureCredentials.UseManagedIdentity)
	require.Nil(t, updatedBSP.Spec.AzureCredentials.ClientSecretRef)
	require.Nil(t, updatedBSP.Spec.AzureCredentials.OIDCExchangeToken)
}
