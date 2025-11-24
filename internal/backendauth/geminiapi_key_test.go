// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

func TestNewGeminiAPIKeyHandler(t *testing.T) {
	tests := []struct {
		name        string
		auth        *filterapi.GeminiAPIKeyAuth
		expectError bool
	}{
		{
			name:        "valid API key",
			auth:        &filterapi.GeminiAPIKeyAuth{Key: "test-key-123"},
			expectError: false,
		},
		{
			name:        "empty API key",
			auth:        &filterapi.GeminiAPIKeyAuth{Key: ""},
			expectError: true,
		},
		{
			name:        "whitespace only API key",
			auth:        &filterapi.GeminiAPIKeyAuth{Key: "   "},
			expectError: true,
		},
		{
			name:        "API key with leading/trailing spaces",
			auth:        &filterapi.GeminiAPIKeyAuth{Key: "  test-key  "},
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler, err := newGeminiAPIKeyHandler(tt.auth)
			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, handler)
			} else {
				require.NoError(t, err)
				require.NotNil(t, handler)
			}
		})
	}
}

func TestGeminiAPIKeyHandler_Do(t *testing.T) {
	tests := []struct {
		name           string
		apiKey         string
		requestHeaders map[string]string
		expectedPath   string
		expectError    bool
	}{
		{
			name:   "path without existing query params",
			apiKey: "test-key-123",
			requestHeaders: map[string]string{
				":path": "/v1/models/gemini-pro:generateContent",
			},
			expectedPath: "/v1/models/gemini-pro:generateContent?key=test-key-123",
			expectError:  false,
		},
		{
			name:   "path with existing query params",
			apiKey: "test-key-456",
			requestHeaders: map[string]string{
				":path": "/v1/models/gemini-pro:streamGenerateContent?alt=sse",
			},
			expectedPath: "/v1/models/gemini-pro:streamGenerateContent?alt=sse&key=test-key-456",
			expectError:  false,
		},
		{
			name:   "path with multiple query params",
			apiKey: "test-key-789",
			requestHeaders: map[string]string{
				":path": "/v1/models/gemini-pro:generateContent?param1=value1&param2=value2",
			},
			expectedPath: "/v1/models/gemini-pro:generateContent?param1=value1&param2=value2&key=test-key-789",
			expectError:  false,
		},
		{
			name:   "missing :path header",
			apiKey: "test-key-123",
			requestHeaders: map[string]string{
				"content-type": "application/json",
			},
			expectError: true,
		},
		{
			name:   "empty :path header",
			apiKey: "test-key-123",
			requestHeaders: map[string]string{
				":path": "",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := &geminiAPIKeyHandler{apiKey: tt.apiKey}
			headers, err := handler.Do(context.Background(), tt.requestHeaders, nil)

			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, headers)
			} else {
				require.NoError(t, err)
				require.NotNil(t, headers)
				require.Len(t, headers, 1)
				require.Equal(t, ":path", headers[0].Key())
				require.Equal(t, tt.expectedPath, headers[0].Value())
				require.Equal(t, tt.expectedPath, tt.requestHeaders[":path"])
			}
		})
	}
}

