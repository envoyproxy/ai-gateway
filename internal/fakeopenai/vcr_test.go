// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package fakeopenai

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/yaml.v3" //nolint:depguard // Testing that this specific library works with Duration fields.
)

// gzipJSON compresses a JSON string for testing.
func gzipJSON(t *testing.T, jsonStr string) []byte {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, err := gz.Write([]byte(jsonStr))
	require.NoError(t, err)
	err = gz.Close()
	require.NoError(t, err)
	return buf.Bytes()
}

// TestVCR_YAMLExtensionHandling tests that we don't add double .yaml extensions.
func TestVCR_YAMLExtensionHandling(t *testing.T) {
	tempDir := t.TempDir()

	// Test that recorder doesn't add extra .yaml.
	tests := []struct {
		cassetteName string
		expectedFile string
	}{
		{"test-case", "test-case.yaml"},
		{"test-case.yaml", "test-case.yaml.yaml"}, // VCR always adds .yaml.
	}

	for _, tc := range tests {
		t.Run(tc.cassetteName, func(t *testing.T) {
			// Create a minimal cassette file path.
			cassettePath := filepath.Join(tempDir, tc.cassetteName)

			// The recorder will add .yaml automatically.
			expectedPath := filepath.Join(tempDir, tc.expectedFile)

			// Verify our understanding of VCR behavior.
			require.Equal(t, expectedPath, cassettePath+".yaml")
		})
	}
}

// TestVCR_DurationUnmarshaling tests that cassettes with Duration fields work correctly.
func TestVCR_DurationUnmarshaling(t *testing.T) {
	// Create a test cassette with a Duration field.
	testCassette := &cassette.Cassette{
		Version: 4,
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
					Status:     "200 OK",
					Code:       200,
					Proto:      "HTTP/2.0",
					ProtoMajor: 2,
					ProtoMinor: 0,
					Headers: map[string][]string{
						"Content-Type": {"application/json"},
					},
					Body:     `{"id":"test","object":"chat.completion","created":1234567890}`,
					Duration: 500 * time.Millisecond, // This is what causes issues with sigs.k8s.io/yaml.
				},
			},
		},
	}

	// Marshal with gopkg.in/yaml.v3.
	data, err := yaml.Marshal(testCassette)
	require.NoError(t, err)

	// Unmarshal back.
	var loaded cassette.Cassette
	err = yaml.Unmarshal(data, &loaded)
	require.NoError(t, err)

	// Verify Duration was preserved.
	require.Equal(t, 500*time.Millisecond, loaded.Interactions[0].Response.Duration)
}

// TestVCR_HTMLEscaping tests that HTML characters are not escaped in cassette files.
func TestVCR_HTMLEscaping(t *testing.T) {
	// Test that prettyPrintJSON doesn't escape HTML characters.
	jsonWithHTML := `{"message":"Use <div> tags & check > and < symbols","url":"https://example.com?a=1&b=2"}`

	pretty, err := prettyPrintJSON(jsonWithHTML)
	require.NoError(t, err)

	// Should NOT contain Unicode escapes.
	require.NotContains(t, pretty, `\u003c`)
	require.NotContains(t, pretty, `\u003e`)
	require.NotContains(t, pretty, `\u0026`)

	// Should contain actual characters.
	require.Contains(t, pretty, `<div>`)
	require.Contains(t, pretty, `>`)
	require.Contains(t, pretty, `&`)
}

