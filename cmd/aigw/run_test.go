// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestRun_default(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		require.NoError(t, run(ctx, cmdRun{}, os.Stdout, os.Stderr))
		close(done)
	}()
	require.Eventually(t, func() bool {
		// Make request to 8888 and wait for the response.
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://localhost:8888/", nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("error: %v", err)
			return false
		}
		defer resp.Body.Close()
		// We don't care about the content and just check the connection is successful.
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		t.Logf("status=%d, body: %s", resp.StatusCode, string(body))
		return true
	}, 20*time.Second, 1*time.Second)
	cancel()
	<-done
}

func TestRunCmdContext_writeEnvoyResourcesAndRunExtProc(t *testing.T) {
	runCtx := &runCmdContext{
		stderrLogger:             slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{})),
		envoyGatewayResourcesOut: &bytes.Buffer{},
		tmpdir:                   t.TempDir(),
	}
	ctx, cancel := context.WithCancel(context.Background())
	err := runCtx.writeEnvoyResourcesAndRunExtProc(ctx, defaultYAML)
	require.NoError(t, err)
	time.Sleep(1 * time.Second)
	cancel()
	// Wait for the external processor to stop.
	time.Sleep(1 * time.Second)
}

func TestRunCmdContext_mustWriteExtensionPolicy(t *testing.T) {
	extP := &egv1a1.EnvoyExtensionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myextproc",
			Namespace: "foo-namespace",
		},
		Spec: egv1a1.EnvoyExtensionPolicySpec{
			ExtProc: []egv1a1.ExtProc{
				{
					BackendCluster: egv1a1.BackendCluster{
						BackendRefs: []egv1a1.BackendRef{
							{
								BackendObjectReference: gwapiv1.BackendObjectReference{
									Name:      "myextproc",
									Namespace: ptr.To[gwapiv1.Namespace]("foo-namespace"),
									Port:      ptr.To[gwapiv1.PortNumber](1063),
								},
							},
						},
					},
				},
			},
		},
	}
	runCtx := &runCmdContext{
		stderrLogger:             slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{})),
		envoyGatewayResourcesOut: &bytes.Buffer{},
		tmpdir:                   t.TempDir(),
		cm: map[string]*corev1.ConfigMap{
			"foo-namespace-myextproc": {
				Data: map[string]string{
					"extproc-config.yaml": `
metadataNamespace: io.envoy.ai_gateway
modelNameHeaderKey: x-ai-eg-model
rules:
- backends:
  - auth:
      apiKey:
        filename: /etc/backend_security_policy/rule0-backref0-envoy-ai-gateway-basic-openai-apikey/apiKey
    name: envoy-ai-gateway-basic-openai.default
    schema:
      name: OpenAI
    weight: 0
  headers:
  - name: x-ai-eg-model
    value: gpt-4o-mini
- backends:
  - auth:
      aws:
        credentialFileName: /etc/backend_security_policy/rule1-backref0-envoy-ai-gateway-basic-aws-credentials/credentials
        region: us-east-1
    name: envoy-ai-gateway-basic-aws.default
    schema:
      name: AWSBedrock
    weight: 0
  headers:
  - name: x-ai-eg-model
    value: us.meta.llama3-2-1b-instruct-v1:0
- backends:
  - name: envoy-ai-gateway-basic-testupstream.default
    schema:
      name: OpenAI
    weight: 0
  headers:
  - name: x-ai-eg-model
    value: some-cool-self-hosted-model
schema:
  name: OpenAI
selectedBackendHeaderKey: x-ai-eg-selected-backend
uuid: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa
`,
				},
			},
		},
		sm: map[string]*corev1.Secret{
			"foo-namespace-envoy-ai-gateway-basic-openai-apikey": {
				Data: map[string][]byte{
					"apiKey": []byte("my-api-key"),
				},
			},
			"foo-namespace-envoy-ai-gateway-basic-aws-credentials": {
				StringData: map[string]string{
					"credentials": "my-aws-credentials",
				},
			},
		},
		dm: map[string]*appsv1.Deployment{
			"foo-namespace-myextproc": {
				Spec: appsv1.DeploymentSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									VolumeMounts: []corev1.VolumeMount{
										{
											MountPath: "/etc/ai-gateway/extproc",
											Name:      "config",
										},
										{
											MountPath: "/etc/backend_security_policy/rule0-backref0-envoy-ai-gateway-basic-openai-apikey",
											Name:      "rule0-backref0-envoy-ai-gateway-basic-openai-apikey",
										},
										{
											MountPath: "/etc/backend_security_policy/rule1-backref0-envoy-ai-gateway-basic-aws-credentials",
											Name:      "rule1-backref0-envoy-ai-gateway-basic-aws-credentials",
										},
									},
								},
							},
							Volumes: []corev1.Volume{
								{
									Name: "config",
									VolumeSource: corev1.VolumeSource{
										ConfigMap: &corev1.ConfigMapVolumeSource{
											LocalObjectReference: corev1.LocalObjectReference{
												Name: "foo-namespace-myextproc",
											},
										},
									},
								},
								{
									Name: "rule0-backref0-envoy-ai-gateway-basic-openai-apikey",
									VolumeSource: corev1.VolumeSource{
										Secret: &corev1.SecretVolumeSource{
											SecretName: "envoy-ai-gateway-basic-openai-apikey",
										},
									},
								},
								{
									Name: "rule1-backref0-envoy-ai-gateway-basic-aws-credentials",
									VolumeSource: corev1.VolumeSource{
										Secret: &corev1.SecretVolumeSource{
											SecretName: "envoy-ai-gateway-basic-aws-credentials",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	wd, port, filterConfig := runCtx.mustWriteExtensionPolicy(extP)
	require.Equal(t, filepath.Join(runCtx.tmpdir, "envoy-ai-gateway-extproc-foo-namespace-myextproc"), wd)
	require.NotZero(t, port)
	require.NotEmpty(t, filterConfig)

	// Check the secrets are written to the working directory.
	// API key secret.
	_, err := os.Stat(filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-openai-apikey"))
	require.NoError(t, err)
	content, err := os.ReadFile(filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-openai-apikey/apiKey"))
	require.NoError(t, err)
	require.Equal(t, "my-api-key", string(content))
	// AWS credentials secret.
	_, err = os.Stat(filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-aws-credentials"))
	require.NoError(t, err)
	content, err = os.ReadFile(filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-aws-credentials/credentials"))
	require.NoError(t, err)
	require.Equal(t, "my-aws-credentials", string(content))

	// Check the file path in the filter config.
	require.Equal(t, filterConfig.Rules[0].Backends[0].Auth.APIKey.Filename,
		filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-openai-apikey/apiKey"))
	require.Equal(t, filterConfig.Rules[1].Backends[0].Auth.AWSAuth.CredentialFileName,
		filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-aws-credentials/credentials"))

	// Check the Backend and ExtensionPolicy resources are written to the output file.
	out := runCtx.envoyGatewayResourcesOut.(*bytes.Buffer).String()
	require.Contains(t, out, fmt.Sprintf(`
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: Backend
metadata:
  creationTimestamp: null
  name: myextproc
  namespace: foo-namespace
spec:
  endpoints:
  - ip:
      address: 0.0.0.0
      port: %d`, port))
	require.Contains(t, out, `apiVersion: gateway.envoyproxy.io/v1alpha1
kind: EnvoyExtensionPolicy
metadata:
  creationTimestamp: null
  name: myextproc
  namespace: foo-namespace
spec:
  extProc:
  - backendRefs:
    - group: gateway.envoyproxy.io
      kind: Backend
      name: myextproc
      namespace: foo-namespace`)
}

func Test_mustStartExtProc(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dir := t.TempDir() + "/aaaaaaaaaaaaaaaaaaaaa"
	require.NoError(t, os.MkdirAll(dir, 0o755))
	var fc filterapi.Config
	require.NoError(t, yaml.Unmarshal([]byte(filterapi.DefaultConfig), &fc))
	mustStartExtProc(ctx, dir, mustGetAvailablePort(), fc)
	time.Sleep(1 * time.Second)
	cancel()
	// Wait for the external processor to stop.
	time.Sleep(1 * time.Second)
}

func Test_mustGetAvailablePort(t *testing.T) {
	p := mustGetAvailablePort()
	require.Positive(t, p)
	l, err := net.Listen("tcp", ":"+strconv.Itoa(int(p)))
	require.NoError(t, err)
	require.NoError(t, l.Close())
}
