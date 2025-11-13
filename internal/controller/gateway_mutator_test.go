// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap/zapcore"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestGatewayMutator_Default(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	fakeKube := fake2.NewClientset()
	g := newTestGatewayMutator(fakeClient, fakeKube, "", "", "", "", "", false)
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
		name                           string
		metricsRequestHeaderAttributes string
		spanRequestHeaderAttributes    string
		endpointPrefixes               string
		extProcExtraEnvVars            string
		extProcImagePullSecrets        string
		extprocTest                    func(t *testing.T, container corev1.Container)
		podTest                        func(t *testing.T, pod corev1.Pod)
		needMCP                        bool
	}{
		{
			name: "basic extproc container",
			extprocTest: func(t *testing.T, container corev1.Container) {
				require.Empty(t, container.Env)
			},
			podTest: func(t *testing.T, pod corev1.Pod) {
				require.Empty(t, pod.Spec.ImagePullSecrets)
			},
		},
		{
			name:    "basic extproc container with MCPRoute",
			needMCP: true,
			extprocTest: func(t *testing.T, container corev1.Container) {
				var foundMCPAddr, foundMCPSeed bool
				for i, arg := range container.Args {
					switch arg {
					case "-mcpAddr":
						foundMCPAddr = true
						require.Equal(t, ":"+strconv.Itoa(internalapi.MCPProxyPort), container.Args[i+1])
					case "-mcpSessionEncryptionSeed":
						foundMCPSeed = true
						require.Equal(t, "seed", container.Args[i+1])
					}
				}
				require.True(t, foundMCPAddr)
				require.True(t, foundMCPSeed)
			},
		},
		{
			name:             "with endpoint prefixes",
			endpointPrefixes: "openai:/v1,cohere:/cohere/v2,anthropic:/anthropic/v1",
			extprocTest: func(t *testing.T, container corev1.Container) {
				require.Contains(t, container.Args, "-endpointPrefixes")
				require.Contains(t, container.Args, "openai:/v1,cohere:/cohere/v2,anthropic:/anthropic/v1")
			},
			podTest: func(t *testing.T, pod corev1.Pod) {
				require.Empty(t, pod.Spec.ImagePullSecrets)
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
			podTest: func(t *testing.T, pod corev1.Pod) {
				require.Empty(t, pod.Spec.ImagePullSecrets)
			},
		},
		{
			name:                           "with metrics request header labels",
			metricsRequestHeaderAttributes: "x-team-id:team.id,x-user-id:user.id",
			extprocTest: func(t *testing.T, container corev1.Container) {
				require.Empty(t, container.Env)
				require.Contains(t, container.Args, "-metricsRequestHeaderAttributes")
				require.Contains(t, container.Args, "x-team-id:team.id,x-user-id:user.id")
			},
			podTest: func(t *testing.T, pod corev1.Pod) {
				require.Empty(t, pod.Spec.ImagePullSecrets)
			},
		},
		{
			name:                           "with both metrics and env vars",
			metricsRequestHeaderAttributes: "x-team-id:team.id",
			extProcExtraEnvVars:            "OTEL_SERVICE_NAME=custom-service",
			extprocTest: func(t *testing.T, container corev1.Container) {
				require.Equal(t, []corev1.EnvVar{
					{Name: "OTEL_SERVICE_NAME", Value: "custom-service"},
				}, container.Env)
				require.Contains(t, container.Args, "-metricsRequestHeaderAttributes")
				require.Contains(t, container.Args, "x-team-id:team.id")
			},
			podTest: func(t *testing.T, pod corev1.Pod) {
				require.Empty(t, pod.Spec.ImagePullSecrets)
			},
		},
		{
			name:                        "with tracing request header attributes",
			spanRequestHeaderAttributes: "x-session-id:session.id,x-user-id:user.id",
			extprocTest: func(t *testing.T, container corev1.Container) {
				require.Empty(t, container.Env)
				require.Contains(t, container.Args, "-spanRequestHeaderAttributes")
				require.Contains(t, container.Args, "x-session-id:session.id,x-user-id:user.id")
			},
			podTest: func(t *testing.T, pod corev1.Pod) {
				require.Empty(t, pod.Spec.ImagePullSecrets)
			},
		},
		{
			name:                           "with metrics, tracing, and env vars",
			metricsRequestHeaderAttributes: "x-user-id:user.id",
			spanRequestHeaderAttributes:    "x-session-id:session.id",
			extProcExtraEnvVars:            "OTEL_SERVICE_NAME=test-service",
			extprocTest: func(t *testing.T, container corev1.Container) {
				require.Equal(t, []corev1.EnvVar{
					{Name: "OTEL_SERVICE_NAME", Value: "test-service"},
				}, container.Env)
				require.Contains(t, container.Args, "-metricsRequestHeaderAttributes")
				require.Contains(t, container.Args, "x-user-id:user.id")
				require.Contains(t, container.Args, "-spanRequestHeaderAttributes")
				require.Contains(t, container.Args, "x-session-id:session.id")
			},
			podTest: func(t *testing.T, pod corev1.Pod) {
				require.Empty(t, pod.Spec.ImagePullSecrets)
			},
		},
		{
			name:                    "with image pull secrets",
			extProcImagePullSecrets: "my-registry-secret;backup-secret",
			extprocTest: func(t *testing.T, container corev1.Container) {
				require.Empty(t, container.Env)
			},
			podTest: func(t *testing.T, pod corev1.Pod) {
				expectedSecrets := []corev1.LocalObjectReference{
					{Name: "my-registry-secret"},
					{Name: "backup-secret"},
				}
				require.Equal(t, expectedSecrets, pod.Spec.ImagePullSecrets)
			},
		},
		{
			name:                    "with image pull secrets and env vars",
			extProcExtraEnvVars:     "OTEL_SERVICE_NAME=test-service",
			extProcImagePullSecrets: "my-registry-secret",
			extprocTest: func(t *testing.T, container corev1.Container) {
				require.Equal(t, []corev1.EnvVar{
					{Name: "OTEL_SERVICE_NAME", Value: "test-service"},
				}, container.Env)
			},
			podTest: func(t *testing.T, pod corev1.Pod) {
				expectedSecrets := []corev1.LocalObjectReference{
					{Name: "my-registry-secret"},
				}
				require.Equal(t, expectedSecrets, pod.Spec.ImagePullSecrets)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, sidecar := range []bool{true, false} {
				t.Run(fmt.Sprintf("sidecar=%v", sidecar), func(t *testing.T) {
					fakeClient := requireNewFakeClientWithIndexes(t)
					fakeKube := fake2.NewClientset()
					g := newTestGatewayMutator(fakeClient, fakeKube, tt.metricsRequestHeaderAttributes, tt.spanRequestHeaderAttributes, tt.endpointPrefixes, tt.extProcExtraEnvVars, tt.extProcImagePullSecrets, sidecar)

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
							FilterConfig: &aigv1a1.AIGatewayFilterConfig{},
						},
					})
					require.NoError(t, err)

					if tt.needMCP {
						err = fakeClient.Create(t.Context(), &aigv1a1.MCPRoute{
							ObjectMeta: metav1.ObjectMeta{Name: "test-mcp", Namespace: gwNamespace},
							Spec: aigv1a1.MCPRouteSpec{
								ParentRefs: []gwapiv1a2.ParentReference{
									{
										Name:  gwName,
										Kind:  ptr.To(gwapiv1a2.Kind("Gateway")),
										Group: ptr.To(gwapiv1a2.Group("gateway.networking.k8s.io")),
									},
								},
							},
						})
						require.NoError(t, err)
					}

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

					var extProcContainer corev1.Container
					if sidecar {
						require.Len(t, pod.Spec.Containers, 1)
						require.Len(t, pod.Spec.InitContainers, 1)
						extProcContainer = pod.Spec.InitContainers[0]
						require.NotNil(t, extProcContainer.RestartPolicy)
						require.Equal(t, corev1.ContainerRestartPolicyAlways, *extProcContainer.RestartPolicy)
					} else {
						require.Len(t, pod.Spec.Containers, 2)
						extProcContainer = pod.Spec.Containers[1]
					}

					require.Equal(t, "ai-gateway-extproc", extProcContainer.Name)
					tt.extprocTest(t, extProcContainer)
					if tt.podTest != nil {
						tt.podTest(t, *pod)
					}
				})
			}
		})
	}
}

