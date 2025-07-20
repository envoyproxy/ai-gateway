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

// Manager manages the installation process.
type Manager struct {
	output     *ui.Output
	k8sClient  *k8s.Client
	helmClient *k8s.HelmClient
	dryRun     bool
}

// NewManager creates a new installation manager.
func NewManager(output *ui.Output, dryRun bool) (*Manager, error) {
	k8sClient, err := k8s.NewClient()
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	helmClient := k8s.NewHelmClient(k8sClient.Config())

	return &Manager{
		output:     output,
		k8sClient:  k8sClient,
		helmClient: helmClient,
		dryRun:     dryRun,
	}, nil
}

// InstallOptions contains options for installation.
type InstallOptions struct {
	EnvoyGatewayNamespace string
	EnvoyGatewayVersion   string
	Namespace             string
	Version               string
	SkipCRDs              bool
	Timeout               time.Duration
	CreateNamespace       bool
}

// Install performs the complete installation process.
func (m *Manager) Install(ctx context.Context, opts InstallOptions) error {
	steps := []string{
		"Checking prerequisites",
		"Installing Envoy Gateway",
		"Installing AI Gateway",
		"Configuring Envoy Gateway",
		"Verifying installation",
	}

	progress := ui.NewStepProgress(m.output.Writer(), steps)
	progress.Start()

	// Step 1: Check prerequisites
	if err := m.checkPrerequisites(ctx); err != nil {
		progress.FailCurrentStep()
		return fmt.Errorf("prerequisites check failed: %w", err)
	}
	progress.NextStep()

	// Step 2: Install Envoy Gateway
	if err := m.installEnvoyGateway(ctx, opts); err != nil {
		progress.FailCurrentStep()
		return fmt.Errorf("envoy gateway installation failed: %w", err)
	}
	progress.NextStep()

	// Step 3: Install AI Gateway
	if err := m.installAIGateway(ctx, opts); err != nil {
		progress.FailCurrentStep()
		return fmt.Errorf("ai gateway installation failed: %w", err)
	}
	progress.NextStep()

	// Step 4: Configure Envoy Gateway
	if err := m.configureEnvoyGateway(ctx, opts); err != nil {
		progress.FailCurrentStep()
		return fmt.Errorf("envoy gateway configuration failed: %w", err)
	}
	progress.NextStep()

	// Step 5: Verify installation
	if err := m.verifyInstallation(ctx, opts); err != nil {
		progress.FailCurrentStep()
		return fmt.Errorf("installation verification failed: %w", err)
	}

	progress.Complete()
	return nil
}

// Uninstall performs the complete uninstallation process.
func (m *Manager) Uninstall(ctx context.Context, opts InstallOptions) error {
	steps := []string{
		"Removing AI Gateway configuration",
		"Uninstalling AI Gateway",
		"Uninstalling Envoy Gateway",
		"Cleaning up resources",
	}

	progress := ui.NewStepProgress(m.output.Writer(), steps)
	progress.Start()

	// Step 1: Remove AI Gateway configuration
	if err := m.removeAIGatewayConfig(ctx, opts); err != nil {
		m.output.Warning(fmt.Sprintf("Failed to remove AI Gateway configuration: %v", err))
	}
	progress.NextStep()

	// Step 2: Uninstall AI Gateway
	if err := m.uninstallAIGateway(ctx, opts); err != nil {
		m.output.Warning(fmt.Sprintf("Failed to uninstall AI Gateway: %v", err))
	}
	progress.NextStep()

	// Step 3: Uninstall Envoy Gateway
	if err := m.uninstallEnvoyGateway(ctx, opts); err != nil {
		m.output.Warning(fmt.Sprintf("Failed to uninstall Envoy Gateway: %v", err))
	}
	progress.NextStep()

	// Step 4: Clean up resources
	if err := m.cleanupResources(ctx, opts); err != nil {
		m.output.Warning(fmt.Sprintf("Failed to clean up resources: %v", err))
	}

	progress.Complete()
	return nil
}

// checkPrerequisites checks all prerequisites.
func (m *Manager) checkPrerequisites(ctx context.Context) error {
	if m.dryRun {
		m.output.Info("DRY RUN: Would check prerequisites")
		return nil
	}

	prereq := NewPrerequisites(m.output)
	results, err := prereq.CheckAll(ctx)
	if err != nil {
		return err
	}

	if !prereq.HasRequiredPrerequisites(results) {
		return fmt.Errorf("required prerequisites not met")
	}

	return nil
}

