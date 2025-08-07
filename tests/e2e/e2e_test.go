// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	egDefaultVersion     = "v0.0.0-latest"
	egNamespace          = "envoy-gateway-system"
	egDefaultServicePort = 80

	kindClusterName = "envoy-ai-gateway"
	kindLogDir      = "./logs"
	metallbVersion  = "v0.13.10"
)

var egVersion = func() string {
	if v, ok := os.LookupEnv("EG_VERSION"); ok {
		return v
	}
	return egDefaultVersion
}()

// By default, kind logs are collected when the e2e tests fail. The TEST_KEEP_CLUSTER environment variable
// can be set to "true" to preserve the logs and the kind cluster even if the tests pass.
var keepCluster = func() bool {
	v, _ := os.LookupEnv("TEST_KEEP_CLUSTER")
	return v == "true"
}()

func initLog(msg string) {
	fmt.Printf("\u001b[32m=== INIT LOG: %s\u001B[0m\n", msg)
}

func cleanupLog(msg string) {
	fmt.Printf("\u001b[32m=== CLEANUP LOG: %s\u001B[0m\n", msg)
}

func TestMain(m *testing.M) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(5*time.Minute))

	// The following code sets up the kind cluster, installs the Envoy Gateway, and installs the AI Gateway.
	// They must be idempotent and can be run multiple times so that we can run the tests multiple times on
	// failures.

	run := false
	defer func() {
		// If the setup or some tests panic, try to collect the cluster logs.
		if r := recover(); r != nil {
			cleanupKindCluster(true)
		}
		if !run {
			panic("BUG: no tests were run. This is likely a bug during the setup")
		}
	}()

	if err := initKindCluster(ctx); err != nil {
		cancel()
		panic(err)
	}

	if err := initMetalLB(ctx); err != nil {
		cancel()
		panic(err)
	}

	if err := installInferencePoolEnvironment(ctx); err != nil {
		cancel()
		panic(err)
	}

	if err := initEnvoyGateway(ctx); err != nil {
		cancel()
		panic(err)
	}

	if err := initAIGateway(ctx); err != nil {
		cancel()
		panic(err)
	}

	if err := initPrometheus(ctx); err != nil {
		cancel()
		panic(err)
	}

	code := m.Run()
	run = true
	cancel()

	cleanupKindCluster(code != 0)

	os.Exit(code) // nolint: gocritic
}

func initKindCluster(ctx context.Context) (err error) {
	initLog("Setting up the kind cluster")
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		initLog(fmt.Sprintf("\tdone (took %.2fs in total)", elapsed.Seconds()))
	}()

	initLog(fmt.Sprintf("\tCreating kind cluster named %s", kindClusterName))
	cmd := exec.CommandContext(ctx, "go", "tool", "kind", "create", "cluster", "--name", kindClusterName)
	out, err := cmd.CombinedOutput()
	if err != nil && !bytes.Contains(out, []byte("already exist")) {
		fmt.Printf("Error creating kind cluster: %s\n", out)
		return
	}

	initLog(fmt.Sprintf("\tSwitching kubectl context to %s", kindClusterName))
	cmd = exec.CommandContext(ctx, "go", "tool", "kind", "export", "kubeconfig", "--name", kindClusterName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		return
	}

	initLog("\tLoading Docker images into kind cluster")
	for _, image := range []string{
		"docker.io/envoyproxy/ai-gateway-controller:latest",
		"docker.io/envoyproxy/ai-gateway-extproc:latest",
		"docker.io/envoyproxy/ai-gateway-testupstream:latest",
	} {
		cmd := exec.CommandContext(ctx, "go", "tool", "kind", "load", "docker-image", image, "--name", kindClusterName)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err = cmd.Run(); err != nil {
			return
		}
	}
	return nil
}