func newTestGatewayMutator(fakeClient client.Client, fakeKube *fake2.Clientset, metricsRequestHeaderAttributes, spanRequestHeaderAttributes, endpointPrefixes, extProcExtraEnvVars, extProcImagePullSecrets string, sidecar bool) *gatewayMutator {
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true, Level: zapcore.DebugLevel})))
	return newGatewayMutator(
		fakeClient, fakeKube, ctrl.Log, "docker.io/envoyproxy/ai-gateway-extproc:latest", corev1.PullIfNotPresent,
		"info", "/tmp/extproc.sock", metricsRequestHeaderAttributes, spanRequestHeaderAttributes, "/v1", endpointPrefixes, extProcExtraEnvVars, extProcImagePullSecrets, 512*1024*1024,
		sidecar, "seed",
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

func TestParseImagePullSecrets(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		want      []corev1.LocalObjectReference
		wantError string
	}{
		{
			name:  "empty string",
			input: "",
			want:  nil,
		},
		{
			name:  "single secret",
			input: "my-registry-secret",
			want:  []corev1.LocalObjectReference{{Name: "my-registry-secret"}},
		},
		{
			name:  "multiple secrets",
			input: "my-registry-secret;backup-secret;third-secret",
			want: []corev1.LocalObjectReference{
				{Name: "my-registry-secret"},
				{Name: "backup-secret"},
				{Name: "third-secret"},
			},
		},
		{
			name:  "secrets with spaces",
			input: " my-registry-secret ; backup-secret ",
			want: []corev1.LocalObjectReference{
				{Name: "my-registry-secret"},
				{Name: "backup-secret"},
			},
		},
		{
			name:  "trailing semicolon",
			input: "my-registry-secret;backup-secret;",
			want: []corev1.LocalObjectReference{
				{Name: "my-registry-secret"},
				{Name: "backup-secret"},
			},
		},
		{
			name:  "only semicolons",
			input: ";;;",
			want:  nil,
		},
		{
			name:  "empty secret names",
			input: "my-secret;;backup-secret",
			want: []corev1.LocalObjectReference{
				{Name: "my-secret"},
				{Name: "backup-secret"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseImagePullSecrets(tt.input)
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
