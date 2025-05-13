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
	"strings"
	"testing"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	"sigs.k8s.io/yaml"

	"github.com/envoyproxy/ai-gateway/filterapi"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

// setupDefaultAIGatewayResourcesWithAvailableCredentials sets up the default AI Gateway resources with available
// credentials and returns the path to the resources file and the credentials context.
func setupDefaultAIGatewayResourcesWithAvailableCredentials(t *testing.T) (string, internaltesting.CredentialsContext) {
	credCtx := internaltesting.RequireNewCredentialsContext(t)
	// Set up the credential substitution.
	t.Setenv("OPENAI_API_KEY", credCtx.OpenAIAPIKey)
	aiGatewayResourcesPath := filepath.Join(t.TempDir(), "ai-gateway-resources.yaml")
	aiGatewayResources := strings.ReplaceAll(aiGatewayDefaultResources, "~/.aws/credentials", credCtx.AWSFilePath)
	err := os.WriteFile(aiGatewayResourcesPath, []byte(aiGatewayResources), 0o600)
	require.NoError(t, err)
	return aiGatewayResourcesPath, credCtx
}

func TestRun(t *testing.T) {
	resourcePath, cc := setupDefaultAIGatewayResourcesWithAvailableCredentials(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		require.NoError(t, run(ctx, cmdRun{Debug: true, Path: resourcePath}, os.Stdout, os.Stderr))
		close(done)
	}()
	defer func() {
		// Make sure the external processor is stopped regardless of the test result.
		cancel()
		<-done
	}()

	// This is the health checking to see the extproc is working as expected.
	require.Eventually(t, func() bool {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://localhost:1975/v1/chat/completions",
			strings.NewReader("{}"))
		req.Header.Set("Content-Type", "application/json")
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Logf("error: %v", err)
			return false
		}
		defer func() {
			require.NoError(t, resp.Body.Close())
		}()
		// We don't care about the content and just check the connection is successful.
		raw, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		body := string(raw)
		t.Logf("status=%d, body: %s", resp.StatusCode, body)
		// This ensures that the response is returned from the external processor where the body says about the
		// matching rule not found since we send an empty JSON.
		if resp.StatusCode != http.StatusNotFound || body != "no matching rule found" {
			return false
		}
		return true
	}, 120*time.Second, 1*time.Second)

	for _, tc := range []struct {
		testName, modelName string
		required            internaltesting.RequiredCredential
	}{
		{
			testName:  "openai",
			modelName: "gpt-4o-mini",
			required:  internaltesting.RequiredCredentialOpenAI,
		},
		{
			testName:  "aws",
			modelName: "us.meta.llama3-2-1b-instruct-v1:0",
			required:  internaltesting.RequiredCredentialAWS,
		},
	} {
		t.Run(tc.testName, func(t *testing.T) {
			client := openai.NewClient(option.WithBaseURL("http://localhost:1975" + "/v1/"))
			cc.MaybeSkip(t, tc.required)
			require.Eventually(t, func() bool {
				chatCompletion, err := client.Chat.Completions.New(t.Context(), openai.ChatCompletionNewParams{
					Messages: []openai.ChatCompletionMessageParamUnion{
						openai.UserMessage("Say this is a test"),
					},
					Model: tc.modelName,
				})
				if err != nil {
					t.Logf("error: %v", err)
					return false
				}
				nonEmptyCompletion := false
				for _, choice := range chatCompletion.Choices {
					t.Logf("choice: %s", choice.Message.Content)
					if choice.Message.Content != "" {
						nonEmptyCompletion = true
					}
				}
				return nonEmptyCompletion
			}, 30*time.Second, 2*time.Second)
		})
	}
}

func TestRunCmdContext_writeEnvoyResourcesAndRunExtProc(t *testing.T) {
	resourcePath, _ := setupDefaultAIGatewayResourcesWithAvailableCredentials(t)
	runCtx := &runCmdContext{
		stderrLogger:             slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{})),
		envoyGatewayResourcesOut: &bytes.Buffer{},
		tmpdir:                   t.TempDir(),
	}
	content, err := os.ReadFile(resourcePath)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	_, err = runCtx.writeEnvoyResourcesAndRunExtProc(ctx, string(content))
	require.NoError(t, err)
	time.Sleep(1 * time.Second)
	cancel()
	// Wait for the external processor to stop.
	time.Sleep(1 * time.Second)
}