func initMetalLB(ctx context.Context) (err error) {
	initLog("Installing MetalLB")
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		initLog(fmt.Sprintf("\tdone (took %.2fs in total)", elapsed.Seconds()))
	}()

	// Install MetalLB manifests.
	initLog("\tApplying MetalLB manifests")
	manifestURL := fmt.Sprintf("https://raw.githubusercontent.com/metallb/metallb/%s/config/manifests/metallb-native.yaml", metallbVersion)
	if err = kubectlApplyManifest(ctx, manifestURL); err != nil {
		return fmt.Errorf("failed to apply MetalLB manifests: %w", err)
	}

	// Create memberlist secret if it doesn't exist.
	initLog("\tCreating memberlist secret if needed")
	cmd := kubectl(ctx, "get", "secret", "-n", "metallb-system", "memberlist", "--no-headers", "--ignore-not-found", "-o", "custom-columns=NAME:.metadata.name")
	cmd.Stdout = nil
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to check memberlist secret: %w", err)
	}

	if strings.TrimSpace(string(out)) == "" {
		// Generate random secret key.
		cmd = exec.CommandContext(ctx, "openssl", "rand", "-base64", "128")
		cmd.Stderr = os.Stderr
		var secretKey []byte
		secretKey, err = cmd.Output()
		if err != nil {
			return fmt.Errorf("failed to generate secret key: %w", err)
		}

		cmd = kubectl(ctx, "create", "secret", "generic", "-n", "metallb-system", "memberlist", "--from-literal=secretkey="+strings.TrimSpace(string(secretKey)))
		if err = cmd.Run(); err != nil {
			return fmt.Errorf("failed to create memberlist secret: %w", err)
		}
	}

	// Wait for MetalLB deployments to be ready.
	initLog("\tWaiting for MetalLB controller deployment to be ready")
	if err = kubectlWaitForDeploymentReady("metallb-system", "controller"); err != nil {
		return fmt.Errorf("failed to wait for MetalLB controller: %w", err)
	}

	initLog("\tWaiting for MetalLB speaker daemonset to be ready")
	if err = kubectlWaitForDaemonSetReady("metallb-system", "speaker"); err != nil {
		return fmt.Errorf("failed to wait for MetalLB speaker: %w", err)
	}

	// Configure IP address pools based on Docker network IPAM.
	initLog("\tConfiguring IP address pools")
	if err = configureMetalLBAddressPools(ctx); err != nil {
		return fmt.Errorf("failed to configure MetalLB address pools: %w", err)
	}

	return nil
}

func configureMetalLBAddressPools(ctx context.Context) error {
	// Get Docker network information for kind cluster.
	cmd := exec.CommandContext(ctx, "docker", "network", "inspect", "kind")
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("failed to inspect docker network: %w", err)
	}

	// Parse JSON output.
	var networks []struct {
		IPAM struct {
			Config []struct {
				Subnet string `json:"Subnet"`
			} `json:"Config"`
		} `json:"IPAM"`
	}

	if err := json.Unmarshal(out, &networks); err != nil {
		return fmt.Errorf("failed to parse docker network info: %w", err)
	}

	if len(networks) == 0 || len(networks[0].IPAM.Config) == 0 {
		return fmt.Errorf("no IPAM config found in docker network")
	}

	// Find IPv4 subnet and calculate address range.
	var addressRanges []string
	for _, config := range networks[0].IPAM.Config {
		subnet := config.Subnet
		if !strings.Contains(subnet, ":") { // IPv4.
			// Extract network prefix (e.g., "172.18.0.0/16" -> "172.18.0").
			parts := strings.Split(subnet, ".")
			if len(parts) >= 3 {
				addressPrefix := strings.Join(parts[:3], ".")
				addressRange := fmt.Sprintf("%s.200-%s.250", addressPrefix, addressPrefix)
				addressRanges = append(addressRanges, fmt.Sprintf("    - %s", addressRange))
			}
		}
	}

	if len(addressRanges) == 0 {
		return fmt.Errorf("no valid IPv4 address ranges found")
	}

	// Create MetalLB configuration manifest.
	manifest := fmt.Sprintf(`apiVersion: metallb.io/v1beta1
kind: IPAddressPool
metadata:
  namespace: metallb-system
  name: kube-services
spec:
  addresses:
%s
---
apiVersion: metallb.io/v1beta1
kind: L2Advertisement
metadata:
  name: kube-services
  namespace: metallb-system
spec:
  ipAddressPools:
  - kube-services`, strings.Join(addressRanges, "\n"))

	// Apply configuration with retries.
	const retryInterval = 5 * time.Second
	const timeout = 2 * time.Minute

	retryCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var lastErr error
	attempt := 1

	for {
		select {
		case <-retryCtx.Done():
			if lastErr != nil {
				return fmt.Errorf("timeout applying MetalLB configuration after %d attempts, last error: %w", attempt-1, lastErr)
			}
			return fmt.Errorf("timeout applying MetalLB configuration after %d attempts", attempt-1)
		default:
			if err := kubectlApplyManifestStdin(ctx, manifest); err == nil {
				return nil
			} else {
				lastErr = err
				if strings.Contains(err.Error(), "webhook") && strings.Contains(err.Error(), "connection refused") {
					// This is expected during MetalLB startup, continue retrying.
					fmt.Printf("\t\tAttempt %d: MetalLB webhook not ready yet, retrying in %v...\n", attempt, retryInterval)
				} else {
					// Other errors might be more serious, but still retry.
					fmt.Printf("\t\tAttempt %d: Error applying MetalLB config: %v, retrying in %v...\n", attempt, err, retryInterval)
				}
				attempt++
				time.Sleep(retryInterval)
			}
		}
	}
}