// installEnvoyGateway installs Envoy Gateway.
func (m *Manager) installEnvoyGateway(ctx context.Context, opts InstallOptions) error {
	if m.dryRun {
		m.output.Info("DRY RUN: Would install Envoy Gateway")
		return nil
	}

	envoyInstaller := NewEnvoyGatewayInstaller(m.output, m.k8sClient, m.helmClient)
	return envoyInstaller.Install(ctx, EnvoyGatewayOptions{
		Namespace:       opts.EnvoyGatewayNamespace,
		Version:         opts.EnvoyGatewayVersion,
		Timeout:         opts.Timeout,
		CreateNamespace: true,
	})
}

// installAIGateway installs AI Gateway.
func (m *Manager) installAIGateway(ctx context.Context, opts InstallOptions) error {
	if m.dryRun {
		m.output.Info("DRY RUN: Would install AI Gateway")
		return nil
	}

	aiInstaller := NewAIGatewayInstaller(m.output, m.k8sClient, m.helmClient)
	return aiInstaller.Install(ctx, AIGatewayOptions{
		Namespace:       opts.Namespace,
		Version:         opts.Version,
		SkipCRDs:        opts.SkipCRDs,
		Timeout:         opts.Timeout,
		CreateNamespace: opts.CreateNamespace,
	})
}

// configureEnvoyGateway configures Envoy Gateway for AI Gateway.
func (m *Manager) configureEnvoyGateway(ctx context.Context, opts InstallOptions) error {
	if m.dryRun {
		m.output.Info("DRY RUN: Would configure Envoy Gateway")
		return nil
	}

	configManager, err := NewConfigManager(m.output, m.k8sClient)
	if err != nil {
		return fmt.Errorf("failed to create config manager: %w", err)
	}
	return configManager.ApplyConfiguration(ctx, ConfigOptions{
		EnvoyGatewayNamespace: opts.EnvoyGatewayNamespace,
		AIGatewayNamespace:    opts.Namespace,
	})
}

// verifyInstallation verifies that the installation was successful.
func (m *Manager) verifyInstallation(ctx context.Context, opts InstallOptions) error {
	if m.dryRun {
		m.output.Info("DRY RUN: Would verify installation")
		return nil
	}

	// Check Envoy Gateway deployment
	err := m.k8sClient.WaitForDeployment(ctx, opts.EnvoyGatewayNamespace, "envoy-gateway", opts.Timeout)
	if err != nil {
		return fmt.Errorf("envoy gateway deployment not ready: %w", err)
	}

	// Check AI Gateway deployment
	err = m.k8sClient.WaitForDeployment(ctx, opts.Namespace, "ai-gateway-controller", opts.Timeout)
	if err != nil {
		return fmt.Errorf("ai gateway deployment not ready: %w", err)
	}

	m.output.Success("All deployments are ready")
	return nil
}

// uninstallAIGateway uninstalls AI Gateway.
func (m *Manager) uninstallAIGateway(ctx context.Context, opts InstallOptions) error {
	if m.dryRun {
		m.output.Info("DRY RUN: Would uninstall AI Gateway")
		return nil
	}

	return m.helmClient.UninstallChart(ctx, "aieg", opts.Namespace, opts.Timeout)
}

// uninstallEnvoyGateway uninstalls Envoy Gateway.
func (m *Manager) uninstallEnvoyGateway(ctx context.Context, opts InstallOptions) error {
	if m.dryRun {
		m.output.Info("DRY RUN: Would uninstall Envoy Gateway")
		return nil
	}

	return m.helmClient.UninstallChart(ctx, "eg", opts.EnvoyGatewayNamespace, opts.Timeout)
}

// removeAIGatewayConfig removes AI Gateway configuration.
func (m *Manager) removeAIGatewayConfig(ctx context.Context, opts InstallOptions) error {
	if m.dryRun {
		m.output.Info("DRY RUN: Would remove AI Gateway configuration")
		return nil
	}

	// This would remove the configuration files applied to Envoy Gateway
	// Implementation depends on how configuration is stored
	return nil
}

// cleanupResources cleans up remaining resources.
func (m *Manager) cleanupResources(ctx context.Context, opts InstallOptions) error {
	if m.dryRun {
		m.output.Info("DRY RUN: Would clean up resources")
		return nil
	}

	// Clean up namespaces if they were created by us
	// This is optional and should be done carefully
	return nil
}
