// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func TestGatewayMutator_mutatePod_PerGatewayEnvVars(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	fakeKube := fake2.NewClientset()
	// Initialize with some global env vars to test merging
	g := newTestGatewayMutator(fakeClient, fakeKube, "", "", "", "GLOBAL_VAR=global-value", "", false)

	const gwName, gwNamespace = "test-gateway", "test-namespace"
	err := fakeClient.Create(t.Context(), &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: gwName, Namespace: gwNamespace},
		Spec: aigv1a1.AIGatewayRouteSpec{
			ParentRefs: []gwapiv1a2.ParentReference{
				{
					Name:  gwName,
					Kind:  ptr.To(gwapiv1a2.Kind("Gateway")),
					Group: ptr.To(gwapiv1a2.Group("gateway.networking.k8s.io")),
				},
			},
			Rules: []aigv1a1.AIGatewayRouteRule{
				{BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "apple"}}},
			},
			FilterConfig: &aigv1a1.AIGatewayFilterConfig{
				ExternalProcessor: &aigv1a1.AIGatewayFilterConfigExternalProcessor{
					Env: []corev1.EnvVar{
						{Name: "PER_GATEWAY_VAR", Value: "per-gateway-value"},
						{Name: "GLOBAL_VAR", Value: "overridden-value"}, // Test override (though current logic appends, so last one might win depending on k8s behavior, but here we just check presence)
					},
				},
			},
		},
	})
	require.NoError(t, err)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "test-namespace"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "envoy"}},
		},
	}

	// Create the config secret
	_, err = g.kube.CoreV1().Secrets("test-namespace").Create(t.Context(),
		&corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: FilterConfigSecretPerGatewayName(
				gwName, gwNamespace,
			), Namespace: "test-namespace"},
		}, metav1.CreateOptions{})
	require.NoError(t, err)

	err = g.mutatePod(t.Context(), pod, gwName, gwNamespace)
	require.NoError(t, err)

	require.Len(t, pod.Spec.Containers, 2)
	extProcContainer := pod.Spec.Containers[1]
	require.Equal(t, "ai-gateway-extproc", extProcContainer.Name)

	// Check if both global and per-gateway env vars are present
	// Note: The current implementation appends per-gateway vars to global vars.
	// If K8s sees duplicate env var names, the last one takes precedence.
	// So we expect both to be in the list.
	expectedEnvVars := []corev1.EnvVar{
		{Name: "GLOBAL_VAR", Value: "global-value"},
		{Name: "PER_GATEWAY_VAR", Value: "per-gateway-value"},
		{Name: "GLOBAL_VAR", Value: "overridden-value"},
	}
	require.Equal(t, expectedEnvVars, extProcContainer.Env)
}
