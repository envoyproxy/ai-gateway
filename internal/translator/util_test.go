// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// TestParseDataURI tests the parseDataURI function with various inputs.
func TestParseDataURI(t *testing.T) {
	tests := []struct {
		name          string
		uri           string
		wantType      string
		wantData      []byte
		expectErr     bool
		expectedError string
	}{
		{
			name:      "Valid JPEG Data URI",
			uri:       "data:image/jpeg;base64,dGVzdF9kYXRh", // "test_data" in base64.
			wantType:  "image/jpeg",
			wantData:  []byte("test_data"),
			expectErr: false,
		},
		{
			name:      "Valid PNG Data URI",
			uri:       "data:image/png;base64,dGVzdF9wbmc=", // "test_png" in base64.
			wantType:  "image/png",
			wantData:  []byte("test_png"),
			expectErr: false,
		},
		{
			name:          "Invalid URI Format",
			uri:           "not-a-data-uri",
			expectErr:     true,
			expectedError: "data uri does not have a valid format",
		},
		{
			name:          "Malformed Base64",
			uri:           "data:image/jpeg;base64,invalid-base64-string",
			expectErr:     true,
			expectedError: "illegal base64 data at input byte 7",
		},
		{
			name:      "Data URI without base64 encoding specified",
			uri:       "data:text/plain,SGVsbG8sIFdvcmxkIQ==",
			wantType:  "text/plain",
			wantData:  []byte("Hello, World!"),
			expectErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			contentType, data, err := parseDataURI(tc.uri)

			if tc.expectErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedError)
				require.Nil(t, data)
				require.Empty(t, contentType)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantType, contentType)
				require.Equal(t, tc.wantData, data)
			}
		})
	}
}

// TestSystemMsgToDeveloperMsg tests the systemMsgToDeveloperMsg function.
func TestSystemMsgToDeveloperMsg(t *testing.T) {
	systemMsg := openai.ChatCompletionSystemMessageParam{
		Name:    "test-system",
		Content: openai.ContentUnion{Value: "You are a helpful assistant."},
	}
	developerMsg := systemMsgToDeveloperMsg(systemMsg)
	require.Equal(t, "test-system", developerMsg.Name)
	require.Equal(t, openai.ChatMessageRoleDeveloper, developerMsg.Role)
	require.Equal(t, openai.ContentUnion{Value: "You are a helpful assistant."}, developerMsg.Content)
}

func TestEncodeIDWithRouting(t *testing.T) {
	tests := []struct {
		name        string
		id          string
		modelName   string
		backendName string
		idType      string
	}{
		{
			name:        "encodes file id with model and backend",
			id:          "file-abc123",
			modelName:   "gpt-4o-mini",
			backendName: "azure-openai",
			idType:      "file",
		},
		{
			name:        "encodes with empty backend",
			id:          "file-xyz789",
			modelName:   "gpt-4.1",
			backendName: "",
			idType:      "file",
		},
		{
			name:        "encodes with special characters in backend",
			id:          "file-test123",
			modelName:   "claude-3",
			backendName: "anthropic-us-east-1",
			idType:      "file",
		},
		{
			name:        "encodes batch id with model and backend",
			id:          "batch_abc123",
			modelName:   "gpt-4o-mini",
			backendName: "openai-primary",
			idType:      "batch",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			encoded := EncodeFileIDWithRouting(tc.id, tc.modelName, tc.backendName, tc.idType)

			// Verify it starts with the correct prefix
			expectedPrefix := FileIDPrefix
			if tc.idType == "batch" {
				expectedPrefix = BatchIDPrefix
			}
			require.True(t, strings.HasPrefix(encoded, expectedPrefix), "encoded ID should start with expected prefix")

			// Decode and verify
			modelName, backendName, id, err := DecodeFileIDWithRouting(encoded)
			require.NoError(t, err)
			require.Equal(t, tc.modelName, modelName)
			require.Equal(t, tc.backendName, backendName)
			require.Equal(t, tc.id, id)
		})
	}
}

func TestDecodeFileIDRouting(t *testing.T) {
	tests := []struct {
		name            string
		encodedID       string
		wantModelName   string
		wantBackendName string
		wantID          string
		expectErr       bool
		expectedError   string
	}{
		{
			name:            "decodes new format with backend",
			encodedID:       EncodeFileIDWithRouting("file-abc123", "gpt-4o-mini", "azure-openai", "file"),
			wantModelName:   "gpt-4o-mini",
			wantBackendName: "azure-openai",
			wantID:          "file-abc123",
			expectErr:       false,
		},
		{
			name:            "decodes new format without backend",
			encodedID:       EncodeFileIDWithRouting("file-xyz789", "claude-3", "", "file"),
			wantModelName:   "claude-3",
			wantBackendName: "",
			wantID:          "file-xyz789",
			expectErr:       false,
		},
		{
			name:            "decodes batch prefix format",
			encodedID:       EncodeFileIDWithRouting("batch_abc123", "gpt-4.1", "azure-openai", "batch"),
			wantModelName:   "gpt-4.1",
			wantBackendName: "azure-openai",
			wantID:          "batch_abc123",
			expectErr:       false,
		},
		{
			name:          "returns error on legacy id model format",
			encodedID:     FileIDPrefix + base64.RawURLEncoding.EncodeToString([]byte("id:file-legacy123;model:gpt-4o-mini")),
			expectErr:     true,
			expectedError: "invalid decoded ID format: expected format 'aigw:<original_id>;model:<model_name>;backend:<backend_name>'",
		},
		{
			name:          "returns error on missing prefix",
			encodedID:     "YWlndzpmaWxlLWFiYzEyMzttb2RlbDpncHQtNDttYmFja2VuZDphenVyZQ",
			expectErr:     true,
			expectedError: "invalid encoded ID format: missing expected prefix",
		},
		{
			name:          "returns error on malformed base64",
			encodedID:     "file-not@valid@base64",
			expectErr:     true,
			expectedError: "failed to decode base64 part of the ID",
		},
		{
			name:          "returns error on missing model in new format",
			encodedID:     FileIDPrefix + base64.RawURLEncoding.EncodeToString([]byte("aigw:file-123;backend:azure")),
			expectErr:     true,
			expectedError: "model name not found in decoded Id",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			modelName, backendName, id, err := DecodeFileIDWithRouting(tc.encodedID)

			if tc.expectErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedError)
				require.Empty(t, modelName)
				require.Empty(t, backendName)
				require.Empty(t, id)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantModelName, modelName)
				require.Equal(t, tc.wantBackendName, backendName)
				require.Equal(t, tc.wantID, id)
			}
		})
	}
}
