// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openinference

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewTraceConfig_Defaults(t *testing.T) {
	config := NewTraceConfig()

	require.Equal(t, defaultHideLLMInvocationParameters, config.HideLLMInvocationParameters)
	require.Equal(t, defaultHideInputs, config.HideInputs)
	require.Equal(t, defaultHideOutputs, config.HideOutputs)
	require.Equal(t, defaultHideInputMessages, config.HideInputMessages)
	require.Equal(t, defaultHideOutputMessages, config.HideOutputMessages)
	require.Equal(t, defaultHideInputImages, config.HideInputImages)
	require.Equal(t, defaultHideInputText, config.HideInputText)
	require.Equal(t, defaultHideOutputText, config.HideOutputText)
	require.Equal(t, defaultHideEmbeddingVectors, config.HideEmbeddingVectors)
	require.Equal(t, defaultBase64ImageMaxLength, config.Base64ImageMaxLength)
	require.Equal(t, defaultHidePrompts, config.HidePrompts)
}

func TestNewTraceConfigFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		validate func(t *testing.T, config *TraceConfig)
	}{
		{
			name: "all boolean environment variables set to true",
			envVars: map[string]string{
				EnvHideLLMInvocationParameters: "true",
				EnvHideInputs:                  "true",
				EnvHideOutputs:                 "true",
				EnvHideInputMessages:           "true",
				EnvHideOutputMessages:          "true",
				EnvHideInputImages:             "true",
				EnvHideInputText:               "true",
				EnvHideOutputText:              "true",
				EnvHideEmbeddingVectors:        "true",
				EnvHidePrompts:                 "true",
				EnvBase64ImageMaxLength:        "10000",
			},
			validate: func(t *testing.T, config *TraceConfig) {
				require.True(t, config.HideLLMInvocationParameters)
				require.True(t, config.HideInputs)
				require.True(t, config.HideOutputs)
				require.True(t, config.HideInputMessages)
				require.True(t, config.HideOutputMessages)
				require.True(t, config.HideInputImages)
				require.True(t, config.HideInputText)
				require.True(t, config.HideOutputText)
				require.True(t, config.HideEmbeddingVectors)
				require.True(t, config.HidePrompts)
				require.Equal(t, 10000, config.Base64ImageMaxLength)
			},
		},
		{
			name: "all boolean environment variables set to false",
			envVars: map[string]string{
				EnvHideLLMInvocationParameters: "false",
				EnvHideInputs:                  "false",
				EnvHideOutputs:                 "false",
				EnvHideInputMessages:           "false",
				EnvHideOutputMessages:          "false",
				EnvHideInputImages:             "false",
				EnvHideInputText:               "false",
				EnvHideOutputText:              "false",
				EnvHideEmbeddingVectors:        "false",
				EnvHidePrompts:                 "false",
				EnvBase64ImageMaxLength:        "50000",
			},
			validate: func(t *testing.T, config *TraceConfig) {
				require.False(t, config.HideLLMInvocationParameters)
				require.False(t, config.HideInputs)
				require.False(t, config.HideOutputs)
				require.False(t, config.HideInputMessages)
				require.False(t, config.HideOutputMessages)
				require.False(t, config.HideInputImages)
				require.False(t, config.HideInputText)
				require.False(t, config.HideOutputText)
				require.False(t, config.HideEmbeddingVectors)
				require.False(t, config.HidePrompts)
				require.Equal(t, 50000, config.Base64ImageMaxLength)
			},
		},
		{
			name: "partial environment variables",
			envVars: map[string]string{
				EnvHideInputs:           "true",
				EnvHideOutputMessages:   "true",
				EnvBase64ImageMaxLength: "15000",
			},
			validate: func(t *testing.T, config *TraceConfig) {
				require.True(t, config.HideInputs)
				require.True(t, config.HideOutputMessages)
				require.Equal(t, 15000, config.Base64ImageMaxLength)
				// Others should be defaults.
				require.Equal(t, defaultHideOutputs, config.HideOutputs)
				require.Equal(t, defaultHideInputMessages, config.HideInputMessages)
				require.Equal(t, defaultHideInputText, config.HideInputText)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set environment variables.
			for key, value := range tt.envVars {
				t.Setenv(key, value)
			}

			config := NewTraceConfigFromEnv()
			tt.validate(t, config)
		})
	}
}

func TestGetBoolEnv(t *testing.T) {
	// We use strconv.ParseBool, so only test a few cases for coverage.
	tests := []struct {
		name         string
		envValue     string
		defaultValue bool
		expected     bool
	}{
		{
			name:         "true",
			envValue:     "true",
			defaultValue: false,
			expected:     true,
		},
		{
			name:         "false",
			envValue:     "false",
			defaultValue: true,
			expected:     false,
		},
		{
			name:         "empty",
			envValue:     "",
			defaultValue: true,
			expected:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_BOOL_ENV"
			if tt.envValue != "" {
				t.Setenv(key, tt.envValue)
			}
			result := getBoolEnv(key, tt.defaultValue)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestGetIntEnv(t *testing.T) {
	// We use strconv.Atoi, so only test a few cases for coverage.
	defaultValue := 100
	tests := []struct {
		name     string
		envValue string
		expected int
	}{
		{
			name:     "positive",
			envValue: "12345",
			expected: 12345,
		},
		{
			name:     "zero",
			envValue: "0",
			expected: 0,
		},
		{
			name:     "empty",
			envValue: "",
			expected: defaultValue,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := "TEST_INT_ENV"
			if tt.envValue != "" {
				t.Setenv(key, tt.envValue)
			}
			result := getIntEnv(key, defaultValue)
			require.Equal(t, tt.expected, result)
		})
	}
}
