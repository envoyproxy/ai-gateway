// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// BuildExtProcOnDemand builds the extproc binary unless EXTPROC_BIN is set.
// If EXTPROC_BIN environment variable is set, it will use that path instead.
func BuildExtProcOnDemand() (string, error) {
	if envPath := os.Getenv("EXTPROC_BIN"); envPath != "" {
		if !filepath.IsAbs(envPath) {
			envPath = filepath.Join(findProjectRoot(), envPath)
		}
		if _, err := os.Stat(envPath); err != nil {
			return "", fmt.Errorf("EXTPROC_BIN path does not exist: %s", envPath)
		}
		fmt.Fprintf(os.Stderr, "Using EXTPROC_BIN: %s\n", envPath)
		return envPath, nil
	}

	// Always rebuild to ensure tests run against current code.
	return buildExtProc()
}

// buildExtProc builds the extproc binary using the same logic as the Makefile.
func buildExtProc() (string, error) {
	return buildGoBinary("extproc", "./cmd/extproc")
}

// buildGoBinary builds a Go binary with the given name and package path.
func buildGoBinary(binaryNamePrefix, packagePath string) (string, error) {
	projectRoot := findProjectRoot()
	outputDir := filepath.Join(projectRoot, "out")

	// Create output directory.
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	// Build binary.
	binaryName := fmt.Sprintf("%s-%s-%s", binaryNamePrefix, runtime.GOOS, runtime.GOARCH)
	binaryPath := filepath.Join(outputDir, binaryName)

	cmd := exec.Command("go", "build", "-o", binaryPath, packagePath)
	cmd.Dir = projectRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stderr strings.Builder
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to build %s: %w\nstderr: %s", binaryNamePrefix, err, stderr.String())
	}

	// Make executable.
	if err := os.Chmod(binaryPath, 0o755); err != nil {
		return "", fmt.Errorf("failed to make binary executable: %w", err)
	}
	return binaryPath, nil
}

// findProjectRoot finds the root of the project by looking for go.mod.
func findProjectRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			panic("could not find project root (go.mod)")
		}
		dir = parent
	}
}