func cleanupKindCluster(testsFailed bool) {
	if testsFailed || keepCluster {
		cleanupLog("Collecting logs from the kind cluster")
		cmd := exec.Command("go", "tool", "kind", "export", "logs", "--name", kindClusterName, kindLogDir)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}
	if !testsFailed && !keepCluster {
		cleanupLog("Destroying the kind cluster")
		cmd := exec.Command("go", "tool", "kind", "delete", "cluster", "--name", kindClusterName)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		_ = cmd.Run()
	}
}

func installInferenceExtensionCRD(ctx context.Context) (err error) {
	const infExtURL = "https://github.com/kubernetes-sigs/gateway-api-inference-extension/releases/download/v0.5.1/manifests.yaml"
	return kubectlApplyManifest(ctx, infExtURL)
}

func installVLLMDeployment(ctx context.Context) (err error) {
	const vllmURL = "https://github.com/kubernetes-sigs/gateway-api-inference-extension/raw/main/config/manifests/vllm/sim-deployment.yaml"
	return kubectlApplyManifest(ctx, vllmURL)
}

func installInferenceModel(ctx context.Context) (err error) {
	const inferenceModelURL = "https://github.com/kubernetes-sigs/gateway-api-inference-extension/raw/v0.5.1/config/manifests/inferencemodel.yaml"
	return kubectlApplyManifest(ctx, inferenceModelURL)
}

func installInferencePoolResources(ctx context.Context) (err error) {
	const inferencePoolURL = "https://github.com/kubernetes-sigs/gateway-api-inference-extension/raw/v0.5.1/config/manifests/inferencepool-resources.yaml"
	return kubectlApplyManifest(ctx, inferencePoolURL)
}

func installInferencePoolEnvironment(ctx context.Context) (err error) {
	// Install all InferencePool related resources in sequence.
	if err = installInferenceExtensionCRD(ctx); err != nil {
		return fmt.Errorf("failed to install inference extension CRDs: %w", err)
	}

	if err = installVLLMDeployment(ctx); err != nil {
		return fmt.Errorf("failed to install vLLM deployment: %w", err)
	}

	if err = installInferenceModel(ctx); err != nil {
		return fmt.Errorf("failed to install inference model: %w", err)
	}

	if err = installInferencePoolResources(ctx); err != nil {
		return fmt.Errorf("failed to install inference pool resources: %w", err)
	}

	return nil
}

// initEnvoyGateway initializes the Envoy Gateway in the kind cluster following the quickstart guide:
// https://gateway.envoyproxy.io/latest/tasks/quickstart/
func initEnvoyGateway(ctx context.Context) (err error) {
	initLog("Installing Envoy Gateway")
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		initLog(fmt.Sprintf("\tdone (took %.2fs in total)", elapsed.Seconds()))
	}()
	initLog("\tHelm Install")
	helm := exec.CommandContext(ctx, "go", "tool", "helm", "upgrade", "-i", "eg",
		"oci://docker.io/envoyproxy/gateway-helm", "--version", egVersion,
		"-n", "envoy-gateway-system", "--create-namespace")
	helm.Stdout = os.Stdout
	helm.Stderr = os.Stderr
	if err = helm.Run(); err != nil {
		return
	}

	initLog("\tApplying Patch for Envoy Gateway")
	if err = kubectlApplyManifest(ctx, "../../manifests/envoy-gateway-config/"); err != nil {
		return
	}
	initLog("\tApplying InferencePool Patch for Envoy Gateway")
	if err = kubectlApplyManifest(ctx, "../../examples/inference-pool/config.yaml"); err != nil {
		return
	}
	initLog("\tRestart Envoy Gateway deployment")
	if err = kubectlRestartDeployment(ctx, "envoy-gateway-system", "envoy-gateway"); err != nil {
		return
	}
	initLog("\tWaiting for Ratelimit deployment to be ready")
	if err = kubectlWaitForDeploymentReady("envoy-gateway-system", "envoy-ratelimit"); err != nil {
		return
	}
	initLog("\tWaiting for Envoy Gateway deployment to be ready")
	return kubectlWaitForDeploymentReady("envoy-gateway-system", "envoy-gateway")
}

