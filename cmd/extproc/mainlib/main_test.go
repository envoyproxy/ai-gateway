// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mainlib

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func Test_parseAndValidateFlags(t *testing.T) {
	t.Run("ok extProcFlags", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			args       []string
			configPath string
			addr       string
			logLevel   slog.Level
		}{
			{
				name:       "minimal extProcFlags",
				args:       []string{"-configPath", "/path/to/config.yaml"},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				logLevel:   slog.LevelInfo,
			},
			{
				name:       "custom addr",
				args:       []string{"-configPath", "/path/to/config.yaml", "-extProcAddr", "unix:///tmp/ext_proc.sock"},
				configPath: "/path/to/config.yaml",
				addr:       "unix:///tmp/ext_proc.sock",
				logLevel:   slog.LevelInfo,
			},
			{
				name:       "log level debug",
				args:       []string{"-configPath", "/path/to/config.yaml", "-logLevel", "debug"},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				logLevel:   slog.LevelDebug,
			},
			{
				name:       "log level warn",
				args:       []string{"-configPath", "/path/to/config.yaml", "-logLevel", "warn"},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				logLevel:   slog.LevelWarn,
			},
			{
				name:       "log level error",
				args:       []string{"-configPath", "/path/to/config.yaml", "-logLevel", "error"},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				logLevel:   slog.LevelError,
			},
			{
				name: "all extProcFlags",
				args: []string{
					"-configPath", "/path/to/config.yaml",
					"-extProcAddr", "unix:///tmp/ext_proc.sock",
					"-logLevel", "debug",
				},
				configPath: "/path/to/config.yaml",
				addr:       "unix:///tmp/ext_proc.sock",
				logLevel:   slog.LevelDebug,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				flags, err := parseAndValidateFlags(tc.args)
				require.NoError(t, err)
				assert.Equal(t, tc.configPath, flags.configPath)
				assert.Equal(t, tc.addr, flags.extProcAddr)
				assert.Equal(t, tc.logLevel, flags.logLevel)
			})
		}
	})

	t.Run("invalid extProcFlags", func(t *testing.T) {
		_, err := parseAndValidateFlags([]string{"-logLevel", "invalid"})
		assert.EqualError(t, err, `configPath must be provided
failed to unmarshal log level: slog: level string "invalid": unknown name`)
	})
}

func TestListenAddress(t *testing.T) {
	tests := []struct {
		addr        string
		wantNetwork string
		wantAddress string
	}{
		{":8080", "tcp", ":8080"},
		{"unix:///var/run/ai-gateway/extproc.sock", "unix", "/var/run/ai-gateway/extproc.sock"},
	}

	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			network, address := listenAddress(tt.addr)
			assert.Equal(t, tt.wantNetwork, network)
			assert.Equal(t, tt.wantAddress, address)
		})
	}
}
