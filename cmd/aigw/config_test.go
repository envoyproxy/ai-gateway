// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReadConfig(t *testing.T) {
	aiGatewayLocalPath := sourceRelativePath("ai-gateway-local.yaml")

	tests := []struct {
		name           string
		path           string
		envVars        map[string]string
		expectHostname string
		expectPort     string
	}{
		{
			name:           "non default config",
			path:           aiGatewayLocalPath,
			expectHostname: "127.0.0.1.nip.io",
			expectPort:     "11434",
		},
		{
			name: "non default config with OPENAI_HOST OPENAI_PORT",
			path: aiGatewayLocalPath,
			envVars: map[string]string{
				"OPENAI_HOST": "host.docker.internal",
				"OPENAI_PORT": "8080",
			},
			expectHostname: "host.docker.internal",
			expectPort:     "8080",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			config, err := readConfig(tt.path)
			require.NoError(t, err)
			require.Contains(t, config, "hostname: "+tt.expectHostname)
			require.Contains(t, config, "port: "+tt.expectPort)
		})
	}

	// Historical configuration used an IP for ollama. We can't use this
	// config in docker, as it needs a hostname. However, we have another
	// config to use in docker, ai-gateway-local.yaml. So, we leave this
	// one alone.
	t.Run("Default config uses 0.0.0.0 IP for Ollama", func(t *testing.T) {
		config, err := readConfig("")
		require.NoError(t, err)
		require.Contains(t, config, "address: 0.0.0.0")
		require.Contains(t, config, "port: 11434")
	})

	t.Run("error when file does not exist", func(t *testing.T) {
		_, err := readConfig("/non/existent/file.yaml")
		require.Error(t, err)
		require.EqualError(t, err, "error reading config: open /non/existent/file.yaml: no such file or directory")
	})
}

func TestRecreateDir(t *testing.T) {
	tests := []struct {
		name      string
		setupFunc func(t *testing.T, path string)
	}{
		{
			name: "creates new directory",
			setupFunc: func(*testing.T, string) {
			},
		},
		{
			name: "recreates existing directory",
			setupFunc: func(t *testing.T, path string) {
				require.NoError(t, os.MkdirAll(path, 0o755))
				testFile := filepath.Join(path, "test.txt")
				require.NoError(t, os.WriteFile(testFile, []byte("test"), 0o600))
			},
		},
		{
			name: "recreates existing directory with subdirs",
			setupFunc: func(t *testing.T, path string) {
				subdir := filepath.Join(path, "subdir")
				require.NoError(t, os.MkdirAll(subdir, 0o755))
				testFile := filepath.Join(subdir, "test.txt")
				require.NoError(t, os.WriteFile(testFile, []byte("test"), 0o600))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()

			tt.setupFunc(t, tmpDir)

			err := recreateDir(tmpDir)
			require.NoError(t, err)
			// Verify directory exists and is empty.
			info, err := os.Stat(tmpDir)
			require.NoError(t, err)
			require.True(t, info.IsDir())

			// Check directory is empty.
			entries, err := os.ReadDir(tmpDir)
			require.NoError(t, err)
			require.Empty(t, entries)
		})
	}
}

func TestMaybeResolveHome(t *testing.T) {
	homeDir, err := os.UserHomeDir()
	require.NoError(t, err)

	tests := []struct {
		name   string
		path   string
		expect string
	}{
		{
			name:   "tilde path",
			path:   "~/test/file.txt",
			expect: filepath.Join(homeDir, "test/file.txt"),
		},
		{
			name:   "absolute path",
			path:   "/absolute/path/file.txt",
			expect: "/absolute/path/file.txt",
		},
		{
			name:   "relative path",
			path:   "relative/path/file.txt",
			expect: "relative/path/file.txt",
		},
		{
			name:   "tilde only",
			path:   "~",
			expect: "~",
		},
		{
			name:   "tilde in middle",
			path:   "/path/~/file.txt",
			expect: "/path/~/file.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := maybeResolveHome(tt.path)
			require.Equal(t, tt.expect, home)
		})
	}
}

func sourceRelativePath(file string) string {
	_, filename, _, _ := runtime.Caller(0)
	testDir := filepath.Dir(filename)
	return filepath.Join(testDir, file)
}
