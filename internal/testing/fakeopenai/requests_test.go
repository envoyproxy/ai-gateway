// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package fakeopenai

import (
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewRequest(t *testing.T) {
	// Start fake OpenAI server.
	server, err := NewServer()
	require.NoError(t, err)
	defer server.Close()

	baseURL := server.URL()

	// Test matrix with all cassettes and their expected status.
	tests := []struct {
		cassetteName   string
		expectedStatus int
		description    string
	}{
		{
			cassetteName:   CassetteChatBasic,
			expectedStatus: http.StatusOK,
			description:    "Basic chat completion request",
		},
		{
			cassetteName:   CassetteChatStreaming,
			expectedStatus: http.StatusOK,
			description:    "Streaming chat completion request",
		},
		{
			cassetteName:   CassetteChatTools,
			expectedStatus: http.StatusOK,
			description:    "Chat completion with function tools",
		},
		{
			cassetteName:   CassetteChatMultimodal,
			expectedStatus: http.StatusOK,
			description:    "Multimodal chat with text and image",
		},
		{
			cassetteName:   CassetteChatMultiturn,
			expectedStatus: http.StatusOK,
			description:    "Multi-turn conversation with history",
		},
		{
			cassetteName:   CassetteChatJSONMode,
			expectedStatus: http.StatusBadRequest,
			description:    "JSON mode request (should fail without 'json' in message)",
		},
		{
			cassetteName:   CassetteChatNoMessages,
			expectedStatus: http.StatusBadRequest,
			description:    "Request missing required messages field",
		},
		{
			cassetteName:   CassetteChatBadModel,
			expectedStatus: http.StatusNotFound,
			description:    "Request with invalid model name",
		},
		{
			cassetteName:   CassetteChatParallelTools,
			expectedStatus: http.StatusOK,
			description:    "Parallel function calling enabled",
		},
		{
			cassetteName:   CassetteEdgeBadRequest,
			expectedStatus: http.StatusBadRequest,
			description:    "Request with multiple validation errors",
		},
		{
			cassetteName:   CassetteEdgeBase64Image,
			expectedStatus: http.StatusOK,
			description:    "Request with base64-encoded image",
		},
		{
			cassetteName:   CassetteChatUnknownModel,
			expectedStatus: http.StatusNotFound,
			description:    "Request with non-existent model",
		},
	}

	for _, tc := range tests {
		t.Run(tc.cassetteName, func(t *testing.T) {
			// Create request using NewRequest.
			req, err := NewRequest(baseURL, tc.cassetteName)
			require.NoError(t, err, "NewRequest should succeed for known cassette")

			// Verify the request is properly formed.
			require.Equal(t, http.MethodPost, req.Method)
			require.Equal(t, baseURL+"/chat/completions", req.URL.String())
			require.Equal(t, "application/json", req.Header.Get("Content-Type"))
			require.Equal(t, tc.cassetteName, req.Header.Get("X-Cassette-Name"))

			// Verify request body matches expected from requestBodies map.
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			expectedBody, ok := requestBodies[tc.cassetteName]
			require.True(t, ok, "Should have request body for cassette %s", tc.cassetteName)
			require.JSONEq(t, expectedBody, string(body), "Request body should match expected for %s", tc.cassetteName)

			// Actually send the request to verify it works with the fake server.
			req, err = NewRequest(baseURL, tc.cassetteName) // Recreate since we consumed the body.
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			// Verify expected status code.
			require.Equal(t, tc.expectedStatus, resp.StatusCode,
				"Expected status %d for %s (%s), got %d",
				tc.expectedStatus, tc.cassetteName, tc.description, resp.StatusCode)
		})
	}

	// Test error case - unknown cassette.
	t.Run("unknown-cassette", func(t *testing.T) {
		_, err := NewRequest(baseURL, "non-existent-cassette")
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown cassette name")
	})
}

// TestCassetteCompleteness ensures all cassette constants have corresponding request bodies.
func TestCassetteCompleteness(t *testing.T) {
	// List of all cassette constants.
	cassettes := []string{
		CassetteChatBadModel,
		CassetteChatBasic,
		CassetteChatJSONMode,
		CassetteChatMultimodal,
		CassetteChatMultiturn,
		CassetteChatNoMessages,
		CassetteChatParallelTools,
		CassetteChatStreaming,
		CassetteChatTools,
		CassetteChatUnknownModel,
		CassetteEdgeBadRequest,
		CassetteEdgeBase64Image,
	}

	// Verify each cassette has a request body.
	for _, cassette := range cassettes {
		t.Run(cassette, func(t *testing.T) {
			body, ok := requestBodies[cassette]
			require.True(t, ok, "Cassette %s should have a request body", cassette)
			require.NotEmpty(t, body, "Request body for cassette %s should not be empty", cassette)
		})
	}

	// Verify we have the same number of entries.
	require.Len(t, requestBodies, len(cassettes),
		"Number of cassette constants should match number of request bodies")
}
