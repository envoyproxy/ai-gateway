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

// cmdUninstall corresponds to `aigw uninstall` command.
type cmdUninstall struct {
	// Envoy Gateway options
	EnvoyGatewayNamespace string `help:"Envoy Gateway installation namespace" default:"envoy-gateway-system"`

	// AI Gateway options
	Namespace string `help:"AI Gateway installation namespace" default:"envoy-ai-gateway-system"`
	KeepCRDs  bool   `help:"Keep AI Gateway CRDs after uninstallation"`

	// Common options
	Force   bool          `help:"Skip confirmation prompts"`
	Debug   bool          `help:"Enable debug logging"`
	DryRun  bool          `help:"Show what would be done without executing"`
	Timeout time.Duration `help:"Timeout for uninstallation operations" default:"5m"`
}

// uninstall executes the uninstall command.
func uninstall(ctx context.Context, cmd cmdUninstall, stdout, _ io.Writer) error {
	output := ui.NewOutput(stdout, cmd.Debug)

	// Print banner
	output.Banner("Envoy AI Gateway Uninstallation")

	if cmd.DryRun {
		output.Info("Running in DRY RUN mode - no changes will be made")
		output.EmptyLine()
	}

	// Show what will be removed
	output.Subheader("Components to be removed")
	components := []string{
		"AI Gateway controller and CRDs",
		"Envoy Gateway and its components",
		"Configuration applied to Envoy Gateway",
		"Associated namespaces (if empty)",
	}

	if cmd.KeepCRDs {
		components[0] = "AI Gateway controller (CRDs will be kept)"
	}

	output.List(components)
	output.EmptyLine()

	// Show uninstallation options
	output.Subheader("Envoy Gateway Uninstallation Options")
	envoyOptions := []string{
		fmt.Sprintf("Namespace: %s", cmd.EnvoyGatewayNamespace),
	}
	output.List(envoyOptions)
	output.EmptyLine()

	output.Subheader("Envoy AI Gateway Uninstallation Options")
	aiOptions := []string{
		fmt.Sprintf("Namespace: %s", cmd.Namespace),
		fmt.Sprintf("Keep CRDs: %t", cmd.KeepCRDs),
		fmt.Sprintf("Timeout: %s", cmd.Timeout),
	}
	output.List(aiOptions)
	output.EmptyLine()

	// Warning message
	output.Warning("This operation will remove all AI Gateway components and cannot be undone!")
	output.EmptyLine()

	// Confirmation prompt (unless force is specified or dry run)
	if !cmd.Force && !cmd.DryRun {
		if !output.ConfirmPrompt("Are you sure you want to proceed with the uninstallation?") {
			output.Info("Uninstallation cancelled by user")
			return nil
		}
		output.EmptyLine()

		// Double confirmation for production-like environments
		if !output.ConfirmPrompt("This will permanently remove all AI Gateway components. Continue?") {
			output.Info("Uninstallation cancelled by user")
			return nil
		}
		output.EmptyLine()
	}

	// Create installation manager
	manager, err := installer.NewManager(output, cmd.DryRun)
	if err != nil {
		return fmt.Errorf("failed to create installation manager: %w", err)
	}

	// Prepare uninstallation options
	uninstallOpts := installer.InstallOptions{
		EnvoyGatewayNamespace: cmd.EnvoyGatewayNamespace,
		Namespace:             cmd.Namespace,
		SkipCRDs:              cmd.KeepCRDs,
		Timeout:               cmd.Timeout,
		CreateNamespace:       false,
	}

	// Execute uninstallation
	err = manager.Uninstall(ctx, uninstallOpts)
	if err != nil {
		output.Error(fmt.Sprintf("Uninstallation encountered errors: %v", err))
		output.EmptyLine()
		output.Warning("Some components may not have been removed completely")
		output.Subheader("Manual Cleanup")
		output.List([]string{
			"Check for remaining resources: kubectl get all -n " + cmd.Namespace,
			"Check for remaining CRDs: kubectl get crd | grep ai-gateway",
			"Remove namespace manually if needed: kubectl delete namespace " + cmd.Namespace,
			"Check Envoy Gateway: kubectl get all -n envoy-gateway-system",
		})
		return err
	}

	// Show success message
	output.EmptyLine()
	output.Success("Uninstallation completed successfully!")
	output.EmptyLine()

	// Show post-uninstallation information
	output.Subheader("Post-Uninstallation")
	postInfo := []string{
		"All AI Gateway components have been removed",
		"Envoy Gateway has been uninstalled",
		"Configuration changes have been reverted",
	}

	if cmd.KeepCRDs {
		postInfo = append(postInfo, "CRDs have been preserved as requested")
	} else {
		postInfo = append(postInfo, "CRDs have been removed")
	}

	output.List(postInfo)
	output.EmptyLine()

	// Verification steps
	output.Subheader("Verification")
	verifySteps := []string{
		"Check no pods remain: kubectl get pods -n " + cmd.Namespace,
		"Verify namespace removal: kubectl get namespace " + cmd.Namespace,
		"Check Envoy Gateway removal: kubectl get pods -n " + cmd.EnvoyGatewayNamespace,
	}
	output.NumberedList(verifySteps)

	output.EmptyLine()
	output.Info("Thank you for using Envoy AI Gateway! 👋")

	return nil
}

