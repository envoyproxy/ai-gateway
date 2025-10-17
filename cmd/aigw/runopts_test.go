// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/xdg"
)

func newTempDirectories(t *testing.T) *xdg.Directories {
	return &xdg.Directories{
		ConfigHome: t.TempDir(),
		DataHome:   t.TempDir(),
		StateHome:  t.TempDir(),
		RuntimeDir: t.TempDir(),
	}
}

func TestNewRunOpts(t *testing.T) {
	mockLauncher := func(_ context.Context, _ []string, _ io.Writer) error { return nil }

	t.Run("sets all fields correctly", func(t *testing.T) {
		dirs := newTempDirectories(t)
		runID := "test-run-123"
		configPath := "/explicit/config.yaml"

		actual, err := newRunOpts(dirs, runID, configPath, mockLauncher)
		require.NoError(t, err)
		require.NotNil(t, actual)

		require.Equal(t, runID, actual.runID)
		require.NotNil(t, actual.extProcLauncher)
		require.Equal(t, configPath, actual.configPath)

		expectedRunDir := filepath.Join(dirs.StateHome, "runs", runID)
		paths := []struct {
			name     string
			expected string
			actual   string
		}{
			{"logPath", filepath.Join(expectedRunDir, "aigw.log"), actual.logPath},
			{"egConfigPath", filepath.Join(expectedRunDir, "envoy-gateway-config.yaml"), actual.egConfigPath},
			{"egResourcesPath", filepath.Join(expectedRunDir, "envoy-ai-gateway-resources", "config.yaml"), actual.egResourcesPath},
			{"extprocConfigPath", filepath.Join(expectedRunDir, "extproc-config.yaml"), actual.extprocConfigPath},
			{"extprocUDSPath", filepath.Join(dirs.RuntimeDir, runID, "uds.sock"), actual.extprocUDSPath},
		}

		for _, p := range paths {
			require.Equal(t, p.expected, p.actual, p.name)
			require.True(t, filepath.IsAbs(p.actual), p.name)
		}

		require.DirExists(t, expectedRunDir)
		require.DirExists(t, filepath.Dir(actual.egResourcesPath))
		require.DirExists(t, filepath.Dir(actual.extprocUDSPath))
	})

	t.Run("empty configPath remains empty", func(t *testing.T) {
		dirs := newTempDirectories(t)

		actual, err := newRunOpts(dirs, "test-run", "", mockLauncher)
		require.NoError(t, err)
		require.Empty(t, actual.configPath)
	})
}

func TestNewRunOpts_Permissions(t *testing.T) {
	runID := "test-run-permissions"

	dirs := newTempDirectories(t)

	actual, err := newRunOpts(dirs, runID, "", nil)
	require.NoError(t, err)

	// Verify runDir created with correct permissions
	expectedRunDir := filepath.Join(dirs.StateHome, "runs", runID)
	info, err := os.Stat(expectedRunDir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.Equal(t, os.FileMode(0o750), info.Mode().Perm())

	// Verify egResourcesPath parent created with correct permissions
	expectedResourcesDir := filepath.Dir(actual.egResourcesPath)
	info, err = os.Stat(expectedResourcesDir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.Equal(t, os.FileMode(0o750), info.Mode().Perm())

	// Verify RuntimeDir/{runID} created with correct permissions
	expectedRuntimeRunDir := filepath.Join(dirs.RuntimeDir, runID)
	info, err = os.Stat(expectedRuntimeRunDir)
	require.NoError(t, err)
	require.True(t, info.IsDir())
	require.Equal(t, os.FileMode(0o700), info.Mode().Perm())
}

func TestNewRunOpts_DirectoryContents(t *testing.T) {
	runID := "test-run-empty"

	dirs := newTempDirectories(t)

	actual, err := newRunOpts(dirs, runID, "", nil)
	require.NoError(t, err)

	// Verify runDir contains only expected entries
	expectedRunDir := filepath.Join(dirs.StateHome, "runs", runID)
	actualEntries, err := os.ReadDir(expectedRunDir)
	require.NoError(t, err)
	require.Len(t, actualEntries, 1)
	require.Equal(t, "envoy-ai-gateway-resources", actualEntries[0].Name())

	// Verify resourcesDir is empty
	expectedResourcesDir := filepath.Dir(actual.egResourcesPath)
	actualEntries, err = os.ReadDir(expectedResourcesDir)
	require.NoError(t, err)
	require.Empty(t, actualEntries)

	// Verify runtimeRunDir is empty
	expectedRuntimeRunDir := filepath.Join(dirs.RuntimeDir, runID)
	actualEntries, err = os.ReadDir(expectedRuntimeRunDir)
	require.NoError(t, err)
	require.Empty(t, actualEntries)
}

func TestNewRunOpts_Errors(t *testing.T) {
	t.Run("error when runDir creation fails", func(t *testing.T) {
		baseDir := t.TempDir()
		stateHome := filepath.Join(baseDir, "nonexistent", "readonly")

		// Make the parent read-only
		parent := filepath.Dir(stateHome)
		err := os.MkdirAll(parent, 0o755)
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = os.Chmod(parent, 0o755)
		})
		err = os.Chmod(parent, 0o555)
		require.NoError(t, err)

		dirs := newTempDirectories(t)
		dirs.StateHome = stateHome

		_, err = newRunOpts(dirs, "test-run", "", nil)
		require.Error(t, err)
	})

	t.Run("error when resources directory creation fails", func(t *testing.T) {
		stateHome := t.TempDir()

		// Pre-create runDir successfully
		runDir := filepath.Join(stateHome, "runs", "test-run-fail-resources")
		err := os.MkdirAll(runDir, 0o750)
		require.NoError(t, err)

		// Create a file where resources directory should be
		resourcesParent := filepath.Join(runDir, "envoy-ai-gateway-resources")
		err = os.WriteFile(resourcesParent, []byte("block"), 0o600)
		require.NoError(t, err)

		// Make runDir read-only so RemoveAll fails
		err = os.Chmod(runDir, 0o555)
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = os.Chmod(runDir, 0o755)
		})

		dirs := newTempDirectories(t)
		dirs.StateHome = stateHome

		_, err = newRunOpts(dirs, "test-run-fail-resources", "", nil)
		require.Error(t, err)
	})

	t.Run("error when runtime directory creation fails", func(t *testing.T) {
		baseDir := t.TempDir()
		runtimeDir := filepath.Join(baseDir, "nonexistent", "readonly")

		// Make the parent read-only
		parent := filepath.Dir(runtimeDir)
		err := os.MkdirAll(parent, 0o755)
		require.NoError(t, err)
		t.Cleanup(func() {
			_ = os.Chmod(parent, 0o755)
		})
		err = os.Chmod(parent, 0o555)
		require.NoError(t, err)

		dirs := newTempDirectories(t)
		dirs.RuntimeDir = runtimeDir

		_, err = newRunOpts(dirs, "test-run", "", nil)
		require.Error(t, err)
	})
}
