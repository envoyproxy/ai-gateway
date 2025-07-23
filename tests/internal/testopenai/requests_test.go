// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewRequest(t *testing.T) {
	// Start test OpenAI server.
	server := newTestServer(t)
	defer server.Close()

	baseURL := server.URL()

	// Test matrix with all cassettes and their expected status.
	tests := []struct {
		cassetteName   Cassette
		expectedStatus int
	}{
		{
			cassetteName:   CassetteChatBasic,
			expectedStatus: http.StatusOK,
		},
		{
			cassetteName:   CassetteChatStreaming,
			expectedStatus: http.StatusOK,
		},
		{
			cassetteName:   CassetteChatTools,
			expectedStatus: http.StatusOK,
		},
		{
			cassetteName:   CassetteChatMultimodal,
			expectedStatus: http.StatusOK,
		},
		{
			cassetteName:   CassetteChatMultiturn,
			expectedStatus: http.StatusOK,
		},
		{
			cassetteName:   CassetteChatJSONMode,
			expectedStatus: http.StatusOK,
		},
		{
			cassetteName:   CassetteChatNoMessages,
			expectedStatus: http.StatusBadRequest,
		},
		{
			cassetteName:   CassetteChatParallelTools,
			expectedStatus: http.StatusOK,
		},
		{
			cassetteName:   CassetteChatBadRequest,
			expectedStatus: http.StatusBadRequest,
		},
		{
			cassetteName:   CassetteChatImageToText,
			expectedStatus: http.StatusOK,
		},
		{
			cassetteName:   CassetteChatUnknownModel,
			expectedStatus: http.StatusNotFound,
		},
		{
			cassetteName:   CassetteChatReasoning,
			expectedStatus: http.StatusOK,
		},
		{
			cassetteName:   CassetteChatTextToImageTool,
			expectedStatus: http.StatusOK,
		},
		{
			cassetteName:   CassetteChatAudioToText,
			expectedStatus: http.StatusOK,
		},
		{
			cassetteName:   CassetteChatTextToAudio,
			expectedStatus: http.StatusOK,
		},
		{
			cassetteName:   CassetteChatDetailedUsage,
			expectedStatus: http.StatusOK,
		},
		{
			cassetteName:   CassetteChatStreamingDetailedUsage,
			expectedStatus: http.StatusOK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.cassetteName.String(), func(t *testing.T) {
			// Create request using NewRequest.
			req, err := NewRequest(baseURL, tc.cassetteName)
			require.NoError(t, err, "NewRequest should succeed for known cassette")

			// Verify the request is properly formed.
			require.Equal(t, http.MethodPost, req.Method)
			require.Equal(t, baseURL+"/chat/completions", req.URL.String())
			require.Equal(t, "application/json", req.Header.Get("Content-Type"))
			require.Equal(t, tc.cassetteName.String(), req.Header.Get(CassetteNameHeader))

			// Verify request body matches expected from requestBodies map.
			body, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			expectedBody, ok := requestBodies[tc.cassetteName]
			require.True(t, ok, "Should have request body for cassette %s", tc.cassetteName)
			expectedJSON, err := json.Marshal(expectedBody)
			require.NoError(t, err)
			require.JSONEq(t, string(expectedJSON), string(body), "Request body should match expected for %s", tc.cassetteName)

			// Actually send the request to verify it works with the fake server.
			req, err = NewRequest(baseURL, tc.cassetteName) // Recreate since we consumed the body.
			require.NoError(t, err)

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, err = io.ReadAll(resp.Body)
			require.NoError(t, err)

			// Verify the expected status code.
			require.Equal(t, tc.expectedStatus, resp.StatusCode,
				"Expected status %d for %s, got %d: %s",
				tc.expectedStatus, tc.cassetteName, resp.StatusCode, body)
		})
	}

	// Test error case - unknown cassette.
	t.Run("unknown-cassette", func(t *testing.T) {
		_, err := NewRequest(baseURL, Cassette(999))
		require.Error(t, err)
		require.Contains(t, err.Error(), "unknown cassette name")
	})
}

// TestCassetteCompleteness ensures all cassette constants have corresponding request bodies.
func TestCassetteCompleteness(t *testing.T) {
	// Get all cassette constants.
	cassettes := ChatCassettes()

	// Verify each cassette has a request body.
	for _, cassette := range cassettes {
		t.Run(cassette.String(), func(t *testing.T) {
			body, ok := requestBodies[cassette]
			require.True(t, ok, "Cassette %s should have a request body", cassette)
			require.NotEmpty(t, body, "Request body for cassette %s should not be empty", cassette)
		})
	}

	// Verify we have the same number of entries.
	require.Len(t, requestBodies, len(cassettes),
		"Number of cassette constants should match number of request bodies")
}
