package e2e

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/sets"
	ctrl "sigs.k8s.io/controller-runtime"
)

const (
	egNamespace   = "envoy-gateway-system"
	egDefaultPort = 10080

	aigNamespace          = "ai-gateway-system"
	aigControllerSelector = "app.kubernetes.io/instance=aig"
)

var (
	envoyGatewayVersion      = "v1.1.1"
	envoyGatewayImage        = fmt.Sprintf("envoyproxy/gateway:%s", envoyGatewayVersion)
	envoyImage               = "envoyproxy/envoy:v1.31.0" // https://gateway.envoyproxy.io/news/releases/matrix/
	aiGatewayControllerImage = "ghcr.io/tetratelabs/ai-gateway-controller:latest"
	aiGatewayExtprocImage    = "ghcr.io/tetratelabs/ai-gateway-extproc:latest"
	testUpstreamImage        = "ghcr.io/tetratelabs/ai-gateway-testupstream:latest"
	rateLimiterImage         = "envoyproxy/ratelimit:master"
	redisImage               = "redis:alpine"
	loadTargetImages         = []string{
		aiGatewayControllerImage,
		aiGatewayExtprocImage,
		testUpstreamImage,
		envoyGatewayImage,
		envoyImage,
		rateLimiterImage,
		redisImage,
	}
)

var kc k8sConfig

func TestMain(m *testing.M) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(5*time.Minute))
	var cleanup func()

	if os.Getenv("USE_CURRENT_KUBECONFIG") != "" {
		kc = k8sConfig{
			config: ctrl.GetConfigOrDie(),
		}
		cleanup = func() {
			// TODO: add clean logic?
		}
	} else {
		prepareImages(ctx, loadTargetImages[0], loadTargetImages[1], loadTargetImages[2], loadTargetImages[3:]...)

		k8sConfig, k8sCleanup, err := initK3s(ctx, loadTargetImages...)
		if err != nil {
			fmt.Printf("k3s setup error: %v\n", err)
			cancel()
			os.Exit(1)
		}
		kc = k8sConfig
		cleanup = k8sCleanup

		if err := initTestupstream(ctx); err != nil {
			fmt.Printf("init testupstream: %v", err)
			cleanup()
			cancel()
			os.Exit(1)
		}

		if err := initEnvoyGateway(ctx); err != nil {
			fmt.Printf("init envoy gateway: %v", err)
			cleanup()
			cancel()
			os.Exit(1)
		}

		if err := initAIGateway(ctx); err != nil {
			cleanup()
			cancel()
			os.Exit(1)
		}

		if err := initRateLimitServer(ctx); err != nil {
			fmt.Printf("init ratelimit server: %v", err)
			cleanup()
			cancel()
			os.Exit(1)
		}
	}

	code := m.Run()
	if os.Getenv("KEEP_CLUSTER") != "" {
		fmt.Printf("Keeping k3s cluster after test run. Use export KUBECONFIG=%s to access the test k3s cluster", kc.yamlPath)
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		<-sigCh
	}
	cleanup()
	cancel()
	os.Exit(code)
}

func initTestupstream(ctx context.Context) (err error) {
	fmt.Println("=== INIT: Test Upstream")
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		fmt.Printf("    --- done (took %.2fs in total)\n", elapsed.Seconds())
	}()
	fmt.Println("    --- applying manifests")
	if err = applyManifest(ctx, "./init/testupstream/"); err != nil {
		return
	}
	fmt.Println("    --- waiting for deployment")
	return waitForDeploymentReady("default", "testupstream")
}

func initRateLimitServer(ctx context.Context) (err error) {
	fmt.Println("=== INIT: Ratelimit server")
	fmt.Println("    --- applying manifests")
	if err = applyManifest(ctx, "./init/ratelimit/"); err != nil {
		return
	}
	fmt.Println("    --- waiting for deployment")
	if err := waitForDeploymentReady("ai-gateway-system", "redis"); err != nil {
		return err
	}
	return waitForDeploymentReady("ai-gateway-system", "ratelimit")
}

func initAIGateway(ctx context.Context) (err error) {
	fmt.Println("=== INIT: AI Gateway")
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		fmt.Printf("    --- done (took %.2fs in total)\n", elapsed.Seconds())
	}()
	fmt.Println("    --- applying manifests")
	if err = helmInstall(ctx, "aig", "../../manifests/charts/ai-gateway-helm", "ai-gateway-system",
		map[string]string{
			"envoy.extProcImage.repository": "ghcr.io/tetratelabs/ai-gateway-extproc",
			"logLevel":                      "debug",
		}); err != nil {
		return
	}
	return waitForDeploymentReady("ai-gateway-system", "ai-gateway-controller")
}

