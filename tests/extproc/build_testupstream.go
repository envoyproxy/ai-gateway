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

// buildTestUpstreamOnDemand builds the testupstream binary unless TESTUPSTREAM_BIN is set.
// If TESTUPSTREAM_BIN environment variable is set, it will use that path instead.
func buildTestUpstreamOnDemand() (string, error) {
	if envPath := os.Getenv("TESTUPSTREAM_BIN"); envPath != "" {
		if !filepath.IsAbs(envPath) {
			envPath = filepath.Join(findProjectRoot(), envPath)
		}
		if _, err := os.Stat(envPath); err != nil {
			return "", fmt.Errorf("TESTUPSTREAM_BIN path does not exist: %s", envPath)
		}
		fmt.Fprintf(os.Stderr, "Using TESTUPSTREAM_BIN: %s\n", envPath)
		return envPath, nil
	}

	// Always rebuild to ensure tests run against current code.
	return buildTestUpstream()
}

// buildTestUpstream builds the testupstream binary using the same logic as the Makefile.
func buildTestUpstream() (string, error) {
	projectRoot := findProjectRoot()
	outputDir := filepath.Join(projectRoot, "out")

	// Create output directory.
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create output directory: %w", err)
	}

	// Build binary.
	binaryName := fmt.Sprintf("testupstream-%s-%s", runtime.GOOS, runtime.GOARCH)
	binaryPath := filepath.Join(outputDir, binaryName)

	cmd := exec.Command("go", "build", "-o", binaryPath, "./tests/internal/testupstreamlib/testupstream")
	cmd.Dir = projectRoot
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	var stderr strings.Builder
	cmd.Stdout = io.Discard
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("failed to build testupstream: %w\nstderr: %s", err, stderr.String())
	}

	// Make executable.
	if err := os.Chmod(binaryPath, 0o755); err != nil {
		return "", fmt.Errorf("failed to make binary executable: %w", err)
	}
	return binaryPath, nil
}
