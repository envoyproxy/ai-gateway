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
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func TestGatewayMutator_Default(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	fakeKube := fake2.NewClientset()
	g := newTestGatewayMutator(fakeClient, fakeKube, "", "")
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
	tests := []struct {
		name                       string
		metricsRequestHeaderLabels string
		extProcExtraEnvVars        string
		extprocTest                func(t *testing.T, container corev1.Container)
	}{
		{
			name: "basic extproc container",
			extprocTest: func(t *testing.T, container corev1.Container) {
				require.Empty(t, container.Env)
			},
		},
		{
			name:                "with extra env vars",
			extProcExtraEnvVars: "OTEL_SERVICE_NAME=ai-gateway-extproc;OTEL_TRACES_EXPORTER=otlp",
			extprocTest: func(t *testing.T, container corev1.Container) {
				require.Equal(t, []corev1.EnvVar{
					{Name: "OTEL_SERVICE_NAME", Value: "ai-gateway-extproc"},
					{Name: "OTEL_TRACES_EXPORTER", Value: "otlp"},
				}, container.Env)
			},
		},
		{
			name:                       "with metrics request header labels",
			metricsRequestHeaderLabels: "x-team-id:team_id,x-user-id:user_id",
			extprocTest: func(t *testing.T, container corev1.Container) {
				require.Empty(t, container.Env)
				require.Contains(t, container.Args, "-metricsRequestHeaderLabels")
				require.Contains(t, container.Args, "x-team-id:team_id,x-user-id:user_id")
			},
		},
		{
			name:                       "with both metrics and env vars",
			metricsRequestHeaderLabels: "x-request-id:request_id",
			extProcExtraEnvVars:        "OTEL_SERVICE_NAME=custom-service",
			extprocTest: func(t *testing.T, container corev1.Container) {
				require.Equal(t, []corev1.EnvVar{
					{Name: "OTEL_SERVICE_NAME", Value: "custom-service"},
				}, container.Env)
				require.Contains(t, container.Args, "-metricsRequestHeaderLabels")
				require.Contains(t, container.Args, "x-request-id:request_id")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fakeClient := requireNewFakeClientWithIndexes(t)
			fakeKube := fake2.NewClientset()
			g := newTestGatewayMutator(fakeClient, fakeKube, tt.metricsRequestHeaderLabels, tt.extProcExtraEnvVars)

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

			// At this point, the config secret does not exist, so the pod should not be mutated.
			err = g.mutatePod(t.Context(), pod, gwName, gwNamespace)
			require.NoError(t, err)
			require.Len(t, pod.Spec.Containers, 1)

			// Create the config secret and mutate the pod again.
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

			// The second container is extproc - pass it to the test function.
			extprocContainer := pod.Spec.Containers[1]
			require.Equal(t, "ai-gateway-extproc", extprocContainer.Name)
			tt.extprocTest(t, extprocContainer)
		})
	}
}

func newTestGatewayMutator(fakeClient client.Client, fakeKube *fake2.Clientset, metricsRequestHeaderLabels, extProcExtraEnvVars string) *gatewayMutator {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true, Level: zapcore.DebugLevel})))
	return newGatewayMutator(
		fakeClient, fakeKube, ctrl.Log, "docker.io/envoyproxy/ai-gateway-extproc:latest", corev1.PullIfNotPresent,
		"info", "/tmp/extproc.sock", metricsRequestHeaderLabels, "/v1", extProcExtraEnvVars,
	)
}

func TestParseExtraEnvVars(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      []corev1.EnvVar
		wantError string
	}{
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "single env var",
			input: "OTEL_SERVICE_NAME=ai-gateway",
			want: []corev1.EnvVar{
				{Name: "OTEL_SERVICE_NAME", Value: "ai-gateway"},
			},
		},
		{
			name:  "multiple env vars",
			input: "OTEL_SERVICE_NAME=ai-gateway;OTEL_TRACES_EXPORTER=otlp",
			want: []corev1.EnvVar{
				{Name: "OTEL_SERVICE_NAME", Value: "ai-gateway"},
				{Name: "OTEL_TRACES_EXPORTER", Value: "otlp"},
			},
		},
		{
			name:  "env var with comma in value",
			input: "OTEL_RESOURCE_ATTRIBUTES=service.name=gateway,service.version=1.0",
			want: []corev1.EnvVar{
				{Name: "OTEL_RESOURCE_ATTRIBUTES", Value: "service.name=gateway,service.version=1.0"},
			},
		},
		{
			name:  "multiple env vars with commas",
			input: "OTEL_RESOURCE_ATTRIBUTES=service.name=gateway,service.version=1.0;OTEL_TRACES_EXPORTER=otlp",
			want: []corev1.EnvVar{
				{Name: "OTEL_RESOURCE_ATTRIBUTES", Value: "service.name=gateway,service.version=1.0"},
				{Name: "OTEL_TRACES_EXPORTER", Value: "otlp"},
			},
		},
		{
			name:  "env var with equals in value",
			input: "CONFIG=key1=value1",
			want: []corev1.EnvVar{
				{Name: "CONFIG", Value: "key1=value1"},
			},
		},
		{
			name:  "trailing semicolon",
			input: "OTEL_SERVICE_NAME=ai-gateway;",
			want: []corev1.EnvVar{
				{Name: "OTEL_SERVICE_NAME", Value: "ai-gateway"},
			},
		},
		{
			name:  "spaces around values",
			input: " OTEL_SERVICE_NAME = ai-gateway ; OTEL_TRACES_EXPORTER = otlp ",
			want: []corev1.EnvVar{
				{Name: "OTEL_SERVICE_NAME", Value: " ai-gateway"},
				{Name: "OTEL_TRACES_EXPORTER", Value: " otlp"},
			},
		},
		{
			name:  "only semicolons",
			input: ";;;",
			want:  nil,
		},
		{
			name:      "missing equals",
			input:     "OTEL_SERVICE_NAME",
			wantError: "invalid env var pair at position 1: \"OTEL_SERVICE_NAME\" (expected format: KEY=value)",
		},
		{
			name:      "empty key",
			input:     "=value",
			wantError: "empty env var name at position 1: \"=value\"",
		},
		{
			name:      "mixed valid and invalid",
			input:     "VALID=value;INVALID;ANOTHER=value",
			wantError: "invalid env var pair at position 2: \"INVALID\" (expected format: KEY=value)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseExtraEnvVars(tt.input)
			if tt.wantError != "" {
				require.Error(t, err)
				require.Equal(t, tt.wantError, err.Error())
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.want, got)
			}
		})
	}
}
