package mainlib

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_parseAndValidateFlags(t *testing.T) {
	t.Run("minimal flags", func(t *testing.T) {
		args := []string{"-configPath", "/path/to/config.yaml"}
		configPath, addr, logLevel, err := parseAndValidateFlags(args)
		assert.Equal(t, "/path/to/config.yaml", configPath)
		assert.Equal(t, ":1063", addr)
		assert.Equal(t, slog.LevelInfo, logLevel)
		assert.NoError(t, err)
	})
	t.Run("all flags", func(t *testing.T) {
		args := []string{
			"-configPath", "/path/to/config.yaml",
			"-extProcAddr", "unix:///tmp/ext_proc.sock",
			"-logLevel", "debug",
		}
		configPath, addr, logLevel, err := parseAndValidateFlags(args)
		assert.Equal(t, "/path/to/config.yaml", configPath)
		assert.Equal(t, "unix:///tmp/ext_proc.sock", addr)
		assert.Equal(t, slog.LevelDebug, logLevel)
		assert.NoError(t, err)
	})
	t.Run("invalid flags", func(t *testing.T) {
		for _, tc := range []struct {
			name   string
			flags  []string
			expErr string
		}{
			{
				name:   "missing configPath",
				flags:  []string{"-extProcAddr", ":1063"},
				expErr: "configPath must be provided",
			},
			{
				name:   "invalid logLevel",
				flags:  []string{"-configPath", "/path/to/config.yaml", "-logLevel", "invalid"},
				expErr: `failed to unmarshal log level: slog: level string "invalid": unknown name`,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				configPath, addr, logLevel, err := parseAndValidateFlags(tc.flags)
				assert.Empty(t, configPath)
				assert.Empty(t, addr)
				assert.Equal(t, slog.LevelInfo, logLevel)
				assert.EqualError(t, err, tc.expErr)
			})
		}
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
