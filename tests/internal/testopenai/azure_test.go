// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBuildAzurePath(t *testing.T) {
	tests := []struct {
		name         string
		endpoint     string
		model        string
		expectedPath string
	}{
		{
			name:         "builds chat completions path",
			endpoint:     "/chat/completions",
			model:        "gpt-4",
			expectedPath: "/openai/deployments/gpt-4/chat/completions",
		},
		{
			name:         "builds embeddings path",
			endpoint:     "/embeddings",
			model:        "text-embedding-3-small",
			expectedPath: "/openai/deployments/text-embedding-3-small/embeddings",
		},
		{
			name:         "builds completions path",
			endpoint:     "/completions",
			model:        "gpt-5-nano",
			expectedPath: "/openai/deployments/gpt-5-nano/completions",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := buildAzurePath(tc.endpoint, tc.model)
			require.Equal(t, tc.expectedPath, result)
		})
	}
}

func TestIsAzureURL(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected bool
	}{
		{
			name:     "Azure chat completions URL",
			url:      "https://my-resource.cognitiveservices.azure.com/openai/deployments/gpt-4/chat/completions",
			expected: true,
		},
		{
			name:     "Azure embeddings URL",
			url:      "https://my-resource.cognitiveservices.azure.com/openai/deployments/text-embedding-3-small/embeddings",
			expected: true,
		},
		{
			name:     "Azure completions URL",
			url:      "https://my-resource.cognitiveservices.azure.com/openai/deployments/gpt-5-nano/completions",
			expected: true,
		},
		{
			name:     "standard OpenAI URL",
			url:      "https://api.openai.com/v1/chat/completions",
			expected: false,
		},
		{
			name:     "localhost URL",
			url:      "http://localhost:11434/v1/chat/completions",
			expected: false,
		},
		{
			name:     "empty URL",
			url:      "",
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := isAzureURL(tc.url)
			require.Equal(t, tc.expected, result)
		})
	}
}

func TestScrubAzureURL(t *testing.T) {
	tests := []struct {
		name        string
		url         string
		model       string
		expectedURL string
	}{
		{
			name:        "scrubs Azure chat URL",
			url:         "https://my-resource.eastus2.cognitiveservices.azure.com/openai/deployments/prod-gpt4/chat/completions?api-version=2024-12-01-preview",
			model:       "gpt-4",
			expectedURL: "https://resource-name.cognitiveservices.azure.com/openai/deployments/gpt-4/chat/completions",
		},
		{
			name:        "scrubs Azure embeddings URL",
			url:         "https://test-resource.westus.cognitiveservices.azure.com/openai/deployments/my-embedding-deployment/embeddings?api-version=2024-02-15-preview",
			model:       "text-embedding-3-small",
			expectedURL: "https://resource-name.cognitiveservices.azure.com/openai/deployments/text-embedding-3-small/embeddings",
		},
		{
			name:        "deployment name same as model",
			url:         "https://my-resource.cognitiveservices.azure.com/openai/deployments/gpt-5-nano/chat/completions?api-version=2024-12-01-preview",
			model:       "gpt-5-nano",
			expectedURL: "https://resource-name.cognitiveservices.azure.com/openai/deployments/gpt-5-nano/chat/completions",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := scrubAzureURL(tc.url, tc.model)
			require.Equal(t, tc.expectedURL, result)
		})
	}
}