func initAIGateway(ctx context.Context) (err error) {
	initLog("Installing AI Gateway")
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		initLog(fmt.Sprintf("\tdone (took %.2fs in total)\n", elapsed.Seconds()))
	}()
	initLog("\tHelm Install")
	helmCRD := exec.CommandContext(ctx, "go", "tool", "helm", "upgrade", "-i", "ai-eg-crd",
		"../../manifests/charts/ai-gateway-crds-helm",
		"-n", "envoy-ai-gateway-system", "--create-namespace")
	helmCRD.Stdout = os.Stdout
	helmCRD.Stderr = os.Stderr
	if err = helmCRD.Run(); err != nil {
		return
	}

	helm := exec.CommandContext(ctx, "go", "tool", "helm", "upgrade", "-i", "ai-eg",
		"../../manifests/charts/ai-gateway-helm",
		// Configure the additional prometheus metrics label for user ID.
		"--set", "controller.metricsRequestHeaderLabels=x-user-id:"+userIDMetricsLabel,
		"-n", "envoy-ai-gateway-system", "--create-namespace")
	helm.Stdout = os.Stdout
	helm.Stderr = os.Stderr
	if err = helm.Run(); err != nil {
		return
	}
	// Restart the controller to pick up the new changes in the AI Gateway.
	initLog("\tRestart AI Gateway controller")
	if err = kubectlRestartDeployment(ctx, "envoy-ai-gateway-system", "ai-gateway-controller"); err != nil {
		return
	}
	return kubectlWaitForDeploymentReady("envoy-ai-gateway-system", "ai-gateway-controller")
}

func initPrometheus(ctx context.Context) (err error) {
	initLog("Installing Prometheus")
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		initLog(fmt.Sprintf("\tdone (took %.2fs in total)\n", elapsed.Seconds()))
	}()
	initLog("\tapplying manifests")
	if err = kubectlApplyManifest(ctx, "../../examples/monitoring/monitoring.yaml"); err != nil {
		return
	}
	initLog("\twaiting for deployment")
	return kubectlWaitForDeploymentReady("monitoring", "prometheus")
}

func kubectl(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func kubectlApplyManifest(ctx context.Context, manifest string) (err error) {
	cmd := kubectl(ctx, "apply", "--server-side", "-f", manifest, "--force-conflicts")
	return cmd.Run()
}

func kubectlApplyManifestStdin(ctx context.Context, manifest string) (err error) {
	cmd := kubectl(ctx, "apply", "--server-side", "-f", "-")
	cmd.Stdin = bytes.NewReader([]byte(manifest))
	return cmd.Run()
}

func kubectlDeleteManifest(ctx context.Context, manifest string) (err error) {
	cmd := kubectl(ctx, "delete", "-f", manifest)
	return cmd.Run()
}

func kubectlRestartDeployment(ctx context.Context, namespace, deployment string) error {
	cmd := kubectl(ctx, "rollout", "restart", "deployment/"+deployment, "-n", namespace)
	return cmd.Run()
}

func kubectlWaitForDeploymentReady(namespace, deployment string) (err error) {
	cmd := kubectl(context.Background(), "wait", "--timeout=2m", "-n", namespace,
		"deployment/"+deployment, "--for=create")
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("error waiting for deployment %s in namespace %s: %w", deployment, namespace, err)
	}

	cmd = kubectl(context.Background(), "wait", "--timeout=2m", "-n", namespace,
		"deployment/"+deployment, "--for=condition=Available")
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("error waiting for deployment %s in namespace %s: %w", deployment, namespace, err)
	}
	return
}

