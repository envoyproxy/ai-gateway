// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func TestGatewayMutator_Default(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	fakeKube := fake2.NewClientset()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true, Level: zapcore.DebugLevel})))
	g := newGatewayMutator(
		fakeClient, fakeKube, ctrl.Log, "docker.io/envoyproxy/ai-gateway-extproc:latest",
		"info", "envoy-gateway-system", "/tmp/extproc.sock",
	)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "test-namespace"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "envoy"}},
		},
	}
	err := fakeClient.Create(t.Context(), &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gateway", Namespace: "test-namespace"},
		Spec:       aigv1a1.AIGatewayRouteSpec{},
	})
	require.NoError(t, err)
	err = g.Default(t.Context(), pod)
	require.NoError(t, err)
}

func TestGatewayMutator_mutatePod(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	fakeKube := fake2.NewClientset()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true, Level: zapcore.DebugLevel})))
	g := newGatewayMutator(
		fakeClient, fakeKube, ctrl.Log, "docker.io/envoyproxy/ai-gateway-extproc:latest",
		"info", "envoy-gateway-system", "/tmp/extproc.sock",
	)

	const gwName, gwNamespace = "test-gateway", "test-namespace"
	err := fakeClient.Create(t.Context(), &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{Name: gwName, Namespace: gwNamespace},
		Spec: aigv1a1.AIGatewayRouteSpec{
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName{
				{
					LocalPolicyTargetReference: gwapiv1a2.LocalPolicyTargetReference{
						Name: gwName, Kind: "Gateway", Group: "gateway.networking.k8s.io",
					},
				},
			},
			Rules: []aigv1a1.AIGatewayRouteRule{
				{BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{{Name: "apple"}}},
			},
			APISchema:    aigv1a1.VersionedAPISchema{Name: aigv1a1.APISchemaOpenAI, Version: ptr.To("v1")},
			FilterConfig: &aigv1a1.AIGatewayFilterConfig{},
		},
	})
	require.NoError(t, err)

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "test-namespace"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "envoy"}},
		},
	}
	err = g.mutatePod(t.Context(), pod, gwName, gwNamespace)
	require.NoError(t, err)
}
