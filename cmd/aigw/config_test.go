// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
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
			name:           "default hostname and port",
			expectHostname: "127.0.0.1.nip.io",
			expectPort:     "11434",
		},
		{
			name: "default config override with OLLAMA_HOST",
			envVars: map[string]string{
				"OLLAMA_HOST": "host.docker.internal",
			},
			expectHostname: "host.docker.internal",
			expectPort:     "11434",
		},
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
}

func sourceRelativePath(file string) string {
	_, filename, _, _ := runtime.Caller(0)
	testDir := filepath.Dir(filename)
	return filepath.Join(testDir, file)
}
