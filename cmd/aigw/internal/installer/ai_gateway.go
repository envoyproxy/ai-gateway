// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package installer

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/envoyproxy/ai-gateway/cmd/aigw/internal/k8s"
	"github.com/envoyproxy/ai-gateway/cmd/aigw/internal/ui"
)

// AIGatewayInstaller handles AI Gateway installation.
type AIGatewayInstaller struct {
	output     *ui.Output
	k8sClient  *k8s.Client
	helmClient *k8s.HelmClient
}

// NewAIGatewayInstaller creates a new AI Gateway installer.
func NewAIGatewayInstaller(output *ui.Output, k8sClient *k8s.Client, helmClient *k8s.HelmClient) *AIGatewayInstaller {
	return &AIGatewayInstaller{
		output:     output,
		k8sClient:  k8sClient,
		helmClient: helmClient,
	}
}

// AIGatewayOptions contains options for AI Gateway installation.
type AIGatewayOptions struct {
	Namespace       string
	Version         string
	SkipCRDs        bool
	Timeout         time.Duration
	CreateNamespace bool
}

// Install installs AI Gateway using Helm.
func (a *AIGatewayInstaller) Install(ctx context.Context, opts AIGatewayOptions) error {
	a.output.Info("Installing AI Gateway...")

	// Create namespace if needed
	if opts.CreateNamespace {
		err := a.k8sClient.CreateNamespace(ctx, opts.Namespace)
		if err != nil {
			return fmt.Errorf("failed to create namespace %s: %w", opts.Namespace, err)
		}
		a.output.Success(fmt.Sprintf("Created namespace: %s", opts.Namespace))
	}

	// Install CRDs first if not skipping
	if !opts.SkipCRDs {
		err := a.installCRDs(ctx, opts)
		if err != nil {
			return fmt.Errorf("failed to install CRDs: %w", err)
		}
	}

	// Install AI Gateway using command line helm to work around namespace issues

	spinner := ui.NewSpinner(a.output.Writer(), "Installing AI Gateway chart...")
	spinner.Start()

	// Use command line helm to work around namespace issues in the AI Gateway chart
	err := a.installAIGatewayWithCLI(ctx, opts)
	if err != nil {
		spinner.ErrorAndStop("Failed to install AI Gateway")
		return fmt.Errorf("failed to install ai gateway chart: %w", err)
	}

	spinner.SuccessAndStop("AI Gateway chart installed successfully")

	// Wait for deployment to be ready
	spinner = ui.NewSpinner(a.output.Writer(), "Waiting for AI Gateway deployment to be ready...")
	spinner.Start()

	err = a.k8sClient.WaitForDeployment(ctx, opts.Namespace, "ai-gateway-controller", opts.Timeout)
	if err != nil {
		spinner.ErrorAndStop("AI Gateway deployment failed to become ready")
		return fmt.Errorf("ai gateway deployment not ready: %w", err)
	}

	spinner.SuccessAndStop("AI Gateway deployment is ready")

	a.output.Success("AI Gateway installation completed successfully")
	return nil
}

// installCRDs installs AI Gateway CRDs separately.
func (a *AIGatewayInstaller) installCRDs(ctx context.Context, opts AIGatewayOptions) error {
	a.output.Info("Installing AI Gateway CRDs...")

	chartOpts := k8s.ChartOptions{
		ChartRef:        "oci://docker.io/envoyproxy/ai-gateway-crds-helm",
		ReleaseName:     "aieg-crd",
		Namespace:       opts.Namespace,
		Version:         opts.Version,
		Values:          map[string]interface{}{},
		Wait:            true,
		Timeout:         opts.Timeout,
		CreateNamespace: opts.CreateNamespace,
		SkipCRDs:        false,
	}

	spinner := ui.NewSpinner(a.output.Writer(), "Installing AI Gateway CRDs...")
	spinner.Start()

	err := a.helmClient.InstallOrUpgradeChart(ctx, chartOpts)
	if err != nil {
		spinner.ErrorAndStop("Failed to install AI Gateway CRDs")
		return fmt.Errorf("failed to install ai gateway crds: %w", err)
	}

	spinner.SuccessAndStop("AI Gateway CRDs installed successfully")
	return nil
}

