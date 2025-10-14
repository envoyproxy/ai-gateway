// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/envoyproxy/ai-gateway/internal/xdg"
)

// runOpts are the options for the run command.
type runOpts struct {
	xdg.Directories
	// runID is the unique identifier for this run
	runID string
	// extProcLauncher is the function used to launch the external processor.
	extProcLauncher func(ctx context.Context, args []string, w io.Writer) error

	// Computed paths derived from Directories and runID
	// configPath is the resolved aigw config file path. Either --path flag, {ConfigHome}/config.yaml if exists, or empty.
	// Empty means auto-generate from OPENAI_API_KEY/AZURE_OPENAI_API_KEY environment variables.
	configPath string
	// logPath is {StateHome}/runs/{runID}/aigw.log
	// Contains: aigw debug/info/error logs
	logPath string
	// egConfigPath is {StateHome}/runs/{runID}/envoy-gateway-config.yaml
	// Contains: generated Envoy Gateway config that references egResourcesPath directory
	// Passed to: Envoy Gateway via `envoy-gateway server --config-path <egConfigPath>`
	egConfigPath string
	// egResourcesPath is {StateHome}/runs/{runID}/envoy-ai-gateway-resources/config.yaml
	// Contains: Gateway, HTTPRoute, HTTPRouteFilter, Backend, Secret, BackendTrafficPolicy, SecurityPolicy, EnvoyExtensionPolicy objects
	// Derived from: translating configPath (aigw resources -> Envoy Gateway resources)
	// Referenced by: egConfigPath (tells Envoy Gateway where to load resources from the parent directory)
	// Note: Must be in a subdirectory (not a flat file) because Envoy Gateway config template requires a directory path
	egResourcesPath string
	// extprocUDSPath is {RuntimeDir}/{runID}/uds.sock
	// Unix domain socket for Envoy <-> aigw extproc communication
	extprocUDSPath string
	// extprocConfigPath is {StateHome}/runs/{runID}/extproc-config.yaml
	// Contains: filterapi.Config YAML for external processor
	// Derived from: translating configPath (extracts filter config from aigw resources)
	extprocConfigPath string
}

// newRunOpts creates runOpts with all paths computed and creates directories
// that aigw writes to directly (e.g. not ones owned by func-e or Envoy
// Gateway). Note: configPath may be empty (will auto-generate from env vars).
func newRunOpts(dirs *xdg.Directories, runID, configPath string, extProcLauncher func(context.Context, []string, io.Writer) error) (*runOpts, error) {
	opts := &runOpts{
		Directories:     *dirs,
		runID:           runID,
		configPath:      configPath,
		extProcLauncher: extProcLauncher,
	}

	// Compute all paths
	runDir := filepath.Join(dirs.StateHome, "runs", runID)
	opts.logPath = filepath.Join(runDir, "aigw.log")
	opts.egConfigPath = filepath.Join(runDir, "envoy-gateway-config.yaml")
	opts.egResourcesPath = filepath.Join(runDir, "envoy-ai-gateway-resources", "config.yaml")
	opts.extprocConfigPath = filepath.Join(runDir, "extproc-config.yaml")
	opts.extprocUDSPath = filepath.Join(dirs.RuntimeDir, runID, "uds.sock")

	// Create directories that aigw writes to
	// runDir: for log, config, extproc-config (0o750 per XDG spec for StateHome)
	if err := os.MkdirAll(runDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create run directory %s: %w", runDir, err)
	}

	// Recreate runDir/envoy-ai-gateway-resources: for egResourcesPath (0o750)
	// Remove if exists to ensure a clean state, then create
	resourcesDir := filepath.Dir(opts.egResourcesPath)
	if err := os.RemoveAll(resourcesDir); err != nil {
		return nil, fmt.Errorf("failed to remove resources directory %s: %w", resourcesDir, err)
	}
	if err := os.MkdirAll(resourcesDir, 0o750); err != nil {
		return nil, fmt.Errorf("failed to create resources directory %s: %w", resourcesDir, err)
	}

	// RuntimeDir/{runID}: for UDS socket (0o700 per XDG spec for RuntimeDir)
	// Remove UDS socket if exists to ensure a clean state
	if err := os.MkdirAll(filepath.Dir(opts.extprocUDSPath), 0o700); err != nil {
		return nil, fmt.Errorf("failed to create runtime directory %s: %w", filepath.Dir(opts.extprocUDSPath), err)
	}
	_ = os.Remove(opts.extprocUDSPath)

	return opts, nil
}