// showUninstallationStatus shows the current status before uninstallation.
//
//nolint:unused
func showUninstallationStatus(_ context.Context, output *ui.Output, _ string) error {
	output.Subheader("Current Installation Status")

	// This would check what's currently installed
	// For now, we'll just show a placeholder
	output.Info("Checking current installation...")

	// TODO: Implement status checking
	output.Warning("Status checking not implemented yet")

	return nil
}

// validateUninstallOptions validates the uninstallation options.
//
//nolint:unused
func validateUninstallOptions(cmd cmdUninstall) error {
	// Validate namespace name
	if cmd.Namespace == "" {
		return fmt.Errorf("namespace cannot be empty")
	}

	// Validate timeout
	if cmd.Timeout <= 0 {
		return fmt.Errorf("timeout must be positive")
	}

	return nil
}

// showPreUninstallationWarning shows warnings before uninstallation.
//
//nolint:unused
func showPreUninstallationWarning(output *ui.Output, _ cmdUninstall) {
	output.Subheader("⚠️  Important Warnings")

	warnings := []string{
		"This operation is irreversible",
		"All AI Gateway configurations will be lost",
		"Any running AI workloads will be affected",
		"Custom resources and data may be removed",
		"Backup any important configurations before proceeding",
	}

	output.List(warnings)
	output.EmptyLine()

	output.Subheader("What will be removed")
	removals := []string{
		"AI Gateway controller deployment",
		"AI Gateway custom resource definitions (unless --keep-crds is specified)",
		"Envoy Gateway installation",
		"Associated service accounts and RBAC",
		"Configuration applied to Envoy Gateway",
	}
	output.List(removals)
	output.EmptyLine()
}

// handleUninstallationError handles uninstallation errors and provides guidance.
//
//nolint:unused
func handleUninstallationError(output *ui.Output, err error) {
	output.Error(fmt.Sprintf("Uninstallation failed: %v", err))
	output.EmptyLine()

	output.Subheader("Manual Cleanup Steps")
	cleanupSteps := []string{
		"Remove AI Gateway resources: kubectl delete all -l app.kubernetes.io/name=ai-gateway -n envoy-ai-gateway-system",
		"Remove CRDs: kubectl delete crd -l app.kubernetes.io/name=ai-gateway",
		"Remove Envoy Gateway: helm uninstall eg -n envoy-gateway-system",
		"Remove namespaces: kubectl delete namespace envoy-ai-gateway-system envoy-gateway-system",
		"Check for remaining resources: kubectl get all --all-namespaces | grep -E '(ai-gateway|envoy-gateway)'",
	}
	output.NumberedList(cleanupSteps)
	output.EmptyLine()

	output.Info("For more help, visit: https://aigateway.envoyproxy.io/docs/latest/getting-started/installation")
}