func TestRunCmdContext_writeExtensionPolicy(t *testing.T) {
	// They will be used for substitutions.
	t.Setenv("FOO", "bar")
	tmpFilePath := filepath.Join(t.TempDir(), "some-temp")
	require.NoError(t, os.WriteFile(tmpFilePath, []byte("some-temp-content"), 0o600))

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
	fakeClientSet := fake2.NewClientset()
	runCtx := &runCmdContext{
		stderrLogger:             slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{})),
		envoyGatewayResourcesOut: &bytes.Buffer{},
		tmpdir:                   t.TempDir(),
		fakeClientSet:            fakeClientSet,
	}
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myextproc",
			Namespace: "foo-namespace",
		},
		Data: map[string]string{
			"extproc-config.yaml": `
metadataNamespace: io.envoy.ai_gateway
modelNameHeaderKey: x-ai-eg-model
backends:
- auth:
    apiKey:
      filename: /etc/backend_security_policy/rule0-backref0-envoy-ai-gateway-basic-openai-apikey/apiKey
  name: envoy-ai-gateway-basic-openai.default
  schema:
    name: OpenAI
- auth:
    aws:
      credentialFileName: /etc/backend_security_policy/rule1-backref0-envoy-ai-gateway-basic-aws-credentials/credentials
      region: us-east-1
  name: envoy-ai-gateway-basic-aws.default
  schema:
    name: AWSBedrock
- name: envoy-ai-gateway-basic-testupstream.default
  schema:
    name: OpenAI
rules:
- headers:
  - name: x-ai-eg-model
    value: gpt-4o-mini
- headers:
  - name: x-ai-eg-model
    value: us.meta.llama3-2-1b-instruct-v1:0
- headers:
  - name: x-ai-eg-model
    value: some-cool-self-hosted-model
schema:
  name: OpenAI
selectedRouteHeaderKey: x-ai-eg-selected-route
uuid: aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa
`,
		},
	}
	secrets := []*corev1.Secret{
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "envoy-ai-gateway-basic-openai-apikey",
				Namespace: "foo-namespace",
				Annotations: map[string]string{
					substitutionEnvAnnotationPrefix + "envSubstTarget":    "FOO",
					substitutionEnvAnnotationPrefix + "nonExistEnvTarget": "dog",
				},
			},
			Data: map[string][]byte{
				"apiKey":            []byte("my-api-key"),
				"envSubstTarget":    []byte("NO"),
				"nonExistEnvTarget": []byte("cat"),
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "envoy-ai-gateway-basic-aws-credentials",
				Namespace: "foo-namespace",
				Annotations: map[string]string{
					substitutionFileAnnotationPrefix + "fileSubstTarget":    tmpFilePath,
					substitutionFileAnnotationPrefix + "nonExistFileTarget": "non-exist",
				},
			},
			StringData: map[string]string{
				"credentials":        "my-aws-credentials",
				"fileSubstTarget":    "NO",
				"nonExistFileTarget": "dog",
			},
		},
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myextproc",
			Namespace: "foo-namespace",
		},
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
	}

	_, err := fakeClientSet.CoreV1().ConfigMaps("foo-namespace").Create(context.Background(), configMap, metav1.CreateOptions{})
	require.NoError(t, err)
	for _, secret := range secrets {
		_, err = fakeClientSet.CoreV1().Secrets("foo-namespace").Create(context.Background(), secret, metav1.CreateOptions{})
		require.NoError(t, err)
	}
	_, err = fakeClientSet.AppsV1().Deployments("foo-namespace").Create(context.Background(), deployment, metav1.CreateOptions{})
	require.NoError(t, err)

	wd, port, filterConfig, err := runCtx.writeExtensionPolicy(extP)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(runCtx.tmpdir, "envoy-ai-gateway-extproc-foo-namespace-myextproc"), wd)
	require.NotZero(t, port)
	require.NotEmpty(t, filterConfig)

	// Check the secrets are written to the working directory.
	// API key secret.
	_, err = os.Stat(filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-openai-apikey"))
	require.NoError(t, err)
	content, err := os.ReadFile(filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-openai-apikey/apiKey"))
	require.NoError(t, err)
	require.Equal(t, "my-api-key", string(content))
	content, err = os.ReadFile(filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-openai-apikey/envSubstTarget"))
	require.NoError(t, err)
	require.Equal(t, "bar", string(content))
	// Non-exist env target should be skipped.
	content, err = os.ReadFile(filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-openai-apikey/nonExistEnvTarget"))
	require.NoError(t, err)
	require.Equal(t, "cat", string(content))
	// AWS credentials secret.
	_, err = os.Stat(filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-aws-credentials"))
	require.NoError(t, err)
	content, err = os.ReadFile(filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-aws-credentials/credentials"))
	require.NoError(t, err)
	require.Equal(t, "my-aws-credentials", string(content))
	// Check the symlink from the secret to the file.
	content, err = os.ReadFile(filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-aws-credentials/fileSubstTarget"))
	require.NoError(t, err)
	require.Equal(t, "some-temp-content", string(content))
	// Check the symlink from the secret to the non-exist file should not be skipped.
	content, err = os.ReadFile(filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-aws-credentials/nonExistFileTarget"))
	require.NoError(t, err)
	require.Equal(t, "dog", string(content))

	// Check the file path in the filter config.
	require.Equal(t, filterConfig.Backends[0].Auth.APIKey.Filename,
		filepath.Join(wd, "foo-namespace-envoy-ai-gateway-basic-openai-apikey/apiKey"))
	require.Equal(t, filterConfig.Backends[1].Auth.AWSAuth.CredentialFileName,
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
      address: 127.0.0.1
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
	runCtx := &runCmdContext{stderrLogger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{}))}
	runCtx.mustStartExtProc(ctx, dir, mustGetAvailablePort(), fc)
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