func initEnvoyGateway(ctx context.Context) (err error) {
	fmt.Println("=== INIT: Envoy Gateway")
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		fmt.Printf("    --- done (took %.2fs in total)\n", elapsed.Seconds())
	}()
	fmt.Println("    --- applying manifests")
	if err = applyManifest(ctx,
		fmt.Sprintf("https://github.com/envoyproxy/gateway/releases/download/%s/install.yaml",
			envoyGatewayVersion)); err != nil {
		return
	}

	fmt.Println("    --- patch for envoy-gateway-system")
	if err = applyManifest(ctx, "./init/envoygateway/"); err != nil {
		return
	}
	fmt.Println("    --- restart deployment")
	if err = restartDeployment(ctx, "envoy-gateway-system", "envoy-gateway"); err != nil {
		return
	}
	fmt.Println("    --- waiting for deployment")
	if err = waitForDeploymentReady("envoy-gateway-system", "envoy-gateway"); err != nil {
		return
	}
	return nil
}

// TestE2E runs the end-to-end tests.
//
// We sequentially run the tests to avoid the conflict of the resources.
// Each test *must* be able to run independently for ease of debugging.
// In other words, each test *must not* rely on the state of the previous test.
func TestE2E(t *testing.T) {
	cases := []struct {
		caseName string
		caseFunc func(t *testing.T)
	}{
		{"awsInlineCredentials", testAWSInlineCredentials},
		{"awsCredentialsFile", testAWSCredentialsFile},
		{"resourceTranslation", testResourceTranslation},
		{"resourceTranslationMultipleGateways", testResourceTranslationMultipleGateways},
		{"rateLimitQuickstart", testRateLimitQuickstart},
		{"rateLimitBlockUnknown", testRateLimitBlockUnknown},
		{"rateLimitModelNameDistinct", testRateLimitModelNameDistinct},
		{"rateLimitHeaderMatchExact", testRateLimitHeaderMatchExact},
		{"rateLimitMultipleBackends", testRateLimitWithMultipleBackends},
		{"rateLimitMultipleLimits", testRateLimitMultipleLimits},
		{"rateLimitJWTClaim", testRateLimitJWTClaim},
		{"apiKeyBackend", testAPIKeyBackend},
	}

	for _, tc := range cases {
		t.Run(tc.caseName, tc.caseFunc)
	}

	require.Equal(t, float64(0), getMetricByName(t, aigNamespace, aigControllerSelector, "ratelimit_snapshot_failures_total"))
	require.Equal(t, float64(0), getMetricByName(t, aigNamespace, aigControllerSelector, "reconcile_failures_total"))
	require.Less(t, float64(0), getMetricByName(t, aigNamespace, aigControllerSelector, "extension_server_events_received_total"))
}

// testResourceTranslation runs the end-to-end test for the resource translation and updates for a single Gateway resource.
func testResourceTranslation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create the resource, and check the logs of the controller.
	const manifest = "testdata/testResourceTranslation.yaml"
	require.NoError(t, applyManifest(ctx, manifest))
	defer func() {
		require.NoError(t, deleteManifest(context.Background(), manifest))
		requireLLMRouteResourcesDeleted(t, "test-resource-translation-policy", "default")
	}()

	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=test-resource-translation"
	requireWaitForPodReady(t, egNamespace, egSelector)

	newDefaultV1ChatCompletionCase("foo-backend", "gpt4-o").
		setExpectedRequestHeaders(
			`x-ai-gateway-llm-model-name:gpt4-o`,
			`x-ai-gateway-llm-backend:foo-backend`,
		).
		run(t, egSelector, http.StatusOK)
	newDefaultV1ChatCompletionCase("bar-backend", "some-model").
		setExpectedRequestHeaders(
			`x-ai-gateway-llm-model-name:some-model`,
			`x-ai-gateway-llm-backend:bar-backend`,
		).
		run(t, egSelector, http.StatusOK)

	const extProcSelector = "ai-gateway.envoyproxy.io/owning-llm-route-name=test-resource-translation-policy"
	const extProcNs = "default"
	pods := listPods(t, extProcNs, extProcSelector)
	podNames := getPodNames(pods)
	require.Len(t, pods, 1)

	// Update the resource and see the changes.
	const manifestUpdated = "testdata/testResourceTranslation_updated.yaml"
	err := applyManifest(ctx, manifestUpdated)
	require.NoError(t, err)

	// Verify pods are rolled out.
	require.Eventually(t, func() bool {
		newPods := listPods(t, extProcNs, extProcSelector)
		newPodsNames := getPodNames(newPods)
		return len(newPodsNames.Intersection(podNames)) == 0
	}, time.Minute, 3*time.Second)
	newDefaultV1ChatCompletionCase("cat-backend", "some-model").
		setExpectedRequestHeaders(
			`x-ai-gateway-llm-model-name:some-model`,
			`x-ai-gateway-llm-backend:cat-backend`,
		).
		run(t, egSelector, http.StatusOK)
}

