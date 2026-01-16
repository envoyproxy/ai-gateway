// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"bytes"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"

	"github.com/envoyproxy/ai-gateway/internal/json"
)

var noopLogger = log.New(io.Discard, "[testopenai] ", 0)

// TestSplitSSEEvents tests the SSE event splitting logic.
func TestSplitSSEEvents(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected []string
	}{
		{
			name:  "single event",
			input: "data: {\"message\": \"hello\"}",
			expected: []string{
				"data: {\"message\": \"hello\"}",
			},
		},
		{
			name:  "multiple events with blank lines",
			input: "data: {\"chunk\": 1}\n\ndata: {\"chunk\": 2}\n\ndata: [DONE]\n\n",
			expected: []string{
				"data: {\"chunk\": 1}",
				"data: {\"chunk\": 2}",
				"data: [DONE]",
			},
		},
		{
			name:  "events with multiple fields",
			input: "event: message\ndata: {\"text\": \"hello\"}\n\nevent: close\ndata: [DONE]\n\n",
			expected: []string{
				"event: message\ndata: {\"text\": \"hello\"}",
				"event: close\ndata: [DONE]",
			},
		},
		{
			name:     "empty input",
			input:    "",
			expected: nil,
		},
		{
			name:  "trailing content without double newline",
			input: "data: {\"message\": \"incomplete\"}",
			expected: []string{
				"data: {\"message\": \"incomplete\"}",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := splitSSEEvents(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestRecordNewInteraction tests the recording functionality with a mock server.
func TestRecordNewInteraction(t *testing.T) {
	// Create a mock OpenAI server.
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request.
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.Equal(t, "Bearer fake-api-key", r.Header.Get("Authorization"))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Return a mock response.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"id": "chatcmpl-123",
			"object": "chat.completion",
			"created": 1677652288,
			"model": "gpt-3.5-turbo",
			"choices": [{
				"index": 0,
				"message": {"role": "assistant", "content": "test"},
				"finish_reason": "stop"
			}],
			"usage": {"prompt_tokens": 10, "completion_tokens": 1, "total_tokens": 11}
		}`))
	}))
	defer mockServer.Close()

	tempDir := t.TempDir()
	handler := &cassetteHandler{
		logger:       noopLogger,
		apiBase:      mockServer.URL + "/v1",
		cassettesDir: tempDir,
		apiKey:       "fake-api-key",
		cassettes:    map[string]*cassette.Cassette{},
	}

	// Create a test request.
	body := `{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"Say 'test' and nothing else"}],"max_tokens":10}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(CassetteNameHeader, "test-recording")

	// Record the response.
	w := httptest.NewRecorder()
	err := handler.recordNewInteraction(req, []byte(body), w, "test-recording")
	require.NoError(t, err)

	// Check that response was received.
	resp := w.Result()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// Parse and verify the JSON response.
	var respData map[string]any
	err = json.Unmarshal(respBody, &respData)
	require.NoError(t, err)
	require.Equal(t, "chatcmpl-123", respData["id"])
	require.Equal(t, "chat.completion", respData["object"])

	// Check that cassette was saved.
	cassettePath := filepath.Join(tempDir, "test-recording.yaml")
	require.FileExists(t, cassettePath)
}

// TestRecordNewInteraction_Errors tests error handling in recordNewInteraction.
func TestRecordNewInteraction_Errors(t *testing.T) {
	// Test directory creation error - use a read-only path.
	handler := &cassetteHandler{
		logger:       noopLogger,
		apiBase:      "https://api.openai.com/v1",
		cassettesDir: "/dev/null/cannot-create-dir",
		apiKey:       "test-key",
		cassettes:    map[string]*cassette.Cassette{},
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
	w := httptest.NewRecorder()

	err := handler.recordNewInteraction(req, []byte("{}"), w, "test")
	require.Error(t, err)
	require.Equal(t, "failed to create cassettes directory: mkdir /dev/null: not a directory", err.Error())
}

// TestRecordNewInteraction_ServerError tests handling of server errors.
func TestRecordNewInteraction_ServerError(t *testing.T) {
	// Create a mock server that returns an error.
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error": {"message": "Internal server error", "type": "server_error"}}`))
	}))
	defer mockServer.Close()

	tempDir := t.TempDir()
	handler := &cassetteHandler{
		logger:       noopLogger,
		apiBase:      mockServer.URL + "/v1",
		cassettesDir: tempDir,
		apiKey:       "test-key",
		cassettes:    map[string]*cassette.Cassette{},
	}

	body := `{"model":"gpt-3.5-turbo","messages":[{"role":"user","content":"test"}]}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(CassetteNameHeader, "error-test")

	w := httptest.NewRecorder()
	err := handler.recordNewInteraction(req, []byte(body), w, "error-test")
	require.NoError(t, err) // The function should still succeed even with server error.

	// Check that error response was passed through.
	resp := w.Result()
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	respBody, _ := io.ReadAll(resp.Body)
	var errResp map[string]any
	err = json.Unmarshal(respBody, &errResp)
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"error": map[string]any{
			"message": "Internal server error",
			"type":    "server_error",
		},
	}, errResp)

	// Verify cassette was still saved.
	cassettePath := filepath.Join(tempDir, "error-test.yaml")
	require.FileExists(t, cassettePath)
}

// TestRecordNewInteraction_WithHeaders tests that headers are properly copied.
func TestRecordNewInteraction_WithHeaders(t *testing.T) {
	var capturedHeaders http.Header
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Request-ID", "test-123")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id": "test", "object": "chat.completion"}`))
	}))
	defer mockServer.Close()

	tempDir := t.TempDir()
	handler := &cassetteHandler{
		logger:       noopLogger,
		apiBase:      mockServer.URL + "/v1",
		cassettesDir: tempDir,
		apiKey:       "test-key",
		cassettes:    map[string]*cassette.Cassette{},
	}

	body := `{"model":"gpt-3.5-turbo"}`
	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "test-client")
	req.Header.Set("X-Custom-Header", "custom-value")
	req.Header.Set(CassetteNameHeader, "header-test")

	w := httptest.NewRecorder()
	err := handler.recordNewInteraction(req, []byte(body), w, "header-test")
	require.NoError(t, err)

	// Verify headers were passed to the server (except X-Cassette-Name).
	require.Equal(t, "Bearer test-key", capturedHeaders.Get("Authorization"))
	require.Equal(t, "application/json", capturedHeaders.Get("Content-Type"))
	require.Equal(t, "test-client", capturedHeaders.Get("User-Agent"))
	require.Equal(t, "custom-value", capturedHeaders.Get("X-Custom-Header"))
	require.Empty(t, capturedHeaders.Get(CassetteNameHeader))

	// Verify response headers were passed back.
	resp := w.Result()
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))
	require.Equal(t, "test-123", resp.Header.Get("X-Request-ID"))
}

