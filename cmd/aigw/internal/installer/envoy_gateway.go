// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package installer

import (
	"context"
	"fmt"
	"time"

	"github.com/envoyproxy/ai-gateway/cmd/aigw/internal/k8s"
	"github.com/envoyproxy/ai-gateway/cmd/aigw/internal/ui"
)

// EnvoyGatewayInstaller handles Envoy Gateway installation.
type EnvoyGatewayInstaller struct {
	output     *ui.Output
	k8sClient  *k8s.Client
	helmClient *k8s.HelmClient
}

// NewEnvoyGatewayInstaller creates a new Envoy Gateway installer.
func NewEnvoyGatewayInstaller(output *ui.Output, k8sClient *k8s.Client, helmClient *k8s.HelmClient) *EnvoyGatewayInstaller {
	return &EnvoyGatewayInstaller{
		output:     output,
		k8sClient:  k8sClient,
		helmClient: helmClient,
	}
}

// EnvoyGatewayOptions contains options for Envoy Gateway installation.
type EnvoyGatewayOptions struct {
	Namespace       string
	Version         string
	Timeout         time.Duration
	CreateNamespace bool
}

// Install installs Envoy Gateway using Helm.
func (e *EnvoyGatewayInstaller) Install(ctx context.Context, opts EnvoyGatewayOptions) error {
	e.output.Info("Installing Envoy Gateway...")

	// Create namespace if needed
	if opts.CreateNamespace {
		err := e.k8sClient.CreateNamespace(ctx, opts.Namespace)
		if err != nil {
			return fmt.Errorf("failed to create namespace %s: %w", opts.Namespace, err)
		}
		e.output.Success(fmt.Sprintf("Created namespace: %s", opts.Namespace))
	}

	// Install Envoy Gateway using Helm
	chartOpts := k8s.ChartOptions{
		ChartRef:        "oci://docker.io/envoyproxy/gateway-helm",
		ReleaseName:     "eg",
		Namespace:       opts.Namespace,
		Version:         opts.Version,
		Values:          e.getDefaultValues(),
		Wait:            true,
		Timeout:         opts.Timeout,
		CreateNamespace: opts.CreateNamespace,
		SkipCRDs:        false,
	}

	spinner := ui.NewSpinner(e.output.Writer(), "Installing Envoy Gateway chart...")
	spinner.Start()

	err := e.helmClient.InstallOrUpgradeChart(ctx, chartOpts)
	if err != nil {
		spinner.ErrorAndStop("Failed to install Envoy Gateway")
		return fmt.Errorf("failed to install envoy gateway chart: %w", err)
	}

	spinner.SuccessAndStop("Envoy Gateway chart installed successfully")

	// Wait for deployment to be ready
	spinner = ui.NewSpinner(e.output.Writer(), "Waiting for Envoy Gateway deployment to be ready...")
	spinner.Start()

	err = e.k8sClient.WaitForDeployment(ctx, opts.Namespace, "envoy-gateway", opts.Timeout)
	if err != nil {
		spinner.ErrorAndStop("Envoy Gateway deployment failed to become ready")
		return fmt.Errorf("envoy gateway deployment not ready: %w", err)
	}

	spinner.SuccessAndStop("Envoy Gateway deployment is ready")

	e.output.Success("Envoy Gateway installation completed successfully")
	return nil
}

// Uninstall uninstalls Envoy Gateway.
func (e *EnvoyGatewayInstaller) Uninstall(ctx context.Context, opts EnvoyGatewayOptions) error {
	e.output.Info("Uninstalling Envoy Gateway...")

	spinner := ui.NewSpinner(e.output.Writer(), "Uninstalling Envoy Gateway...")
	spinner.Start()

	err := e.helmClient.UninstallChart(ctx, "eg", opts.Namespace, opts.Timeout)
	if err != nil {
		spinner.ErrorAndStop("Failed to uninstall Envoy Gateway")
		return fmt.Errorf("failed to uninstall envoy gateway: %w", err)
	}

	spinner.SuccessAndStop("Envoy Gateway uninstalled successfully")

	e.output.Success("Envoy Gateway uninstallation completed")
	return nil
}

// getDefaultValues returns the default values for Envoy Gateway installation.
func (e *EnvoyGatewayInstaller) getDefaultValues() map[string]interface{} {
	return map[string]interface{}{
		// Add any default values needed for Envoy Gateway
		"deployment": map[string]interface{}{
			"replicas": 1,
		},
		"service": map[string]interface{}{
			"type": "ClusterIP",
		},
	}
}

// CheckStatus checks the status of Envoy Gateway installation.
func (e *EnvoyGatewayInstaller) CheckStatus(ctx context.Context, namespace string) error {
	// Check if Helm release exists
	status, err := e.helmClient.GetReleaseStatus(ctx, "eg", namespace)
	if err != nil {
		return fmt.Errorf("failed to get envoy gateway release status: %w", err)
	}

	e.output.Info(fmt.Sprintf("Envoy Gateway release status: %s", status))

	// Check deployment status
	pods, err := e.k8sClient.GetPods(ctx, namespace, "app.kubernetes.io/name=envoy-gateway")
	if err != nil {
		return fmt.Errorf("failed to get envoy gateway pods: %w", err)
	}

	e.output.Info(fmt.Sprintf("Found %d Envoy Gateway pods", len(pods.Items)))

	for _, pod := range pods.Items {
		e.output.Info(fmt.Sprintf("Pod %s: %s", pod.Name, pod.Status.Phase))
	}

	return nil
}

// GetInstallationInfo returns information about the Envoy Gateway installation.
func (e *EnvoyGatewayInstaller) GetInstallationInfo(ctx context.Context, namespace string) (map[string]string, error) {
	info := make(map[string]string)

	// Get release status
	status, err := e.helmClient.GetReleaseStatus(ctx, "eg", namespace)
	if err != nil {
		info["helm_status"] = "unknown"
	} else {
		info["helm_status"] = status
	}

	// Get deployment status
	pods, err := e.k8sClient.GetPods(ctx, namespace, "app.kubernetes.io/name=envoy-gateway")
	if err != nil {
		info["pod_count"] = "unknown"
	} else {
		info["pod_count"] = fmt.Sprintf("%d", len(pods.Items))
	}

	// Get version information
	version, err := e.k8sClient.GetServerVersion(ctx)
	if err != nil {
		info["k8s_version"] = "unknown"
	} else {
		info["k8s_version"] = version
	}

	return info, nil
}