// Uninstall uninstalls AI Gateway.
func (a *AIGatewayInstaller) Uninstall(ctx context.Context, opts AIGatewayOptions) error {
	a.output.Info("Uninstalling AI Gateway...")

	// Uninstall main chart
	spinner := ui.NewSpinner(a.output.Writer(), "Uninstalling AI Gateway...")
	spinner.Start()

	err := a.helmClient.UninstallChart(ctx, "aieg", opts.Namespace, opts.Timeout)
	if err != nil {
		spinner.ErrorAndStop("Failed to uninstall AI Gateway")
		a.output.Warning(fmt.Sprintf("Failed to uninstall AI Gateway: %v", err))
	} else {
		spinner.SuccessAndStop("AI Gateway uninstalled successfully")
	}

	// Uninstall CRDs if they were installed separately
	if !opts.SkipCRDs {
		spinner = ui.NewSpinner(a.output.Writer(), "Uninstalling AI Gateway CRDs...")
		spinner.Start()

		err = a.helmClient.UninstallChart(ctx, "aieg-crd", opts.Namespace, opts.Timeout)
		if err != nil {
			spinner.ErrorAndStop("Failed to uninstall AI Gateway CRDs")
			a.output.Warning(fmt.Sprintf("Failed to uninstall AI Gateway CRDs: %v", err))
		} else {
			spinner.SuccessAndStop("AI Gateway CRDs uninstalled successfully")
		}
	}

	a.output.Success("AI Gateway uninstallation completed")
	return nil
}

// getDefaultValues returns the default values for AI Gateway installation.
func (a *AIGatewayInstaller) getDefaultValues() map[string]interface{} {
	return map[string]interface{}{
		"controller": map[string]interface{}{
			"replicas": 1,
			"image": map[string]interface{}{
				"pullPolicy": "IfNotPresent",
			},
		},
		"service": map[string]interface{}{
			"type": "ClusterIP",
		},
		"resources": map[string]interface{}{
			"limits": map[string]interface{}{
				"cpu":    "500m",
				"memory": "512Mi",
			},
			"requests": map[string]interface{}{
				"cpu":    "100m",
				"memory": "128Mi",
			},
		},
	}
}

// CheckStatus checks the status of AI Gateway installation.
func (a *AIGatewayInstaller) CheckStatus(ctx context.Context, namespace string) error {
	// Check if Helm release exists
	status, err := a.helmClient.GetReleaseStatus(ctx, "aieg", namespace)
	if err != nil {
		return fmt.Errorf("failed to get ai gateway release status: %w", err)
	}

	a.output.Info(fmt.Sprintf("AI Gateway release status: %s", status))

	// Check deployment status
	pods, err := a.k8sClient.GetPods(ctx, namespace, "app.kubernetes.io/name=ai-gateway")
	if err != nil {
		return fmt.Errorf("failed to get ai gateway pods: %w", err)
	}

	a.output.Info(fmt.Sprintf("Found %d AI Gateway pods", len(pods.Items)))

	for _, pod := range pods.Items {
		a.output.Info(fmt.Sprintf("Pod %s: %s", pod.Name, pod.Status.Phase))
	}

	return nil
}

// GetInstallationInfo returns information about the AI Gateway installation.
func (a *AIGatewayInstaller) GetInstallationInfo(ctx context.Context, namespace string) (map[string]string, error) {
	info := make(map[string]string)

	// Get release status
	status, err := a.helmClient.GetReleaseStatus(ctx, "aieg", namespace)
	if err != nil {
		info["helm_status"] = "unknown"
	} else {
		info["helm_status"] = status
	}

	// Get CRD release status
	crdStatus, err := a.helmClient.GetReleaseStatus(ctx, "aieg-crd", namespace)
	if err != nil {
		info["crd_helm_status"] = "not_installed"
	} else {
		info["crd_helm_status"] = crdStatus
	}

	// Get deployment status
	pods, err := a.k8sClient.GetPods(ctx, namespace, "app.kubernetes.io/name=ai-gateway")
	if err != nil {
		info["pod_count"] = "unknown"
	} else {
		info["pod_count"] = fmt.Sprintf("%d", len(pods.Items))
	}

	return info, nil
}

// installAIGatewayWithCLI installs AI Gateway using command line helm to work around namespace issues.
func (a *AIGatewayInstaller) installAIGatewayWithCLI(ctx context.Context, opts AIGatewayOptions) error {
	// Use command line helm to install with proper namespace handling
	chartRef := "oci://docker.io/envoyproxy/ai-gateway-helm"

	// Build helm install command
	args := []string{
		"install", "aieg", chartRef,
		"--namespace", opts.Namespace,
		"--create-namespace",
		"--version", opts.Version,
		"--wait",
		"--timeout", opts.Timeout.String(),
	}

	if opts.SkipCRDs {
		args = append(args, "--skip-crds")
	}

	// Execute helm install
	cmd := exec.CommandContext(ctx, "helm", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm install failed: %w, output: %s", err, string(output))
	}

	return nil
}
