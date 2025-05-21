// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_parseAndValidateFlags(t *testing.T) {
	t.Run("no flags", func(t *testing.T) {
		f, err := parseAndValidateFlags([]string{})
		require.Equal(t, "info", f.extProcLogLevel)
		require.Equal(t, "docker.io/envoyproxy/ai-gateway-extproc:latest", f.extProcImage)
		require.True(t, f.enableLeaderElection)
		require.Equal(t, "info", f.logLevel.String())
		require.Equal(t, ":1063", f.extensionServerPort)
		require.False(t, f.enableInfExt)
		require.Equal(t, "/certs", f.tlsCertDir)
		require.Equal(t, "tls.crt", f.tlsCertName)
		require.Equal(t, "tls.key", f.tlsKeyName)
		require.Equal(t, "envoy-gateway-system", f.envoyGatewaySystemNamespace)
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
					tc.dash + "enableInferenceExtension=true",
				}
				f, err := parseAndValidateFlags(args)
				require.Equal(t, "debug", f.extProcLogLevel)
				require.Equal(t, "example.com/extproc:latest", f.extProcImage)
				require.False(t, f.enableLeaderElection)
				require.Equal(t, "debug", f.logLevel.String())
				require.Equal(t, ":8080", f.extensionServerPort)
				require.True(t, f.enableInfExt)
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
				_, err := parseAndValidateFlags(tc.flags)
				require.ErrorContains(t, err, tc.expErr)
			})
		}
	})
}
