//go:build test_e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	egLatest      = "v0.0.0-latest" // This defaults to the latest dev version.
	egNamespace   = "envoy-gateway-system"
	egDefaultPort = 10080
)

var egVersion = func() string {
	if v, ok := os.LookupEnv("EG_VERSION"); ok {
		return v
	} else {
		return egLatest
	}
}()

func initLog(msg string) {
	fmt.Printf("\u001b[32m=== INIT LOG: %s\u001B[0m\n", msg)
}

func TestMain(m *testing.M) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(5*time.Minute))

	// The following code sets up the kind cluster, installs the Envoy Gateway, and installs the AI Gateway.
	// They must be idempotent and can be run multiple times so that we can run the tests multiple times on
	// failures.

	if err := initKindCluster(ctx); err != nil {
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

	if err := initTestupstream(ctx); err != nil {
		cancel()
		panic(err)
	}

	if err := initRateLimitServer(ctx); err != nil {
		cancel()
		panic(err)
	}

	code := m.Run()
	cancel()
	os.Exit(code)
}

func initKindCluster(ctx context.Context) (err error) {
	const (
		kindPath        = "../../.bin/kind"
		kindClusterName = "envoy-ai-gateway"
	)

	initLog("Setting up the kind cluster")
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		initLog(fmt.Sprintf("\tdone (took %.2fs in total)", elapsed.Seconds()))
	}()

	initLog("\tCreating kind cluster named envoy-ai-gateway")
	cmd := exec.CommandContext(ctx, kindPath, "create", "cluster", "--name", kindClusterName)
	out, err := cmd.CombinedOutput()
	if err != nil && !bytes.Contains(out, []byte("already exist")) {
		fmt.Printf("Error creating kind cluster: %s\n", out)
		return
	}

	initLog("\tSwitching kubectl context to envoy-ai-gateway")
	cmd = exec.CommandContext(ctx, kindPath, "export", "kubeconfig", "--name", kindClusterName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		return
	}

	initLog("\tLoading Docker images into kind cluster")
	for _, image := range []string{
		"ghcr.io/envoyproxy/ai-gateway/controller:latest",
		"ghcr.io/envoyproxy/ai-gateway/extproc:latest",
		"ghcr.io/envoyproxy/ai-gateway/testupstream:latest",
	} {
		cmd := exec.CommandContext(ctx, kindPath, "load", "docker-image", image, "--name", kindClusterName)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err = cmd.Run(); err != nil {
			return
		}
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
	helm := exec.CommandContext(ctx, "helm", "upgrade", "-i", "eg",
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
	initLog("\tRestart Envoy Gateway deployment")
	if err = kubectlRestartDeployment(ctx, "envoy-gateway-system", "envoy-gateway"); err != nil {
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
	helm := exec.CommandContext(ctx, "helm", "upgrade", "-i", "ai-eg",
		"../../manifests/charts/ai-gateway-helm",
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

func initTestupstream(ctx context.Context) (err error) {
	initLog("Installing Test Upstream sever")
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		initLog(fmt.Sprintf("\tdone (took %.2fs in total)\n", elapsed.Seconds()))
	}()
	initLog("\tapplying manifests")
	if err = kubectlApplyManifest(ctx, "./init/testupstream/"); err != nil {
		return
	}
	initLog("\twaiting for deployment")
	return kubectlWaitForDeploymentReady("default", "testupstream")
}

func initRateLimitServer(ctx context.Context) (err error) {
	initLog("Installing Redis for Rate limits")
	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		initLog(fmt.Sprintf("\tdone (took %.2fs in total)\n", elapsed.Seconds()))
	}()
	initLog("\tapplying manifests")
	if err = kubectlApplyManifest(ctx, "./init/ratelimit/"); err != nil {
		return
	}
	initLog("\twaiting for deployment")
	if err := kubectlWaitForDeploymentReady("redis-system", "redis"); err != nil {
		return err
	}
	return kubectlWaitForDeploymentReady("envoy-gateway-system", "envoy-ratelimit")
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

func requireWaitForPodReady(t *testing.T, namespace, labelSelector string) {
	// This repeats the wait subcommand in order to be able to wait for the
	// resources not created yet.
	requireWaitForPodReadyWithTimeout(t, namespace, labelSelector, 3*time.Minute)
}

func requireWaitForPodReadyWithTimeout(t *testing.T, namespace, labelSelector string, timeout time.Duration) {
	// This repeats the wait subcommand in order to be able to wait for the
	// resources not created yet.
	require.Eventually(t, func() bool {
		cmd := kubectl(context.Background(), "wait", "--timeout=2s", "-n", namespace,
			"pods", "--for=condition=Ready", "-l", labelSelector)
		return cmd.Run() == nil
	}, timeout, 5*time.Second)
}

func requireNewHTTPPortForwarder(t *testing.T, namespace string, selector string, port int) portForwarder {
	f, err := newPodPortForwarder(context.Background(), namespace, selector, port)
	require.NoError(t, err)
	require.Eventually(t, func() bool {
		conn, err := http.Get(f.address())
		if err != nil {
			t.Logf("error: %v", err)
			return false
		}
		_ = conn.Body.Close()
		return true // We don't care about the response.
	}, 3*time.Minute, 200*time.Millisecond)
	return f
}

// newPodPortForwarder creates a new local port forwarder for the namespace and selector.
func newPodPortForwarder(ctx context.Context, namespace, selector string, podPort int) (f portForwarder, err error) {
	l, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		return portForwarder{}, fmt.Errorf("failed to get a local available port for Pod %q: %w", selector, err)
	}
	err = l.Close()
	if err != nil {
		return portForwarder{}, err
	}
	f.localPort = l.Addr().(*net.TCPAddr).Port

	cmd := kubectl(ctx, "get", "pod", "-n", namespace,
		"--selector="+selector, "-o", "jsonpath='{.items[0].metadata.name}'")
	cmd.Stdout = nil // To ensure that we can capture the output by Output().
	out, err := cmd.Output()
	if err != nil {
		return portForwarder{}, fmt.Errorf("failed to get service name: %w", err)
	}
	serviceName := string(out[1 : len(out)-1]) // Remove the quotes.

	cmd = kubectl(ctx, "port-forward",
		"-n", namespace, "pod/"+serviceName,
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
