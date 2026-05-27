// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package gcpauth

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewTransport(t *testing.T) {
	tests := []struct {
		name        string
		proxyURL    string
		setProxyEnv bool
		wantErr     bool
		errContains string
	}{
		{
			name:        "no proxy set",
			setProxyEnv: false,
			wantErr:     false,
		},
		{
			name:        "empty proxy",
			proxyURL:    "",
			setProxyEnv: true,
			wantErr:     false,
		},
		{
			name:        "valid http proxy URL",
			proxyURL:    "http://proxy.example.com:3128",
			setProxyEnv: true,
			wantErr:     false,
		},
		{
			name:        "valid https proxy URL",
			proxyURL:    "https://secure-proxy.example.com:8443",
			setProxyEnv: true,
			wantErr:     false,
		},
		{
			name:        "invalid proxy URL",
			proxyURL:    "://invalid",
			setProxyEnv: true,
			wantErr:     true,
			errContains: "invalid AI_GATEWAY_GCP_AUTH_PROXY_URL",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setProxyEnv {
				t.Setenv(ProxyEnvVar, tt.proxyURL)
			}

			transport, err := NewTransport()

			if tt.wantErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.errContains)
				require.Nil(t, transport)
			} else {
				require.NoError(t, err)
				require.NotNil(t, transport)
				_, ok := transport.(*http.Transport)
				require.True(t, ok, "expected *http.Transport")
			}
		})
	}
}

func TestMustNewTransport(t *testing.T) {
	t.Run("succeeds with valid config", func(t *testing.T) {
		t.Setenv(ProxyEnvVar, "http://proxy.example.com:8080")

		require.NotPanics(t, func() {
			transport := MustNewTransport()
			require.NotNil(t, transport)
		})
	})

	t.Run("panics with invalid config", func(t *testing.T) {
		t.Setenv(ProxyEnvVar, "://invalid")

		require.Panics(t, func() {
			MustNewTransport()
		})
	})
}
