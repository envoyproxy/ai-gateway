// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package k8s

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chart/loader"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/registry"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/client-go/rest"
)

// HelmClient wraps Helm functionality.
type HelmClient struct {
	settings   *cli.EnvSettings
	restConfig *rest.Config
}

// NewHelmClient creates a new Helm client.
func NewHelmClient(restConfig *rest.Config) *HelmClient {
	settings := cli.New()
	return &HelmClient{
		settings:   settings,
		restConfig: restConfig,
	}
}

// InstallOrUpgradeChart installs or upgrades a Helm chart.
func (h *HelmClient) InstallOrUpgradeChart(ctx context.Context, opts ChartOptions) error {
	actionConfig := &action.Configuration{}
	if err := actionConfig.Init(h.settings.RESTClientGetter(), opts.Namespace, os.Getenv("HELM_DRIVER"), func(_ string, _ ...interface{}) {}); err != nil {
		return fmt.Errorf("failed to initialize helm action config: %w", err)
	}

	// Check if release exists
	histClient := action.NewHistory(actionConfig)
	histClient.Max = 1
	_, err := histClient.Run(opts.ReleaseName)
	if err != nil {
		// Release doesn't exist, install it
		return h.installChart(ctx, actionConfig, opts)
	}
	// Release exists, upgrade it
	return h.upgradeChart(ctx, actionConfig, opts)
}

// UninstallChart uninstalls a Helm chart.
func (h *HelmClient) UninstallChart(_ context.Context, releaseName, namespace string, timeout time.Duration) error {
	actionConfig := &action.Configuration{}
	if err := actionConfig.Init(h.settings.RESTClientGetter(), namespace, os.Getenv("HELM_DRIVER"), func(_ string, _ ...interface{}) {}); err != nil {
		return fmt.Errorf("failed to initialize helm action config: %w", err)
	}

	uninstall := action.NewUninstall(actionConfig)
	uninstall.Wait = true
	uninstall.Timeout = timeout

	_, err := uninstall.Run(releaseName)
	if err != nil {
		return fmt.Errorf("failed to uninstall chart %s: %w", releaseName, err)
	}

	return nil
}

// installChart installs a new Helm chart.
func (h *HelmClient) installChart(ctx context.Context, actionConfig *action.Configuration, opts ChartOptions) error {
	install := action.NewInstall(actionConfig)
	install.ReleaseName = opts.ReleaseName
	install.Namespace = opts.Namespace
	install.CreateNamespace = opts.CreateNamespace
	install.Wait = opts.Wait
	install.Timeout = opts.Timeout
	install.SkipCRDs = opts.SkipCRDs

	// Load chart
	chart, err := h.loadChart(opts.ChartRef, opts.Version)
	if err != nil {
		return fmt.Errorf("failed to load chart: %w", err)
	}

	_, err = install.RunWithContext(ctx, chart, opts.Values)
	if err != nil {
		return fmt.Errorf("failed to install chart %s: %w", opts.ChartRef, err)
	}

	return nil
}

// upgradeChart upgrades an existing Helm chart.
func (h *HelmClient) upgradeChart(ctx context.Context, actionConfig *action.Configuration, opts ChartOptions) error {
	upgrade := action.NewUpgrade(actionConfig)
	upgrade.Namespace = opts.Namespace
	upgrade.Wait = opts.Wait
	upgrade.Timeout = opts.Timeout
	upgrade.SkipCRDs = opts.SkipCRDs

	// Load chart
	chart, err := h.loadChart(opts.ChartRef, opts.Version)
	if err != nil {
		return fmt.Errorf("failed to load chart: %w", err)
	}

	_, err = upgrade.RunWithContext(ctx, opts.ReleaseName, chart, opts.Values)
	if err != nil {
		return fmt.Errorf("failed to upgrade chart %s: %w", opts.ChartRef, err)
	}

	return nil
}

// loadChart loads a chart from various sources.
func (h *HelmClient) loadChart(chartRef, version string) (*chart.Chart, error) {
	// Handle OCI registry charts
	if registry.IsOCI(chartRef) {
		return h.loadOCIChart(chartRef, version)
	}

	// Handle local charts
	if _, err := os.Stat(chartRef); err == nil {
		return loader.Load(chartRef)
	}

	// Handle repository charts
	return h.loadRepoChart(chartRef, version)
}

// loadOCIChart loads a chart from an OCI registry.
func (h *HelmClient) loadOCIChart(chartRef, version string) (*chart.Chart, error) {
	// Create a temporary directory for chart download
	tempDir, err := os.MkdirTemp("", "helm-chart-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)

	// Use helm pull to download the OCI chart
	ref := fmt.Sprintf("%s:%s", chartRef, version)
	pullCmd := exec.Command("helm", "pull", ref, "--untar", "--untardir", tempDir)

	output, err := pullCmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("failed to pull OCI chart %s: %w, output: %s", ref, err, string(output))
	}

	// Find the extracted chart directory
	entries, err := os.ReadDir(tempDir)
	if err != nil {
		return nil, fmt.Errorf("failed to read temp directory: %w", err)
	}

	var chartDir string
	for _, entry := range entries {
		if entry.IsDir() {
			chartDir = filepath.Join(tempDir, entry.Name())
			break
		}
	}

	if chartDir == "" {
		return nil, fmt.Errorf("no chart directory found after extraction")
	}

	// Load the chart from the extracted directory
	return loader.Load(chartDir)
}

// loadRepoChart loads a chart from a repository.
func (h *HelmClient) loadRepoChart(_, _ string) (*chart.Chart, error) {
	// This is a simplified implementation
	// In a real implementation, you would need to handle repository management
	return nil, fmt.Errorf("repository charts not implemented yet")
}

// ChartOptions contains options for chart operations.
type ChartOptions struct {
	ChartRef        string
	ReleaseName     string
	Namespace       string
	Version         string
	Values          map[string]interface{}
	Wait            bool
	Timeout         time.Duration
	CreateNamespace bool
	SkipCRDs        bool
}

// GetReleaseStatus gets the status of a Helm release.
func (h *HelmClient) GetReleaseStatus(_ context.Context, releaseName, namespace string) (string, error) {
	actionConfig := &action.Configuration{}
	if err := actionConfig.Init(h.settings.RESTClientGetter(), namespace, os.Getenv("HELM_DRIVER"), func(_ string, _ ...interface{}) {}); err != nil {
		return "", fmt.Errorf("failed to initialize helm action config: %w", err)
	}

	status := action.NewStatus(actionConfig)
	release, err := status.Run(releaseName)
	if err != nil {
		return "", fmt.Errorf("failed to get release status: %w", err)
	}

	return release.Info.Status.String(), nil
}

// ListReleases lists all Helm releases in a namespace.
func (h *HelmClient) ListReleases(_ context.Context, namespace string) ([]*release.Release, error) {
	actionConfig := &action.Configuration{}
	if err := actionConfig.Init(h.settings.RESTClientGetter(), namespace, os.Getenv("HELM_DRIVER"), func(_ string, _ ...interface{}) {}); err != nil {
		return nil, fmt.Errorf("failed to initialize helm action config: %w", err)
	}

	list := action.NewList(actionConfig)
	list.All = true

	releases, err := list.Run()
	if err != nil {
		return nil, fmt.Errorf("failed to list releases: %w", err)
	}

	return releases, nil
}
