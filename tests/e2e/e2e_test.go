//go:build test_e2e

package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"
)

func initLog(msg string) {
	fmt.Printf("\u001b[32m=== INIT LOG: %s\u001B[0m\n", msg)
}

func TestMain(m *testing.M) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(5*time.Minute))
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
		"oci://docker.io/envoyproxy/gateway-helm", "--version", "v0.0.0-latest",
		"-n", "envoy-gateway-system", "--create-namespace")
	helm.Stdout = os.Stdout
	helm.Stderr = os.Stderr
	if err = helm.Run(); err != nil {
		return
	}

	initLog("\tApplying Patch for Envoy Gateway")
	if err = kubectlApplyManifest(ctx, "./init/envoygateway/"); err != nil {
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
	helm := exec.CommandContext(ctx, "helm", "upgrade", "-i", "eaig",
		"../../manifests/charts/ai-gateway-helm",
		"-n", "envoy-ai-gateway-system", "--create-namespace")
	helm.Stdout = os.Stdout
	helm.Stderr = os.Stderr
	if err = helm.Run(); err != nil {
		return
	}
	return kubectlWaitForDeploymentReady("envoy-ai-gateway-system", "ai-gateway-controller")
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

func kubectlRestartDeployment(ctx context.Context, namespace, deployment string) error {
	cmd := kubectl(ctx, "rollout", "restart", "deployment/"+deployment, "-n", namespace)
	return cmd.Run()
}

func kubectlWaitForDeploymentReady(namespace, deployment string) (err error) {
	cmd := kubectl(context.Background(), "wait", "--timeout=2m", "-n", namespace,
		"deployment/"+deployment, "--for=condition=Available")
	if err = cmd.Run(); err != nil {
		return fmt.Errorf("error waiting for deployment %s in namespace %s: %w", deployment, namespace, err)
	}
	return
}