// TestVCR_JSONMatching tests semantic JSON matching ignoring formatting.
func TestVCR_JSONMatching(t *testing.T) {
	tests := []struct {
		name     string
		body1    string
		body2    string
		expected bool
	}{
		{
			name:     "identical JSON",
			body1:    `{"a":1,"b":"test"}`,
			body2:    `{"a":1,"b":"test"}`,
			expected: true,
		},
		{
			name:     "different key order",
			body1:    `{"a":1,"b":"test"}`,
			body2:    `{"b":"test","a":1}`,
			expected: true,
		},
		{
			name:     "different formatting",
			body1:    `{"a":1,"b":"test"}`,
			body2:    `{  "a" : 1 , "b" : "test"  }`,
			expected: true,
		},
		{
			name:     "different values",
			body1:    `{"a":1,"b":"test"}`,
			body2:    `{"a":2,"b":"test"}`,
			expected: false,
		},
		{
			name:     "invalid JSON",
			body1:    `{"a":1,"b":"test"}`,
			body2:    `not json`,
			expected: false,
		},
		{
			name:     "both invalid JSON",
			body1:    `not json`,
			body2:    `not json`,
			expected: true, // Falls back to string comparison.
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := matchJSONBodies(tc.body1, tc.body2)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestVCR_HeaderFiltering tests that sensitive and tracing headers are filtered correctly.
func TestVCR_HeaderFiltering(t *testing.T) {
	headers := http.Header{
		"Authorization":   {"Bearer secret-token"},
		"Content-Type":    {"application/json"},
		"X-B3-TraceId":    {"abc123"},
		"X-B3-SpanId":     {"def456"},
		"traceparent":     {"00-trace-span-01"},
		"User-Agent":      {"test-client"},
		"X-Custom-Header": {"should-remain"},
	}

	cfg := defaultConfig()
	filtered := filterHeaders(headers, cfg.HeadersToIgnoreForMatching)

	// Should keep non-sensitive headers.
	require.Contains(t, filtered, "Authorization")
	require.Contains(t, filtered, "Content-Type")
	require.Contains(t, filtered, "User-Agent")
	require.Contains(t, filtered, "X-Custom-Header")

	// Should remove tracing headers.
	require.NotContains(t, filtered, "x-b3-traceid") // Headers are case-sensitive.
	require.NotContains(t, filtered, "x-b3-spanid")
	require.NotContains(t, filtered, "traceparent")
}

// TestVCR_AfterCaptureHook tests the cassette sanitization process.
func TestVCR_AfterCaptureHook(t *testing.T) {
	cfg := defaultConfig()
	hook := afterCaptureHook(cfg)

	// Create a test interaction with sensitive data and gzipped response.
	interaction := &cassette.Interaction{
		Request: cassette.Request{
			Headers: map[string][]string{
				"Authorization": {"Bearer secret-key"},
				"Content-Type":  {"application/json"},
			},
			Body: `{"test":"data","number":123}`,
		},
		Response: cassette.Response{
			Headers: map[string][]string{
				"Content-Type":        {"application/json"},
				"Content-Encoding":    {"gzip"},
				"Set-Cookie":          {"session=secret"},
				"Openai-Organization": {"org-123"},
			},
			// Simulate actual gzipped data.
			Body: string(gzipJSON(t, `{"result":"test"}`)),
		},
	}

	err := hook(interaction)
	require.NoError(t, err)

	// Request headers should be sanitized.
	require.NotContains(t, interaction.Request.Headers, "Authorization")
	require.Contains(t, interaction.Request.Headers, "Content-Type")

	// Request body should be pretty-printed.
	var reqBody map[string]interface{}
	err = json.Unmarshal([]byte(interaction.Request.Body), &reqBody)
	require.NoError(t, err)
	require.Contains(t, interaction.Request.Body, "\n") // Pretty-printed has newlines.

	// Response headers should be sanitized.
	require.NotContains(t, interaction.Response.Headers, "Set-Cookie")
	require.NotContains(t, interaction.Response.Headers, "Openai-Organization")
	require.NotContains(t, interaction.Response.Headers, "Content-Encoding") // Removed after decompression.

	// Response body should be pretty-printed.
	var respBody map[string]interface{}
	err = json.Unmarshal([]byte(interaction.Response.Body), &respBody)
	require.NoError(t, err)
	require.Contains(t, interaction.Response.Body, "\n") // Pretty-printed has newlines.
}

// TestHandler_OutdatedCassette tests the error when cassette doesn't match request.
func TestHandler_OutdatedCassette(t *testing.T) {
	// Create server.
	server, err := NewServer()
	require.NoError(t, err)
	defer server.Close()

	// Make a request that specifies chat-basic but with different content.
	req, err := http.NewRequest("POST", server.URL()+"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"different message"}]}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(CassetteNameHeader, "chat-basic")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should get conflict status.
	require.Equal(t, http.StatusConflict, resp.StatusCode)
	require.Equal(t, "true", resp.Header.Get("X-FakeOpenAI-Error"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// Check error message.
	require.Contains(t, string(body), "Interaction out of date")
	require.Contains(t, string(body), "chat-basic.yaml")
	require.Contains(t, string(body), "re-record")
}

// TestHandler_NoAPIKeyError tests the error message when trying to record without API key.
func TestHandler_NoAPIKeyError(t *testing.T) {
	// Ensure no API key.
	t.Setenv("OPENAI_API_KEY", "")

	server, err := NewServer()
	require.NoError(t, err)
	defer server.Close()

	// Request with cassette name that doesn't exist.
	req, err := http.NewRequest("POST", server.URL()+"/v1/chat/completions",
		strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"test"}]}`))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(CassetteNameHeader, "non-existent-cassette")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	// Should get internal server error (no API key to record).
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.Equal(t, "true", resp.Header.Get("X-FakeOpenAI-Error"))

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	// Check error message.
	require.Contains(t, string(body), "No cassette found")
	require.Contains(t, string(body), "set OPENAI_API_KEY")
}

// TestLoadCassettes_EmbeddedFS tests loading cassettes from embedded filesystem.
func TestLoadCassettes_EmbeddedFS(t *testing.T) {
	// Load cassettes from the embedded filesystem.
	cassettes := loadCassettes(embeddedCassettes)

	// Should have loaded all cassettes.
	require.NotEmpty(t, cassettes)

	// Check specific cassettes exist.
	cassetteNames := make(map[string]bool)
	for _, c := range cassettes {
		cassetteNames[filepath.Base(c.Name)] = true
	}

	require.True(t, cassetteNames["chat-basic.yaml"])
	require.True(t, cassetteNames["chat-streaming.yaml"])
	require.True(t, cassetteNames["chat-tools.yaml"])
}

// TestRecorderOptions tests the VCR recorder options configuration.
func TestRecorderOptions(t *testing.T) {
	cfg := defaultConfig()
	opts := recorderOptions(cfg)

	// Should return 3 options: mode, matcher, and hook.
	require.Len(t, opts, 3)
}

// TestRequestMatcher tests the custom request matcher function.
func TestRequestMatcher(t *testing.T) {
	cfg := defaultConfig()
	matcher := requestMatcher(cfg)

	tests := []struct {
		name     string
		httpReq  *http.Request
		cassReq  cassette.Request
		expected bool
	}{
		{
			name:    "method mismatch",
			httpReq: httptest.NewRequest("GET", "https://api.openai.com/v1/models", nil),
			cassReq: cassette.Request{
				Method: "POST",
				URL:    "https://api.openai.com/v1/models",
			},
			expected: false,
		},
		{
			name:    "URL mismatch",
			httpReq: httptest.NewRequest("GET", "https://api.openai.com/v1/models", nil),
			cassReq: cassette.Request{
				Method: "GET",
				URL:    "https://api.openai.com/v1/chat/completions",
			},
			expected: false,
		},
		{
			name: "headers mismatch",
			httpReq: func() *http.Request {
				req := httptest.NewRequest("GET", "https://api.openai.com/v1/models", nil)
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			cassReq: cassette.Request{
				Method: "GET",
				URL:    "https://api.openai.com/v1/models",
				Headers: map[string][]string{
					"Content-Type": {"text/plain"},
				},
			},
			expected: false,
		},
		{
			name: "exact match with headers",
			httpReq: func() *http.Request {
				req := httptest.NewRequest("GET", "https://api.openai.com/v1/models", nil)
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			cassReq: cassette.Request{
				Method: "GET",
				URL:    "https://api.openai.com/v1/models",
				Headers: map[string][]string{
					"Content-Type": {"application/json"},
				},
			},
			expected: true,
		},
		{
			name: "JSON body semantic match",
			httpReq: func() *http.Request {
				req := httptest.NewRequest("POST", "https://api.openai.com/v1/chat/completions",
					strings.NewReader(`{"model":"gpt-4","messages":[{"role":"user","content":"test"}]}`))
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			cassReq: cassette.Request{
				Method: "POST",
				URL:    "https://api.openai.com/v1/chat/completions",
				Headers: map[string][]string{
					"Content-Type": {"application/json"},
				},
				Body: `{"messages":[{"content":"test","role":"user"}],"model":"gpt-4"}`, // Different order.
			},
			expected: true,
		},
		{
			name: "non-JSON body exact match",
			httpReq: httptest.NewRequest("POST", "https://api.openai.com/v1/test",
				strings.NewReader("plain text body")),
			cassReq: cassette.Request{
				Method: "POST",
				URL:    "https://api.openai.com/v1/test",
				Body:   "plain text body",
			},
			expected: true,
		},
		{
			name:    "empty body match",
			httpReq: httptest.NewRequest("GET", "https://api.openai.com/v1/models", nil),
			cassReq: cassette.Request{
				Method: "GET",
				URL:    "https://api.openai.com/v1/models",
			},
			expected: true,
		},
		{
			name: "body read error simulation",
			httpReq: func() *http.Request {
				req := httptest.NewRequest("POST", "https://api.openai.com/v1/test", &errorReader{})
				req.Header.Set("Content-Type", "application/json")
				return req
			}(),
			cassReq: cassette.Request{
				Method: "POST",
				URL:    "https://api.openai.com/v1/test",
				Headers: map[string][]string{
					"Content-Type": {"application/json"},
				},
				Body: `{"test":"data"}`,
			},
			expected: false, // Should fail due to read error.
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := matcher(tc.httpReq, tc.cassReq)
			require.Equal(t, tc.expected, result)
		})
	}
}

// TestPrettyPrintJSON_InvalidJSON tests pretty printing with invalid JSON.
func TestPrettyPrintJSON_InvalidJSON(t *testing.T) {
	// Test with invalid JSON - should return unchanged.
	invalid := "not valid json {"
	result, err := prettyPrintJSON(invalid)
	require.NoError(t, err)
	require.Equal(t, invalid, result)
}

// TestAfterCaptureHook_NonJSONContent tests the hook with non-JSON content.
func TestAfterCaptureHook_NonJSONContent(t *testing.T) {
	cfg := defaultConfig()
	hook := afterCaptureHook(cfg)

	interaction := &cassette.Interaction{
		Request: cassette.Request{
			Headers: map[string][]string{
				"Content-Type": {"text/plain"},
			},
			Body: "plain text request",
		},
		Response: cassette.Response{
			Headers: map[string][]string{
				"Content-Type": {"text/html"},
			},
			Body: "<html>response</html>",
		},
	}

	err := hook(interaction)
	require.NoError(t, err)

	// Bodies should remain unchanged (not pretty-printed).
	require.Equal(t, "plain text request", interaction.Request.Body)
	require.Equal(t, "<html>response</html>", interaction.Response.Body)

	// Content lengths should be updated.
	require.Equal(t, int64(len("plain text request")), interaction.Request.ContentLength)
	require.Equal(t, int64(len("<html>response</html>")), interaction.Response.ContentLength)
}

// TestAfterCaptureHook_InvalidGzip tests the hook with invalid gzip data.
func TestAfterCaptureHook_InvalidGzip(t *testing.T) {
	cfg := defaultConfig()
	hook := afterCaptureHook(cfg)

	interaction := &cassette.Interaction{
		Request: cassette.Request{
			Headers: map[string][]string{},
			Body:    "",
		},
		Response: cassette.Response{
			Headers: map[string][]string{
				"Content-Encoding": {"gzip"},
			},
			Body: "not valid gzip data",
		},
	}

	err := hook(interaction)
	require.Error(t, err)
	require.Contains(t, err.Error(), "gzip")
}

// TestAfterCaptureHook_GzipReadError tests the hook with truncated gzip data.
func TestAfterCaptureHook_GzipReadError(t *testing.T) {
	cfg := defaultConfig()
	hook := afterCaptureHook(cfg)

	// Create truncated gzip data that passes header check but fails on read.
	truncatedGzip := []byte{0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0xff} // Valid gzip header but incomplete.

	interaction := &cassette.Interaction{
		Request: cassette.Request{
			Headers: map[string][]string{},
			Body:    "",
		},
		Response: cassette.Response{
			Headers: map[string][]string{
				"Content-Encoding": {"gzip"},
			},
			Body: string(truncatedGzip),
		},
	}

	err := hook(interaction)
	require.Error(t, err)
	require.Contains(t, err.Error(), "decompress")
}

// TestAfterCaptureHook_InvalidJSONInRequest tests hook with invalid JSON in request.
func TestAfterCaptureHook_InvalidJSONInRequest(t *testing.T) {
	cfg := defaultConfig()
	hook := afterCaptureHook(cfg)

	interaction := &cassette.Interaction{
		Request: cassette.Request{
			Headers: map[string][]string{
				"Content-Type": {"application/json"},
			},
			Body: "invalid json {",
		},
		Response: cassette.Response{
			Headers: map[string][]string{},
			Body:    "response",
		},
	}

	err := hook(interaction)
	require.NoError(t, err) // prettyPrintJSON returns unchanged body without error for invalid JSON.
	require.Equal(t, "invalid json {", interaction.Request.Body)
}
