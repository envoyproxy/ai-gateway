package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_parseAndValidateFlags(t *testing.T) {
	t.Run("no flags", func(t *testing.T) {
		args := []string{}
		extProcLogLevel, extProcImage, enableLeaderElection, logLevel, extensionServerPort, err := parseAndValidateFlags(args)
		require.Equal(t, "info", extProcLogLevel)
		require.Equal(t, "ghcr.io/envoyproxy/ai-gateway/extproc:latest", extProcImage)
		require.True(t, enableLeaderElection)
		require.Equal(t, "info", logLevel.String())
		require.Equal(t, ":1063", extensionServerPort)
		require.NoError(t, err)
	})
	t.Run("all flags", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			dash string
		}{
			{"single dash", "-"},
			{"double dash", "--"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				args := []string{
					tc.dash + "extProcLogLevel=debug",
					tc.dash + "extProcImage=example.com/extproc:latest",
					tc.dash + "enableLeaderElection=false",
					tc.dash + "logLevel=debug",
					tc.dash + "port=:8080",
				}
				extProcLogLevel, extProcImage, enableLeaderElection, logLevel, extensionServerPort, err := parseAndValidateFlags(args)
				require.Equal(t, "debug", extProcLogLevel)
				require.Equal(t, "example.com/extproc:latest", extProcImage)
				require.False(t, enableLeaderElection)
				require.Equal(t, "debug", logLevel.String())
				require.Equal(t, ":8080", extensionServerPort)
				require.NoError(t, err)
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
				name:   "invalid extProcLogLevel",
				flags:  []string{"--extProcLogLevel=invalid"},
				expErr: "invalid external processor log level: \"invalid\"",
			},
			{
				name:   "invalid logLevel",
				flags:  []string{"--logLevel=invalid"},
				expErr: "invalid log level: \"invalid\"",
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, _, _, _, _, err := parseAndValidateFlags(tc.flags)
				require.ErrorContains(t, err, tc.expErr)
			})
		}
	})
}
