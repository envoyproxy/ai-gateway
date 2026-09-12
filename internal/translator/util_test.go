// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
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

func TestCutSSEDataPrefix(t *testing.T) {
	for _, tc := range []struct {
		name     string
		line     string
		expected string
		ok       bool
	}{
		{name: "space after colon", line: `data: {"a":1}`, expected: `{"a":1}`, ok: true},
		{name: "no space after colon", line: `data:{"a":1}`, expected: `{"a":1}`, ok: true},
		{name: "only the first space is framing", line: `data:  {"a":1}`, expected: ` {"a":1}`, ok: true},
		{name: "empty value", line: "data:", expected: "", ok: true},
		{name: "empty value with space", line: "data: ", expected: "", ok: true},
		{name: "done marker without space", line: "data:[DONE]", expected: "[DONE]", ok: true},
		{name: "other field", line: "event: message_start", ok: false},
		{name: "comment", line: ": keep-alive", ok: false},
		{name: "not a field", line: `{"a":1}`, ok: false},
		{name: "empty line", line: "", ok: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, ok := cutSSEDataPrefix([]byte(tc.line))
			require.Equal(t, tc.ok, ok)
			if tc.ok {
				require.Equal(t, tc.expected, string(data))
			}
		})
	}
}

func TestIndexSSEEventBoundary(t *testing.T) {
	for _, tc := range []struct {
		name        string
		buf         string
		expectedIdx int
		expectedSep int
	}{
		{
			name:        "LF framing",
			buf:         "data: a\n\n",
			expectedIdx: bytes.Index([]byte("data: a\n\n"), []byte("\n\n")),
			expectedSep: 2,
		},
		{
			name:        "CRLF framing",
			buf:         "data: a\r\n\r\n",
			expectedIdx: bytes.Index([]byte("data: a\r\n\r\n"), []byte("\r\n\r\n")),
			expectedSep: 4,
		},
		{
			name:        "LF boundary before a later CRLF boundary",
			buf:         "data: a\n\ndata: b\r\n\r\n",
			expectedIdx: bytes.Index([]byte("data: a\n\ndata: b\r\n\r\n"), []byte("\n\n")),
			expectedSep: 2,
		},
		{
			name:        "CRLF boundary before a later LF boundary",
			buf:         "data: a\r\n\r\ndata: b\n\n",
			expectedIdx: bytes.Index([]byte("data: a\r\n\r\ndata: b\n\n"), []byte("\r\n\r\n")),
			expectedSep: 4,
		},
		{
			name:        "incomplete event",
			buf:         "data: a\n",
			expectedIdx: -1,
			expectedSep: 0,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			idx, sepLen := indexSSEEventBoundary([]byte(tc.buf))
			require.Equal(t, tc.expectedIdx, idx)
			require.Equal(t, tc.expectedSep, sepLen)
		})
	}
}
