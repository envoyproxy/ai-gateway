// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/base64"
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

func TestEncodeIDWithModel(t *testing.T) {
	tests := []struct {
		name      string
		id        string
		modelName string
		idType    string
		want      string
	}{
		{
			name:      "encodes file id with file prefix",
			id:        "file-abc123",
			modelName: "gpt-4o-mini",
			idType:    "file",
			want:      "file-aWQ6ZmlsZS1hYmMxMjM7bW9kZWw6Z3B0LTRvLW1pbmk",
		},
		{
			name:      "encodes batch id with batch prefix",
			id:        "3814889423749775360",
			modelName: "gemini-2.5-pro",
			idType:    "batch",
			want:      "batch_aWQ6MzgxNDg4OTQyMzc0OTc3NTM2MDttb2RlbDpnZW1pbmktMi41LXBybw",
		},
		{
			name:      "defaults to file prefix for unknown id type",
			id:        "file-default",
			modelName: "gpt-4.1-mini",
			idType:    "unknown",
			want:      "file-aWQ6ZmlsZS1kZWZhdWx0O21vZGVsOmdwdC00LjEtbWluaQ",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := EncodeIDWithModel(tc.id, tc.modelName, tc.idType)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestDecodeFileID(t *testing.T) {
	tests := []struct {
		name          string
		encodedID     string
		wantModelName string
		wantID        string
		expectErr     bool
		expectedError string
	}{
		{
			name:          "decodes valid file id",
			encodedID:     "file-aWQ6ZmlsZS1hYmMxMjM7bW9kZWw6Z3B0LTRvLW1pbmk",
			wantModelName: "gpt-4o-mini",
			wantID:        "file-abc123",
			expectErr:     false,
		},
		{
			name:          "decodes valid batch id",
			encodedID:     "batch_aWQ6MzgxNDg4OTQyMzc0OTc3NTM2MDttb2RlbDpnZW1pbmktMi41LXBybw",
			wantModelName: "gemini-2.5-pro",
			wantID:        "3814889423749775360",
			expectErr:     false,
		},
		{
			name:          "returns error on missing expected prefix",
			encodedID:     "aWQ6ZmlsZS1hYmMxMjM7bW9kZWw6Z3B0LTRvLW1pbmk",
			expectErr:     true,
			expectedError: "invalid encoded ID format: missing expected prefix",
		},
		{
			name:          "returns error on malformed base64",
			encodedID:     "file-not@base64",
			expectErr:     true,
			expectedError: "failed to decode base64 part of the ID",
		},
		{
			name:          "returns error on invalid decoded format",
			encodedID:     FileIDPrefix + base64.RawURLEncoding.EncodeToString([]byte("id:file-abc123")),
			expectErr:     true,
			expectedError: "invalid decoded ID format: expected format 'id:<original_id>;model:<model_name>'",
		},
		{
			name:          "returns error when model is missing",
			encodedID:     FileIDPrefix + base64.RawURLEncoding.EncodeToString([]byte("id:file-abc123;model:")),
			expectErr:     true,
			expectedError: "model name not found in decoded Id",
		},
		{
			name:          "returns error when id is missing",
			encodedID:     BatchIDPrefix + base64.RawURLEncoding.EncodeToString([]byte("id:;model:gpt-4o-mini")),
			expectErr:     true,
			expectedError: "file/batch id not found in decoded Id",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			modelName, id, err := DecodeFileID(tc.encodedID)

			if tc.expectErr {
				require.Error(t, err)
				require.Contains(t, err.Error(), tc.expectedError)
				require.Empty(t, modelName)
				require.Empty(t, id)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.wantModelName, modelName)
				require.Equal(t, tc.wantID, id)
			}
		})
	}
}
