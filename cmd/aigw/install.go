// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/envoyproxy/ai-gateway/cmd/aigw/internal/installer"
	"github.com/envoyproxy/ai-gateway/cmd/aigw/internal/ui"
)

// cmdInstall corresponds to `aigw install` command.
type cmdInstall struct {
	// Envoy Gateway options
	EnvoyGatewayNamespace string `help:"Envoy Gateway installation namespace" default:"envoy-gateway-system"`
	EnvoyGatewayVersion   string `help:"Envoy Gateway chart version" default:"v0.0.0-latest"`

	// AI Gateway options
	Namespace string `help:"AI Gateway installation namespace" default:"envoy-ai-gateway-system"`
	Version   string `help:"AI Gateway chart version" default:"v0.0.0-latest"`
	SkipCRDs  bool   `help:"Skip AI Gateway CRDs installation"`

	// Common options
	DryRun      bool          `help:"Show what would be done without executing"`
	Force       bool          `help:"Skip confirmation prompts"`
	Interactive bool          `help:"Enable step-by-step confirmation"`
	Debug       bool          `help:"Enable debug logging"`
	Timeout     time.Duration `help:"Timeout for installation operations" default:"5m"`
}

// install executes the install command.
func install(ctx context.Context, cmd cmdInstall, stdout, _ io.Writer) error {
	output := ui.NewOutput(stdout, cmd.Debug)

	// Print banner
	output.Banner("Envoy AI Gateway Installation")

	if cmd.DryRun {
		output.Info("Running in DRY RUN mode - no changes will be made")
		output.EmptyLine()
	}

	// Show installation options
	output.Subheader("Envoy Gateway Installation Options")
	envoyOptions := []string{
		fmt.Sprintf("Namespace: %s", cmd.EnvoyGatewayNamespace),
		fmt.Sprintf("Chart Version: %s", cmd.EnvoyGatewayVersion),
	}
	output.List(envoyOptions)
	output.EmptyLine()

	output.Subheader("Envoy AI Gateway Installation Options")
	aiOptions := []string{
		fmt.Sprintf("Namespace: %s", cmd.Namespace),
		fmt.Sprintf("Chart Version: %s", cmd.Version),
		fmt.Sprintf("Skip CRDs: %t", cmd.SkipCRDs),
		fmt.Sprintf("Timeout: %s", cmd.Timeout),
	}
	output.List(aiOptions)
	output.EmptyLine()

	// Confirmation prompt (unless force is specified or dry run)
	if !cmd.Force && !cmd.DryRun {
		if !output.ConfirmPrompt("Do you want to proceed with the installation?") {
			output.Info("Installation cancelled by user")
			return nil
		}
		output.EmptyLine()
	}

	// Create installation manager
	manager, err := installer.NewManager(output, cmd.DryRun)
	if err != nil {
		return fmt.Errorf("failed to create installation manager: %w", err)
	}

	// Interactive step confirmation
	if cmd.Interactive && !cmd.Force && !cmd.DryRun {
		if !confirmInstallationSteps(output, cmd) {
			output.Info("Installation cancelled by user")
			return nil
		}
	}

	// Prepare installation options
	installOpts := installer.InstallOptions{
		EnvoyGatewayNamespace: cmd.EnvoyGatewayNamespace,
		EnvoyGatewayVersion:   cmd.EnvoyGatewayVersion,
		Namespace:             cmd.Namespace,
		Version:               cmd.Version,
		SkipCRDs:              cmd.SkipCRDs,
		Timeout:               cmd.Timeout,
		CreateNamespace:       true,
	}

	// Execute installation
	err = manager.Install(ctx, installOpts)
	if err != nil {
		output.Error(fmt.Sprintf("Installation failed: %v", err))
		output.EmptyLine()
		output.Subheader("Troubleshooting")
		output.List([]string{
			"Check that your kubeconfig is properly configured",
			"Ensure you have sufficient permissions in the cluster",
			"Verify that the cluster meets the minimum version requirements (1.29+)",
			"Check the cluster has sufficient resources available",
			"Run with --debug flag for more detailed output",
		})
		return err
	}

	// Show success message and next steps
	output.EmptyLine()
	output.Success("Installation completed successfully!")
	output.EmptyLine()

	output.Subheader("Next Steps")
	nextSteps := []string{
		"Verify the installation: kubectl get pods -n " + cmd.Namespace,
		"Check Envoy Gateway: kubectl get pods -n " + cmd.EnvoyGatewayNamespace,
		"View the documentation: https://aigateway.envoyproxy.io/docs/latest/getting-started/basic-usage",
		"Connect providers: https://aigateway.envoyproxy.io/docs/latest/getting-started/connect-providers",
	}
	output.NumberedList(nextSteps)

	output.EmptyLine()
	output.Info("Happy AI Gateway-ing! 🚀")

	return nil
}