// TestRecordNewInteraction_NoAPIKey tests error when trying to record without API key.
func TestRecordNewInteraction_NoAPIKey(t *testing.T) {
	handler := &cassetteHandler{
		logger:       noopLogger,
		apiBase:      "https://api.openai.com/v1",
		cassettesDir: t.TempDir(),
		apiKey:       "", // No API key.
		cassettes:    map[string]*cassette.Cassette{},
	}

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader("{}"))
	req.Header.Set(CassetteNameHeader, "test")

	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	expected := "TestOpenAI Error: No cassette found for POST /v1/chat/completions. To record OpenAI cassettes, set OPENAI_API_KEY environment variable and provide X-Cassette-Name header.\n"
	require.Equal(t, expected, w.Body.String())
}

// TestGetHeaderValue tests the header value extraction function.
func TestGetHeaderValue(t *testing.T) {
	tests := []struct {
		name     string
		headers  map[string][]string
		key      string
		expected string
	}{
		{
			name: "existing header",
			headers: map[string][]string{
				"Content-Type": {"application/json"},
			},
			key:      "Content-Type",
			expected: "application/json",
		},
		{
			name: "multiple values",
			headers: map[string][]string{
				"Accept": {"application/json", "text/plain"},
			},
			key:      "Accept",
			expected: "application/json",
		},
		{
			name:     "missing header",
			headers:  map[string][]string{},
			key:      "Missing",
			expected: "",
		},
		{
			name: "empty values",
			headers: map[string][]string{
				"Empty": {},
			},
			key:      "Empty",
			expected: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := getHeaderValue(tc.headers, tc.key)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestMatchRequest_EdgeCases tests edge cases in request matching.
func TestMatchRequest_EdgeCases(t *testing.T) {
	// Test body read error simulation is tricky with standard http.Request.
	// Instead test other edge cases.
	h := &cassetteHandler{
		logger:     noopLogger,
		apiBase:    "https://api.openai.com/v1",
		serverBase: "https://api.openai.com",
	}

	tests := []struct {
		name     string
		req      *http.Request
		cassReq  cassette.Request
		expected bool
	}{
		{
			name: "method mismatch",
			req:  httptest.NewRequest("GET", "/v1/test", nil),
			cassReq: cassette.Request{
				Method: "POST",
				URL:    "https://api.openai.com/v1/test",
			},
			expected: false,
		},
		{
			name: "path mismatch",
			req:  httptest.NewRequest("POST", "/v1/test", nil),
			cassReq: cassette.Request{
				Method: "POST",
				URL:    "https://api.openai.com/v1/different",
			},
			expected: false,
		},
		{
			name: "non-JSON exact match",
			req:  httptest.NewRequest("POST", "/v1/test", strings.NewReader("plain text")),
			cassReq: cassette.Request{
				Method: "POST",
				URL:    "https://api.openai.com/v1/test",
				Body:   "plain text",
			},
			expected: true,
		},
		{
			name: "non-JSON mismatch",
			req:  httptest.NewRequest("POST", "/v1/test", strings.NewReader("text1")),
			cassReq: cassette.Request{
				Method: "POST",
				URL:    "https://api.openai.com/v1/test",
				Body:   "text2",
			},
			expected: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Normalize URL as in ServeHTTP.
			pathForAPI := strings.TrimPrefix(tc.req.URL.Path, "/v1")
			u, err := url.Parse(h.apiBase + pathForAPI)
			require.NoError(t, err)
			u.RawQuery = tc.req.URL.RawQuery
			tc.req.URL = u

			body, err := io.ReadAll(tc.req.Body)
			require.NoError(t, err)
			tc.req.Body = io.NopCloser(bytes.NewReader(body))

			result := h.matchRequest(tc.req, tc.cassReq, body, "test")
			require.Equal(t, tc.expected, result)
		})
	}
}

// errorReader simulates a body that fails to read.
type errorReader struct{}

func (*errorReader) Read([]byte) (n int, err error) {
	return 0, fmt.Errorf("read error")
}

// TestServeHTTP_ComplexScenarios tests more complex handler scenarios.
func TestServeHTTP_ComplexScenarios(t *testing.T) {
	// Create a handler with some test cassettes.
	handler := &cassetteHandler{
		logger:  noopLogger,
		apiBase: "https://api.openai.com/v1",
		cassettes: map[string]*cassette.Cassette{
			"test-cassette": {
				Name: "test-cassette",
				Interactions: []*cassette.Interaction{
					{
						Request: cassette.Request{
							Method: "POST",
							URL:    "https://api.openai.com/v1/chat/completions",
							Headers: map[string][]string{
								"Content-Type": {"application/json"},
							},
							Body: `{"model":"gpt-4","messages":[{"role":"user","content":"test"}]}`,
						},
						Response: cassette.Response{
							Code: 200,
							Headers: map[string][]string{
								"Content-Type": {"application/json"},
							},
							Body: `{"id":"test-id","object":"chat.completion"}`,
						},
					},
				},
			},
			"named-with-path": {
				Name: "cassettes/named-with-path.yaml",
				Interactions: []*cassette.Interaction{
					{
						Request: cassette.Request{
							Method: "GET",
							URL:    "https://api.openai.com/v1/models",
						},
						Response: cassette.Response{
							Code: 200,
							Body: `{"data":[]}`,
						},
					},
				},
			},
		},
		apiKey: "",
	}

	tests := []struct {
		name           string
		method         string
		path           string
		body           string
		cassetteName   string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "no cassette header - no match",
			method:         "GET",
			path:           "/v1/unknown",
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "No cassette found",
		},
		{
			name:           "cassette name without extension",
			method:         "POST",
			path:           "/v1/chat/completions",
			body:           `{"model":"gpt-4","messages":[{"role":"user","content":"test"}]}`,
			cassetteName:   "test-cassette",
			expectedStatus: http.StatusOK,
			expectedBody:   `{"id":"test-id","object":"chat.completion"}`,
		},
		{
			name:           "cassette name with path",
			method:         "GET",
			path:           "/v1/models",
			cassetteName:   "named-with-path",
			expectedStatus: http.StatusOK,
			expectedBody:   `{"data":[]}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			if tc.cassetteName != "" {
				req.Header.Set(CassetteNameHeader, tc.cassetteName)
			}

			w := httptest.NewRecorder()
			handler.ServeHTTP(w, req)

			require.Equal(t, tc.expectedStatus, w.Code)
			if tc.expectedStatus == http.StatusOK {
				require.Equal(t, tc.expectedBody, w.Body.String())
			} else {
				// For error cases, check that the error message contains the expected text.
				require.Contains(t, w.Body.String(), tc.expectedBody)
			}
		})
	}
}

// TestServeHTTP_BodyReadError tests handling of body read errors.
func TestServeHTTP_BodyReadError(t *testing.T) {
	handler := &cassetteHandler{
		logger:    noopLogger,
		apiBase:   "https://api.openai.com/v1",
		cassettes: map[string]*cassette.Cassette{},
	}

	// Create a request with a body that fails on read.
	req := httptest.NewRequest("POST", "/v1/test", &errorReader{})
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Equal(t, "TestOpenAI Error: Failed to read request body: read error\n", w.Body.String())
}

// TestWriteResponse_ComplexHeaders tests writing responses with multiple header values.
func TestWriteResponse_ComplexHeaders(t *testing.T) {
	interaction := &cassette.Interaction{
		Response: cassette.Response{
			Code: 201,
			Headers: map[string][]string{
				"Content-Type": {"application/json"},
				"Set-Cookie": {
					"session=abc123; Path=/",
					"preference=dark; Path=/",
				},
				"X-Custom": {"value1", "value2"},
			},
			Body: `{"status":"created"}`,
		},
	}

	w := httptest.NewRecorder()
	writeResponse(w, interaction)

	resp := w.Result()
	require.Equal(t, 201, resp.StatusCode)
	require.Equal(t, "application/json", resp.Header.Get("Content-Type"))

	// Check multiple header values.
	cookies := resp.Header["Set-Cookie"]
	require.Len(t, cookies, 2)
	require.Equal(t, "session=abc123; Path=/", cookies[0])
	require.Equal(t, "preference=dark; Path=/", cookies[1])

	customs := resp.Header["X-Custom"]
	require.Len(t, customs, 2)
	require.Equal(t, "value1", customs[0])
	require.Equal(t, "value2", customs[1])

	body, _ := io.ReadAll(resp.Body)
	require.JSONEq(t, `{"status":"created"}`, string(body))
}
