// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package autoconfig

import (
	"testing"

	"github.com/stretchr/testify/require"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func TestPopulateOTELLogEnvConfig(t *testing.T) {
	internaltesting.ClearTestEnv(t)
	tests := []struct {
		name           string
		env            map[string]string
		expectBackends []Backend
		expectLog      *otelLogConfig
		expectErr      bool
		data           *ConfigData
	}{
		{
			name:      "no env defaults to console",
			expectLog: &otelLogConfig{Exporter: "console"},
		},
		{
			name: "no endpoint with backends defaults to console",
			data: &ConfigData{
				Backends: []Backend{
					{Name: "openai", Hostname: "api.openai.com", Port: 443, NeedsTLS: true},
				},
			},
			expectBackends: []Backend{
				{Name: "openai", Hostname: "api.openai.com", Port: 443, NeedsTLS: true},
			},
			expectLog: &otelLogConfig{Exporter: "console"},
		},
		{
			name: "OTEL_LOGS_EXPORTER=none disables access logs",
			env: map[string]string{
				"OTEL_LOGS_EXPORTER": "none",
			},
			expectLog: &otelLogConfig{Exporter: "none"},
		},
		{
			name: "OTEL_LOGS_EXPORTER=console uses file sink even when endpoint provided",
			env: map[string]string{
				"OTEL_LOGS_EXPORTER":          "console",
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317",
			},
			expectLog: &otelLogConfig{
				Exporter: "console",
			},
		},
		{
			name: "generic endpoint uses backend name otel with headers and resources",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317",
				"OTEL_EXPORTER_OTLP_HEADERS":  "Authorization=ApiKey fake-key",
				"OTEL_RESOURCE_ATTRIBUTES":    "service.name=attr-service,service.version=v1",
			},
			expectBackends: []Backend{
				{Name: "otel", Hostname: "collector", Port: 4317, NeedsTLS: false},
			},
			expectLog: &otelLogConfig{
				Exporter:    "otlp",
				BackendName: "otel",
				Headers:     []otelHeader{{Name: "Authorization", Value: "ApiKey fake-key"}},
				Resources: []otelResourceAttr{
					{Key: "service.name", Value: "attr-service"},
					{Key: "service.version", Value: "v1"},
				},
			},
		},
		{
			name: "logs endpoint takes precedence over generic",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT":      "http://generic:4317",
				"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://logs:4317",
			},
			expectBackends: []Backend{
				{Name: "otel-logs", Hostname: "logs", Port: 4317, NeedsTLS: false},
			},
			expectLog: &otelLogConfig{
				Exporter:    "otlp",
				BackendName: "otel-logs",
			},
		},
		{
			name: "https endpoint defaults to 443 and sets NeedsTLS",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "https://secure.example.com",
			},
			expectBackends: []Backend{
				{Name: "otel", Hostname: "secure.example.com", Port: 443, NeedsTLS: true},
			},
			expectLog: &otelLogConfig{
				Exporter:    "otlp",
				BackendName: "otel",
			},
		},
		{
			name: "http endpoint without port uses default HTTP port",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector",
			},
			expectBackends: []Backend{
				{Name: "otel", Hostname: "collector", Port: 80, NeedsTLS: false},
			},
			expectLog: &otelLogConfig{
				Exporter:    "otlp",
				BackendName: "otel",
			},
		},
		{
			name: "ip endpoint without tls uses default HTTP port",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://10.0.0.5",
			},
			expectBackends: []Backend{
				{Name: "otel", IP: "10.0.0.5", Port: 80, NeedsTLS: false},
			},
			expectLog: &otelLogConfig{
				Exporter:    "otlp",
				BackendName: "otel",
			},
		},
		{
			name: "logs headers take precedence over generic",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT":      "http://collector:4317",
				"OTEL_EXPORTER_OTLP_HEADERS":       "Authorization=ApiKey generic-key",
				"OTEL_EXPORTER_OTLP_LOGS_HEADERS":  "Authorization=ApiKey logs-key",
				"OTEL_EXPORTER_OTLP_LOGS_ENDPOINT": "http://collector:4317",
			},
			expectBackends: []Backend{
				{Name: "otel-logs", Hostname: "collector", Port: 4317, NeedsTLS: false},
			},
			expectLog: &otelLogConfig{
				Exporter:    "otlp",
				BackendName: "otel-logs",
				Headers:     []otelHeader{{Name: "Authorization", Value: "ApiKey logs-key"}},
			},
		},
		{
			name: "service name overrides resource attributes",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317",
				"OTEL_RESOURCE_ATTRIBUTES":    "service.name=attr,service.version=v1",
				"OTEL_SERVICE_NAME":           "override",
			},
			expectBackends: []Backend{
				{Name: "otel", Hostname: "collector", Port: 4317, NeedsTLS: false},
			},
			expectLog: &otelLogConfig{
				Exporter:    "otlp",
				BackendName: "otel",
				Resources: []otelResourceAttr{
					{Key: "service.name", Value: "override"},
					{Key: "service.version", Value: "v1"},
				},
			},
		},
		{
			name: "protocol http returns error",
			env: map[string]string{
				"OTEL_EXPORTER_OTLP_ENDPOINT": "http://collector:4317",
				"OTEL_EXPORTER_OTLP_PROTOCOL": "http",
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			data := tt.data
			if data == nil {
				data = &ConfigData{}
			}
			err := PopulateOTELLogEnvConfig(data)
			if tt.expectErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.expectLog, data.OTELLog)
			assertOTELBackends(t, data.Backends, tt.expectBackends)
		})
	}
}

func assertOTELBackends(t *testing.T, backends []Backend, expected []Backend) {
	t.Helper()
	require.Equal(t, expected, backends)
}
