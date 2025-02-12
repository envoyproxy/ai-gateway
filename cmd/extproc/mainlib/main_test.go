package mainlib

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
)

func Test_parseAndValidateFlags(t *testing.T) {
	t.Run("ok flags", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			args       []string
			configPath string
			addr       string
			logLevel   slog.Level
		}{
			{
				name:       "minimal flags",
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
				name:       "prometheus port",
				args:       []string{"-configPath", "/path/to/config.yaml", "-promPort", ":9190"},
				configPath: "/path/to/config.yaml",
				addr:       ":1063",
				logLevel:   slog.LevelInfo,
			},
			{
				name:       "all flags",
				args:       []string{"-configPath", "/path/to/config.yaml", "-extProcAddr", "unix:///tmp/ext_proc.sock", "-logLevel", "debug", "-promPort", ":9190"},
				configPath: "/path/to/config.yaml",
				addr:       "unix:///tmp/ext_proc.sock",
				logLevel:   slog.LevelDebug,
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				configPath, addr, logLevel, promPort, err := parseAndValidateFlags(tc.args)
				assert.Equal(t, tc.configPath, configPath)
				assert.Equal(t, tc.addr, addr)
				assert.Equal(t, tc.logLevel, logLevel)
				assert.Equal(t, ":9190", promPort)
				assert.NoError(t, err)
			})
		}
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
				_, _, _, _, err := parseAndValidateFlags(tc.flags)
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