func getPodNames(pods []corev1.Pod) sets.Set[string] {
	s := sets.New[string]()
	for _, pod := range pods {
		s.Insert(pod.Name)
	}
	return s
}

// testResourceTranslationMultipleGateways is for the resource translation and updates for multiple Gateway resource.
// This attaches the same LLMRoute to multiple Gateways to ensure the translation is done correctly.
func testResourceTranslationMultipleGateways(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const manifest = "testdata/testResourceTranslationMultipleGateways.yaml"

	// Create the resource, and check the logs of the controller.
	require.NoError(t, applyManifest(ctx, manifest))
	defer func() {
		require.NoError(t, deleteManifest(context.Background(), manifest))
		requireLLMRouteResourcesDeleted(t, "test-multiple-gateway", "default")
	}()

	// Wait for two Envoy Gateway pods to be ready.
	const egSelector1 = "gateway.envoyproxy.io/owning-gateway-name=test-multiple-gateway-1"
	const egSelector2 = "gateway.envoyproxy.io/owning-gateway-name=test-multiple-gateway-2"
	var wg sync.WaitGroup
	wg.Add(2)
	for _, selector := range []string{egSelector1, egSelector2} {
		go func(selector string) {
			defer wg.Done()
			requireWaitForPodReady(t, egNamespace, selector)
		}(selector)
	}
	wg.Wait()

	for _, tc := range []*testUpstreamCase{
		newDefaultV1ChatCompletionCase("foo-backend-multiple-gateway", "gpt4-o").
			setExpectedRequestHeaders(`x-ai-gateway-llm-model-name:gpt4-o`),
		newDefaultV1ChatCompletionCase("bar-backend-multiple-gateway", "some-model").
			setExpectedRequestHeaders(`x-ai-gateway-llm-model-name:some-model`),
	} {
		t.Run(tc.backend, func(t *testing.T) {
			tc.setName("gateway-1").run(t, egSelector1, http.StatusOK)
			tc.setName("gateway-2").run(t, egSelector2, http.StatusOK)
		})
	}
}

func newPortForwarder(t *testing.T, namespace string, selector string, port int) portForwarder { // nolint: unparam
	kclient, err := newForRestConfig(kc.config)
	require.NoError(t, err)
	pods, err := kclient.podsForSelector(namespace, selector)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(pods.Items), 1)
	nn := types.NamespacedName{
		Name:      pods.Items[0].Name,
		Namespace: namespace,
	}
	fw, err := newLocalPortForwarder(kclient, nn, 0, port)
	require.NoError(t, err)
	return fw
}

func restartDeployment(ctx context.Context, namespace, deployment string) error {
	cmd := kc.kubectl(ctx, "rollout", "restart", "deployment/"+deployment, "-n", namespace)
	return cmd.Run()
}

func waitForDeploymentReady(namespace, deployment string) (err error) {
	cmd := kc.kubectl(context.Background(), "wait", "--timeout=2m", "-n", namespace,
		"deployment/"+deployment, "--for=condition=Available")
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("error waiting for deployment %s in namespace %s: %w", deployment, namespace, err)
	}
	return
}

func requireWaitForPodReady(t *testing.T, namespace, labelSelector string) {
	// This repeats the wait subcommand in order to be able to wait for the
	// resources not created yet.
	requireWaitForPodReadyWithTimeout(t, namespace, labelSelector, 3*time.Minute)
}

