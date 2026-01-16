// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	// Unset API keys to ensure tests behave consistently regardless of environment.
	_ = os.Unsetenv("OPENAI_API_KEY")
	_ = os.Unsetenv("AZURE_OPENAI_API_KEY")
	os.Exit(m.Run())
}

func TestServer_ExistingCassette(t *testing.T) {
	// Test that an existing cassette (chat-basic) works.
	server := newTestServer(t)
	defer server.Close()

	// Make the same request as in chat-basic cassette.
	req, err := NewRequest(t.Context(), server.URL(), CassetteChatBasic)
	require.NoError(t, err)

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	if resp.StatusCode != http.StatusOK {
		t.Logf("Response status: %d, body: %s", resp.StatusCode, string(body))
	}

	// Should get a successful response.
	require.Equal(t, http.StatusOK, resp.StatusCode)

	// Verify it's a valid OpenAI response.
	require.Contains(t, string(body), "chat.completion")
	require.Contains(t, string(body), "Hi there! How can I help you today?")
}

func TestServer_MissingCassette_NoAPIKey(t *testing.T) {
	// Ensure OPENAI_API_KEY is not set.
	t.Setenv("OPENAI_API_KEY", "")

	server := newTestServer(t)
	defer server.Close()

	// Make a request that won't match any cassette.
	reqBody := `{"model": "gpt-4", "messages": [{"role": "user", "content": "test"}]}`
	req, err := http.NewRequest("POST", server.URL()+"/v1/chat/completions", strings.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should get 500 with clear error message.
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.Equal(t, "true", resp.Header.Get("X-TestOpenAI-Error"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	errorMsg := string(body)
	require.Contains(t, errorMsg, "TestOpenAI Error:")
	require.Contains(t, errorMsg, "No cassette found")
	require.Contains(t, errorMsg, "X-Cassette-Name header")
}

func TestServer_MissingCassette_WithAPIKey(t *testing.T) {
	// Set a fake API key.
	t.Setenv("OPENAI_API_KEY", "test-key")

	server := newTestServer(t)
	defer server.Close()

	// Make a request that won't match any cassette.
	reqBody := `{"model": "gpt-4", "messages": [{"role": "user", "content": "test"}]}`
	req, err := http.NewRequest("POST", server.URL()+"/v1/chat/completions", strings.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should get 500 with clear error message about missing header.
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.Equal(t, "true", resp.Header.Get("X-TestOpenAI-Error"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	errorMsg := string(body)
	require.Contains(t, errorMsg, "TestOpenAI Error:")
	require.Contains(t, errorMsg, "No cassette found")
	require.Contains(t, errorMsg, CassetteNameHeader)
}

func TestServer_Recording(t *testing.T) {
	// Skip if no API key.
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("OPENAI_API_KEY not set, skipping recording test")
	}

	// Use temp directory for recording.
	tempDir := t.TempDir()
	server, err := newServer(io.Discard, 0, allVCRCassettes, tempDir)
	require.NoError(t, err)
	defer server.Close()

	// Make a request with cassette name header.
	reqBody := `{
  "model": "gpt-4o-mini",
  "messages": [
    {
      "role": "user",
      "content": "Say 'test passed' in exactly two words"
    }
  ]
}`

	req, err := http.NewRequest("POST", server.URL()+"/v1/chat/completions", strings.NewReader(reqBody))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(CassetteNameHeader, "test-recording")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should get a successful response.
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// Verify it's a valid OpenAI response.
	require.Contains(t, string(body), "chat.completion")

	// Verify cassette was created.
	cassettePath := filepath.Join(tempDir, "test-recording.yaml")
	require.FileExists(t, cassettePath)

	// Read and verify cassette content.
	cassetteContent, err := os.ReadFile(cassettePath)
	require.NoError(t, err)
	require.Contains(t, string(cassetteContent), "interactions:")
	require.NotContains(t, string(cassetteContent), "Authorization:") // Should be scrubbed.
}

func TestServer_DifferentEndpoints(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	// Test that we handle different endpoints correctly.
	endpoints := []string{
		"/v1/embeddings",
		"/v1/models",
		"/v1/completions",
	}

	for _, endpoint := range endpoints {
		req, err := http.NewRequest("GET", server.URL()+endpoint, nil)
		require.NoError(t, err)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		resp.Body.Close()

		// Should get error response since we don't have cassettes for these.
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
		require.Equal(t, "true", resp.Header.Get("X-TestOpenAI-Error"))
	}
}

func TestServer_JSONMatching(t *testing.T) {
	server := newTestServer(t)
	defer server.Close()

	// Make request with same content as chat-basic but different JSON formatting.
	// This tests that JSON matching works despite different formatting.
	reqBody := `{"model":"gpt-5-nano","messages":[{"role":"user","content":"Hello!"}]}`

	req, err := http.NewRequest("POST", server.URL()+"/v1/chat/completions", bytes.NewReader([]byte(reqBody)))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(CassetteNameHeader, CassetteChatBasic.String())

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should still match despite different formatting.
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

func newTestServer(t *testing.T) *Server {
	server, err := newServer(io.Discard, 0, allVCRCassettes, t.TempDir())
	require.NoError(t, err)
	return server
}