// showInstallationStatus shows the current installation status.
//
//nolint:unused
func showInstallationStatus(_ context.Context, output *ui.Output, _ string) error {
	output.Subheader("Installation Status")

	// This would check the current status of the installation
	// For now, we'll just show a placeholder
	output.Info("Checking installation status...")

	// TODO: Implement status checking
	output.Warning("Status checking not implemented yet")

	return nil
}

// validateInstallOptions validates the installation options.
//
//nolint:unused
func validateInstallOptions(cmd cmdInstall) error {
	// Validate namespace name
	if cmd.Namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}

	// Validate version format
	if cmd.Version == "" {
		return fmt.Errorf("version cannot be empty")
	}

	// Validate timeout
	if cmd.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	return nil
}

// showPreInstallationInfo shows information before installation.
//
//nolint:unused
func showPreInstallationInfo(output *ui.Output, cmd cmdInstall) {
	output.Subheader("Pre-Installation Information")

	info := []string{
		"This will install Envoy AI Gateway and its dependencies",
		"Envoy Gateway will be installed in the 'envoy-gateway-system' namespace",
		fmt.Sprintf("AI Gateway will be installed in the '%s' namespace", cmd.Namespace),
		"Configuration will be applied to integrate the components",
	}

	if cmd.SkipCRDs {
		info = append(info, "CRDs installation will be skipped (ensure they are already installed)")
	} else {
		info = append(info, "CRDs will be installed automatically")
	}

	output.List(info)
	output.EmptyLine()

	output.Subheader("Prerequisites")
	prereqs := []string{
		"kubectl command-line tool",
		"helm package manager",
		"Kubernetes cluster version 1.29 or higher",
		"Sufficient cluster permissions (cluster-admin recommended)",
	}
	output.List(prereqs)
	output.EmptyLine()
}

// handleInstallationError handles installation errors and provides guidance.
//
//nolint:unused
func handleInstallationError(output *ui.Output, err error) {
	output.Error(fmt.Sprintf("Installation failed: %v", err))
	output.EmptyLine()

	output.Subheader("Common Issues and Solutions")
	solutions := []string{
		"Permission denied: Ensure you have cluster-admin permissions",
		"Connection refused: Check your kubeconfig and cluster connectivity",
		"Version mismatch: Verify your Kubernetes cluster version is 1.29+",
		"Resource conflicts: Check if components are already installed",
		"Timeout errors: Increase timeout with --timeout flag",
	}
	output.List(solutions)
	output.EmptyLine()

	output.Info("For more help, visit: https://aigateway.envoyproxy.io/docs/latest/getting-started/installation")
}

// confirmInstallationSteps prompts the user to confirm each installation step.
func confirmInstallationSteps(output *ui.Output, cmd cmdInstall) bool {
	output.EmptyLine()
	output.Header("🔍 Installation Step Confirmation")
	output.Info("You have enabled interactive mode. You will be asked to confirm each major installation step.")
	output.EmptyLine()

	// Step 1: Prerequisites check
	if !output.ConfirmStep("Prerequisites Check",
		"This will verify that kubectl and helm are available, check Kubernetes cluster connectivity, and validate cluster version requirements.",
		true) {
		return false
	}

	// Step 2: Envoy Gateway installation
	envoyDesc := fmt.Sprintf("This will install Envoy Gateway %s in the '%s' namespace. Envoy Gateway is required for AI Gateway to function properly.",
		cmd.EnvoyGatewayVersion, cmd.EnvoyGatewayNamespace)
	if !output.ConfirmStep("Envoy Gateway Installation", envoyDesc, true) {
		return false
	}

	// Step 3: AI Gateway installation
	aiDesc := fmt.Sprintf("This will install AI Gateway %s in the '%s' namespace. This includes the AI Gateway controller and CRDs.",
		cmd.Version, cmd.Namespace)
	if !output.ConfirmStep("AI Gateway Installation", aiDesc, true) {
		return false
	}

	// Step 4: Configuration
	if !output.ConfirmStep("Envoy Gateway Configuration",
		"This will apply AI Gateway-specific configuration to Envoy Gateway, including Redis setup and RBAC policies.",
		true) {
		return false
	}

	// Step 5: Verification
	if !output.ConfirmStep("Installation Verification",
		"This will verify that both Envoy Gateway and AI Gateway are running correctly and ready to handle requests.",
		true) {
		return false
	}

	output.EmptyLine()
	output.Success("All installation steps confirmed. Proceeding with installation...")
	return true
}