func requireWaitForPodReadyWithTimeout(t *testing.T, namespace, labelSelector string, timeout time.Duration) {
	// This repeats the wait subcommand in order to be able to wait for the
	// resources not created yet.
	require.Eventually(t, func() bool {
		cmd := kc.kubectl(context.Background(), "wait", "--timeout=2s", "-n", namespace,
			"pods", "--for=condition=Ready", "-l", labelSelector)
		return cmd.Run() == nil
	}, timeout, 5*time.Second)
}

func requireWaitForHTTPRouteAccepted(t *testing.T, namespace, name string) { // nolint: unparam
	require.Eventually(t, func() bool {
		cmd := kc.kubectl(context.Background(), "wait", "--timeout=2m", "-n", namespace,
			"HTTPRoute/"+name, "--for=jsonpath={.status.parents[0].conditions[?(@.type==\"Accepted\")].status}=True")
		return cmd.Run() == nil
	}, 3*time.Minute, 5*time.Second)
}

func helmInstall(ctx context.Context, releaseName, chart, namespace string, values map[string]string) (err error) {
	cmd := kc.helm(ctx, "install", releaseName, chart, "--namespace", namespace, "--create-namespace")
	for k, v := range values {
		cmd.Args = append(cmd.Args, "--set", fmt.Sprintf("%s=%s", k, v))
	}
	return cmd.Run()
}

func applyManifest(ctx context.Context, manifest string) (err error) {
	cmd := kc.kubectl(ctx, "apply", "--server-side", "-f", manifest)
	return cmd.Run()
}

func deleteManifest(ctx context.Context, manifest string) (err error) {
	cmd := kc.kubectl(ctx, "delete", "-f", manifest)
	return cmd.Run()
}

// requireLLMRouteResourcesDeleted waits until the resources created by the LLMRoute are deleted.
func requireLLMRouteResourcesDeleted(t *testing.T, llmRouteName, llmRouteNamespace string) { // nolint: unparam
	selector := "ai-gateway.envoyproxy.io/owning-llm-route-name=" + llmRouteName
	requireWaitUntilDeleted(t, "deployments", llmRouteNamespace, selector)
	requireWaitUntilDeleted(t, "services", llmRouteNamespace, selector)
	requireWaitUntilDeleted(t, "envoyextensionpolicies", llmRouteNamespace, selector)
}

// requireWaitUntilDeleted waits until the resources are deleted by trying to get
// the resources with the given label selector.
func requireWaitUntilDeleted(t *testing.T, kind, namespace, labelSelector string) {
	require.Eventually(t, func() bool {
		stderr := &bytes.Buffer{}
		cmd := kc.kubectl(context.Background(), "get", kind, "-n", namespace, "-l", labelSelector)
		cmd.Stderr = stderr
		if cmd.Run() != nil {
			fmt.Println(stderr.String())
			return false
		}
		return strings.Contains(stderr.String(), "No resources found")
	}, 3*time.Minute, 5*time.Second)
}

func listPods(t *testing.T, ns string, selector string) []corev1.Pod {
	kclient, err := newForRestConfig(kc.config)
	require.NoError(t, err)
	pods, err := kclient.podsForSelector(ns, selector)
	require.NoError(t, err)
	return pods.Items
}

// testAPIKeyBackend ensures that the API key backend is correctly handled.
func testAPIKeyBackend(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create the LLMRoute resource, and wait for the resources to be ready.
	const policyManifest = "testdata/testAPIKeyBackend.yaml"
	require.NoError(t, applyManifest(ctx, policyManifest))
	defer func() {
		require.NoError(t, deleteManifest(context.Background(), policyManifest))
	}()

	// Wait for the dynamic forwarder to be ready.
	const routeName = "test-api-key-backend"
	requireWaitForPodReady(t, "default", "ai-gateway.envoyproxy.io/owning-llm-route-name="+routeName)
	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=test-api-key-backend"
	requireWaitForPodReady(t, egNamespace, egSelector)

	newDefaultV1ChatCompletionCase("testupstream", "gpt4-o").
		setExpectedRequestHeaders(`Authorization:Bearer thisisapikey`, `x-ai-gateway-llm-model-name:gpt4-o`).
		run(t, egSelector, http.StatusOK)

	newDefaultV1ChatCompletionCase("testupstream-canary", "llama3.2").
		setExpectedRequestHeaders(`Authorization:Bearer inlineapikey`, `x-ai-gateway-llm-model-name:llama3.2`).
		run(t, egSelector, http.StatusOK)
}
