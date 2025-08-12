// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tracing

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestNewTracingFromEnv_DefaultServiceName tests that the service name
// defaults to "ai-gateway" when OTEL_SERVICE_NAME is not set.
func TestNewTracingFromEnv_DefaultServiceName(t *testing.T) {
	tests := []struct {
		name              string
		env               map[string]string
		expectServiceName string
	}{
		{
			name: "default service name when OTEL_SERVICE_NAME not set",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER": "console",
			},
			expectServiceName: "ai-gateway",
		},
		{
			name: "OTEL_SERVICE_NAME overrides default",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER": "console",
				"OTEL_SERVICE_NAME":    "custom-service",
			},
			expectServiceName: "custom-service",
		},
		{
			name: "OTEL_RESOURCE_ATTRIBUTES with service.name overrides default",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER":     "console",
				"OTEL_RESOURCE_ATTRIBUTES": "service.name=from-resource-attrs",
			},
			expectServiceName: "from-resource-attrs",
		},
		{
			name: "OTEL_SERVICE_NAME takes precedence over OTEL_RESOURCE_ATTRIBUTES",
			env: map[string]string{
				"OTEL_TRACES_EXPORTER":     "console",
				"OTEL_SERVICE_NAME":        "from-env",
				"OTEL_RESOURCE_ATTRIBUTES": "service.name=from-resource-attrs",
			},
			expectServiceName: "from-env",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}

			var stdout bytes.Buffer
			result, err := NewTracingFromEnv(t.Context(), &stdout)
			require.NoError(t, err)
			t.Cleanup(func() {
				_ = result.Shutdown(t.Context())
			})

			// Start a span to trigger output.
			span := startCompletionsSpan(t, result, nil)
			require.NotNil(t, span)
			span.EndSpan(200, nil)

			// Check that the service name appears in the console output.
			output := stdout.String()
			require.Contains(t, output, `"service.name"`)
			require.Contains(t, output, tt.expectServiceName)

			// Verify the service name is in the resource attributes
			// The console exporter outputs in JSON format with the service name
			// in the Resource.Attributes section.
			require.Contains(t, output, `"Value":"`+tt.expectServiceName+`"`,
				"Expected service name %q in output, got: %s", tt.expectServiceName, output)
		})
	}
}
