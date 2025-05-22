// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	admissionv1 "k8s.io/api/admission/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	fake2 "k8s.io/client-go/kubernetes/fake"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func TestGatewayMutator_Handle(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	fakeKube := fake2.NewClientset()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true, Level: zapcore.DebugLevel})))
	g := newGatewayMutator(
		fakeClient, fakeKube, ctrl.Log, "docker.io/envoyproxy/ai-gateway-extproc:latest",
		"info", "envoy-gateway-system", "/tmp/extproc.sock",
	)

	t.Run("non attached pod", func(t *testing.T) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "test-namespace",
			},
		}
		raw, err := json.Marshal(pod)
		require.NoError(t, err)

		res := g.Handle(t.Context(), admission.Request{
			AdmissionRequest: admissionv1.AdmissionRequest{Object: runtime.RawExtension{Raw: raw}},
		})
		require.True(t, res.Allowed)
		require.Empty(t, res.Patch)
	})
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
			APISchema: aigv1a1.VersionedAPISchema{Name: aigv1a1.APISchemaOpenAI, Version: "v1"},
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
