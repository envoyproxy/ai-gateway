// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"testing"

	"github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
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

// TestBuildRequestMutations tests the buildRequestMutations function.
func TestBuildRequestMutations(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		reqBody     []byte
		wantHeaders []*corev3.HeaderValueOption
		wantBody    []byte
	}{
		{
			name:    "Path and Body provided",
			path:    "/v1/test",
			reqBody: []byte(`{"key":"value"}`),
			wantHeaders: []*corev3.HeaderValueOption{
				{
					Header: &corev3.HeaderValue{
						Key:      ":path",
						RawValue: []byte("/v1/test"),
					},
				},
				{
					Header: &corev3.HeaderValue{
						Key:      HTTPHeaderKeyContentLength,
						RawValue: []byte("15"),
					},
				},
			},
			wantBody: []byte(`{"key":"value"}`),
		},
		{
			name:    "Only Path provided",
			path:    "/v1/another-test",
			reqBody: []byte{},
			wantHeaders: []*corev3.HeaderValueOption{
				{
					Header: &corev3.HeaderValue{
						Key:      ":path",
						RawValue: []byte("/v1/another-test"),
					},
				},
			},
			wantBody: nil,
		},
		{
			name:    "Only Body provided",
			path:    "",
			reqBody: []byte("some body"),
			wantHeaders: []*corev3.HeaderValueOption{
				{
					Header: &corev3.HeaderValue{
						Key:      HTTPHeaderKeyContentLength,
						RawValue: []byte("9"),
					},
				},
			},
			wantBody: []byte("some body"),
		},
		{
			name:        "Neither Path nor Body provided",
			path:        "",
			reqBody:     []byte{},
			wantHeaders: nil,
			wantBody:    nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			headerMutation, bodyMutation := buildRequestMutations(tc.path, tc.reqBody)

			if tc.wantHeaders == nil {
				require.Nil(t, headerMutation)
			} else {
				require.NotNil(t, headerMutation)
				require.ElementsMatch(t, tc.wantHeaders, headerMutation.SetHeaders)
			}

			if tc.wantBody == nil {
				require.Nil(t, bodyMutation)
			} else {
				require.NotNil(t, bodyMutation)
				require.Equal(t, tc.wantBody, bodyMutation.GetBody())
			}
		})
	}
}

// TestSystemMsgToDeveloperMsg tests the systemMsgToDeveloperMsg function.
func TestSystemMsgToDeveloperMsg(t *testing.T) {
	systemMsg := openai.ChatCompletionSystemMessageParam{
		Name:    "test-system",
		Content: openai.StringOrArray{Value: "You are a helpful assistant."},
	}
	developerMsg := systemMsgToDeveloperMsg(systemMsg)
	require.Equal(t, "test-system", developerMsg.Name)
	require.Equal(t, openai.ChatMessageRoleDeveloper, developerMsg.Role)
	require.Equal(t, openai.StringOrArray{Value: "You are a helpful assistant."}, developerMsg.Content)
}

// TestProcessStopToStringPointers tests the ProcessStopToStringPointers helper function.
func TestProcessStopToStringPointers(t *testing.T) {
	makeStrPtrSlice := func(strs ...string) []*string {
		pointers := make([]*string, len(strs))
		for i, s := range strs {
			temp := s
			pointers[i] = &temp
		}
		return pointers
	}

	testCases := []struct {
		name        string
		input       interface{}
		expected    []*string
		expectError bool
	}{
		{
			name:        "Successful conversion from single string",
			input:       "stop-word",
			expected:    makeStrPtrSlice("stop-word"),
			expectError: false,
		},
		{
			name:        "Successful handling of string slice",
			input:       []string{"stop1", "stop2", "stop3"},
			expected:    makeStrPtrSlice("stop1", "stop2", "stop3"),
			expectError: false,
		},
		{
			name:        "Successful handling of slice of string pointers",
			input:       makeStrPtrSlice("ptr1", "ptr2"),
			expected:    makeStrPtrSlice("ptr1", "ptr2"),
			expectError: false,
		},
		{
			name:        "Handling a nil interface",
			input:       nil,
			expected:    nil,
			expectError: false,
		},
		{
			name:        "Handling an empty string slice",
			input:       []string{},
			expected:    []*string{},
			expectError: false,
		},
		{
			name:        "Failed conversion with an integer",
			input:       12345,
			expected:    nil,
			expectError: true,
		},
		{
			name:        "Failed conversion with a slice of integers",
			input:       []int{1, 2, 3},
			expected:    nil,
			expectError: true,
		},
	}

	areEqual := func(a, b []*string) bool {
		if len(a) != len(b) {
			return false
		}
		if a == nil && b == nil {
			return true
		}
		for i := range a {
			// Check for nil pointers before dereferencing.
			if (a[i] == nil && b[i] != nil) || (a[i] != nil && b[i] == nil) {
				return false
			}
			if a[i] != nil && b[i] != nil && *a[i] != *b[i] {
				return false
			}
		}
		return true
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := processStop(tc.input)
			if tc.expectError {
				if err == nil {
					t.Errorf("Expected an error, but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}

			if !areEqual(result, tc.expected) {
				t.Errorf("Result slice values do not match expected slice values")
			}
		})
	}
}
