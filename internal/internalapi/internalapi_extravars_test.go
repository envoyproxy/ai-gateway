// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package internalapi

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestParseExtraEnvVars(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []corev1.EnvVar
	}{
		{
			name:     "empty string",
			input:    "",
			expected: nil,
		},
		{
			name:  "single",
			input: "OTEL_SERVICE_NAME=ai-gateway",
			expected: []corev1.EnvVar{
				{Name: "OTEL_SERVICE_NAME", Value: "ai-gateway"},
			},
		},
		{
			name:  "comma-separated values",
			input: "OTEL_TRACES_EXPORTER=otlp,console",
			expected: []corev1.EnvVar{
				{Name: "OTEL_TRACES_EXPORTER", Value: "otlp,console"},
			},
		},
		{
			name:  "multiple",
			input: "OTEL_EXPORTER_OTLP_ENDPOINT=http://phoenix:6006;OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf",
			expected: []corev1.EnvVar{
				{Name: "OTEL_EXPORTER_OTLP_ENDPOINT", Value: "http://phoenix:6006"},
				{Name: "OTEL_EXPORTER_OTLP_PROTOCOL", Value: "http/protobuf"},
			},
		},
		{
			name:  "comma-separated key=value pairs",
			input: "OTEL_RESOURCE_ATTRIBUTES=service.name=ai-gateway,service.version=1.0.0,deployment.environment=production",
			expected: []corev1.EnvVar{
				{Name: "OTEL_RESOURCE_ATTRIBUTES", Value: "service.name=ai-gateway,service.version=1.0.0,deployment.environment=production"},
			},
		},
		{
			name:  "boolean values",
			input: "OPENINFERENCE_HIDE_INPUTS=false;OPENINFERENCE_HIDE_OUTPUTS=false;OPENINFERENCE_HIDE_INPUT_MESSAGES=true",
			expected: []corev1.EnvVar{
				{Name: "OPENINFERENCE_HIDE_INPUTS", Value: "false"},
				{Name: "OPENINFERENCE_HIDE_OUTPUTS", Value: "false"},
				{Name: "OPENINFERENCE_HIDE_INPUT_MESSAGES", Value: "true"},
			},
		},
		{
			name:  "trailing semicolon ignored",
			input: "OTEL_SERVICE_NAME=gateway;OTEL_TRACES_EXPORTER=console;",
			expected: []corev1.EnvVar{
				{Name: "OTEL_SERVICE_NAME", Value: "gateway"},
				{Name: "OTEL_TRACES_EXPORTER", Value: "console"},
			},
		},
		{
			name:  "empty value allowed",
			input: "OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT=;OTEL_ATTRIBUTE_COUNT_LIMIT=128",
			expected: []corev1.EnvVar{
				{Name: "OTEL_ATTRIBUTE_VALUE_LENGTH_LIMIT", Value: ""},
				{Name: "OTEL_ATTRIBUTE_COUNT_LIMIT", Value: "128"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseExtraEnvVars(tt.input)
			require.NoError(t, err)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestParseExtraEnvVars_Errors(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected error
	}{
		{
			name:     "missing equals",
			input:    "OTEL_SERVICE_NAME;OTEL_TRACES_EXPORTER=console",
			expected: fmt.Errorf("invalid env var pair at position 1: \"OTEL_SERVICE_NAME\" (expected format: KEY=value)"),
		},
		{
			name:     "empty key",
			input:    "=value;OTEL_SERVICE_NAME=gateway",
			expected: fmt.Errorf("empty env var name at position 1: \"=value\""),
		},
		{
			name:     "only equals",
			input:    "=",
			expected: fmt.Errorf("empty env var name at position 1: \"=\""),
		},
		{
			name:     "spaces only as key",
			input:    "  =value",
			expected: fmt.Errorf("empty env var name at position 1: \"=value\""),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseExtraEnvVars(tt.input)
			require.Error(t, err)
			require.Nil(t, got)
			require.Equal(t, tt.expected.Error(), err.Error())
		})
	}
}
