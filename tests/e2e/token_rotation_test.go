//go:build test_e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/envoyproxy/ai-gateway/tests/internal/testupstreamlib"
)

// kubeClient creates a Kubernetes clientset using the current context
func kubeClient() (*kubernetes.Clientset, error) {
	loadingRules := clientcmd.NewDefaultClientConfigLoadingRules()
	configOverrides := &clientcmd.ConfigOverrides{}
	kubeConfig := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(loadingRules, configOverrides)

	config, err := kubeConfig.ClientConfig()
	if err != nil {
		return nil, err
	}

	return kubernetes.NewForConfig(config)
}

// secret retrieves a Kubernetes secret by name and namespace
func secret(ctx context.Context, name, namespace string) (*corev1.Secret, error) {
	clientset, err := kubeClient()
	if err != nil {
		return nil, err
	}
	return clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
}

// podLogs retrieves logs from a pod matching the given selector
func podLogs(ctx context.Context, namespace, labelSelector string) (string, error) {
	clientset, err := kubeClient()
	if err != nil {
		return "", fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// First list all pods in the namespace to help with debugging
	allPods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to list pods in namespace %s: %w", namespace, err)
	}

	// Log all available pods and their labels
	var availablePods []string
	for _, pod := range allPods.Items {
		podInfo := fmt.Sprintf("pod=%s labels=%v", pod.Name, pod.Labels)
		availablePods = append(availablePods, podInfo)
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return "", fmt.Errorf("failed to list pods with selector %s: %w", labelSelector, err)
	}

	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pods found matching selector %s in namespace %s. Available pods: %v",
			labelSelector, namespace, availablePods)
	}

	req := clientset.CoreV1().Pods(namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{})
	podLogs, err := req.Stream(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get logs for pod %s: %w", pods.Items[0].Name, err)
	}
	defer podLogs.Close()

	buf := new(bytes.Buffer)
	_, err = io.Copy(buf, podLogs)
	if err != nil {
		return "", fmt.Errorf("failed to read logs from pod %s: %w", pods.Items[0].Name, err)
	}

	return buf.String(), nil
}

// Test_Examples_TokenRotation tests the token rotation functionality using test AWS credentials
func Test_Examples_TokenRotation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Apply all components including the test credentials
	require.NoError(t, kubectlApplyManifest(ctx, "./init/token_rotation/manifest.yaml"))

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=token-rotation-test"
	requireWaitForPodReadyWithTimeout(t, egNamespace, egSelector, 1*time.Minute)

	// Set up test response from upstream
	const fakeResponseBody = `{"choices":[{"message":{"content":"This is a test response."}}]}`

	// Test the gateway with a request to ensure basic functionality works
	require.Eventually(t, func() bool {
		fwd := requireNewHTTPPortForwarder(t, egNamespace, egSelector, egDefaultPort)
		defer fwd.kill()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		client := openai.NewClient(
			option.WithBaseURL(fwd.address()+"/v1/"),
			option.WithHeader(
				testupstreamlib.ResponseBodyHeaderKey,
				base64.StdEncoding.EncodeToString([]byte(fakeResponseBody)),
			),
			option.WithHeader(
				testupstreamlib.ExpectedPathHeaderKey,
				base64.StdEncoding.EncodeToString([]byte("/v1/chat/completions")),
			),
			option.WithHeader(
				testupstreamlib.ExpectedHostKey,
				"testupstream.default.svc.cluster.local",
			),
			option.WithHeader(
				testupstreamlib.ExpectedTestUpstreamIDKey,
				"primary",
			),
		)

		chatCompletion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Say this is a test"),
			}),
			Model: openai.F("test-model"),
		})
		if err != nil {
			t.Logf("error: %v", err)
			return false
		}

		// Verify we got the expected response
		if len(chatCompletion.Choices) == 0 {
			return false
		}
		return chatCompletion.Choices[0].Message.Content == "This is a test response."
	}, 30*time.Second, 3*time.Second)

	// Wait for rotation attempt with timeout
	t.Log("Waiting for rotation attempt...")
	const controllerNamespace = "envoy-ai-gateway-system"
	require.Eventually(t, func() bool {
		// First verify the controller pod exists
		clientset, err := kubeClient()
		if err != nil {
			t.Logf("Failed to create kubernetes client: %v", err)
			return false
		}

		pods, err := clientset.CoreV1().Pods(controllerNamespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app.kubernetes.io/instance=ai-eg",
		})
		if err != nil {
			t.Logf("Failed to list pods in namespace %s: %v", controllerNamespace, err)
			return false
		}
		if len(pods.Items) == 0 {
			allPods, err := clientset.CoreV1().Pods(controllerNamespace).List(ctx, metav1.ListOptions{})
			if err != nil {
				t.Logf("Failed to list all pods in namespace %s: %v", controllerNamespace, err)
				return false
			}
			var availablePods []string
			for _, pod := range allPods.Items {
				availablePods = append(availablePods, fmt.Sprintf("pod=%s labels=%v", pod.Name, pod.Labels))
			}
			t.Logf("No controller pods found with selector 'app.kubernetes.io/instance=ai-eg' in namespace %s. Available pods: %v",
				controllerNamespace, availablePods)
			return false
		}

		// Get logs from controller
		logs, err := podLogs(ctx, controllerNamespace, "app.kubernetes.io/instance=ai-eg")
		if err != nil {
			t.Logf("Failed to get controller logs: %v", err)
			return false
		}

		return strings.Contains(logs, "failed to rotate credentials for secret default/test-rotation-secret")
	}, 90*time.Second, 5*time.Second, "Expected to find credential rotation failure in logs")

	// Verify the gateway still works even though rotation failed
	require.Eventually(t, func() bool {
		fwd := requireNewHTTPPortForwarder(t, egNamespace, egSelector, egDefaultPort)
		defer fwd.kill()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		client := openai.NewClient(
			option.WithBaseURL(fwd.address()+"/v1/"),
			option.WithHeader(
				testupstreamlib.ResponseBodyHeaderKey,
				base64.StdEncoding.EncodeToString([]byte(fakeResponseBody)),
			),
			option.WithHeader(
				testupstreamlib.ExpectedPathHeaderKey,
				base64.StdEncoding.EncodeToString([]byte("/v1/chat/completions")),
			),
			option.WithHeader(
				testupstreamlib.ExpectedHostKey,
				"testupstream.default.svc.cluster.local",
			),
			option.WithHeader(
				testupstreamlib.ExpectedTestUpstreamIDKey,
				"primary",
			),
		)

		chatCompletion, err := client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Messages: openai.F([]openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Say this is a test"),
			}),
			Model: openai.F("test-model"),
		})
		if err != nil {
			t.Logf("error: %v", err)
			return false
		}

		// Verify we got the expected response
		if len(chatCompletion.Choices) == 0 {
			return false
		}
		return chatCompletion.Choices[0].Message.Content == "This is a test response."
	}, 30*time.Second, 3*time.Second)
}