func kubectlWaitForDaemonSetReady(namespace, daemonset string) (err error) {
	// Wait for daemonset to be created.
	cmd := kubectl(context.Background(), "wait", "--timeout=2m", "-n", namespace,
		"daemonset/"+daemonset, "--for=create")
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("error waiting for daemonset %s in namespace %s: %w", daemonset, namespace, err)
	}

	// Wait for daemonset pods to be ready using jsonpath.
	cmd = kubectl(context.Background(), "wait", "--timeout=2m", "-n", namespace,
		"daemonset/"+daemonset, "--for=jsonpath={.status.numberReady}=1")
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("error waiting for daemonset %s pods to be ready in namespace %s: %w", daemonset, namespace, err)
	}
	return
}

func requireWaitForGatewayPodReady(t *testing.T, selector string) {
	// Wait for the Envoy Gateway pod containing the extproc container.
	require.Eventually(t, func() bool {
		cmd := kubectl(t.Context(), "get", "pod", "-n", egNamespace,
			"--selector="+selector, "-o", "jsonpath='{.items[0].spec.containers[*].name}'")
		cmd.Stdout = nil // To ensure that we can capture the output by Output().
		out, err := cmd.Output()
		if err != nil {
			t.Logf("error: %v", err)
			return false
		}
		return strings.Contains(string(out), "ai-gateway-extproc")
	}, 2*time.Minute, 1*time.Second)

	requireWaitForPodReady(t, selector)
}

func requireWaitForPodReady(t *testing.T, selector string) {
	// This repeats the wait subcommand in order to be able to wait for the
	// resources not created yet.
	require.Eventually(t, func() bool {
		cmd := kubectl(t.Context(), "wait", "--timeout=2s", "-n", egNamespace,
			"pods", "--for=condition=Ready", "-l", selector)
		return cmd.Run() == nil
	}, 3*time.Minute, 5*time.Second)
}

// requireNewHTTPPortForwarder creates a new port forwarder for the given namespace and selector.
func requireNewHTTPPortForwarder(t *testing.T, namespace string, selector string, port int) portForwarder {
	f, err := newServicePortForwarder(t.Context(), namespace, selector, port)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		res, err := http.Get(f.address())
		if err != nil {
			t.Logf("error: %v", err)
			return false
		}
		_ = res.Body.Close()
		return true // We don't care about the response.
	}, 3*time.Minute, 200*time.Millisecond)
	return f
}

// newServicePortForwarder creates a new local port forwarder for the namespace and selector.
func newServicePortForwarder(ctx context.Context, namespace, selector string, podPort int) (f portForwarder, err error) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return portForwarder{}, fmt.Errorf("failed to get a local available port for Pod %q: %w", selector, err)
	}
	err = l.Close()
	if err != nil {
		return portForwarder{}, err
	}
	f.localPort = l.Addr().(*net.TCPAddr).Port

	cmd := kubectl(ctx, "get", "svc", "-n", namespace,
		"--selector="+selector, "-o", "jsonpath='{.items[0].metadata.name}'")
	cmd.Stdout = nil // To ensure that we can capture the output by Output().
	out, err := cmd.Output()
	if err != nil {
		return portForwarder{}, fmt.Errorf("failed to get service name: %w", err)
	}
	serviceName := string(out[1 : len(out)-1]) // Remove the quotes.

	cmd = kubectl(ctx, "port-forward",
		"-n", namespace, "svc/"+serviceName,
		fmt.Sprintf("%d:%d", f.localPort, podPort),
	)
	if err := cmd.Start(); err != nil {
		return portForwarder{}, fmt.Errorf("failed to start port-forward: %w", err)
	}
	f.cmd = cmd
	return
}

// portForwarder is a local port forwarder to a pod.
type portForwarder struct {
	cmd       *exec.Cmd
	localPort int
}

// kill stops the port forwarder.
func (f portForwarder) kill() {
	_ = f.cmd.Process.Kill()
}

// address returns the address of the port forwarder.
func (f portForwarder) address() string {
	return fmt.Sprintf("http://127.0.0.1:%d", f.localPort)
}
