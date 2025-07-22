// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package fakeopenai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

// CassetteNameHeader is the header used to specify which cassette to use for matching.
const CassetteNameHeader = "X-Cassette-Name"

type cassetteHandler struct {
	apiBase      string
	cassettes    []*cassette.Cassette
	cassettesDir string
	apiKey       string
}

// ServeHTTP implements http.Handler by matching requests against recorded cassettes.
func (h *cassetteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Normalize the request URL to match cassette format.
	originalPath := r.URL.Path
	// Strip /v1 prefix if present since apiBase already includes it.
	pathForAPI := strings.TrimPrefix(r.URL.Path, "/v1")
	r.URL, _ = url.Parse(h.apiBase + pathForAPI)

	// Read the request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.errorResponse(w, http.StatusInternalServerError, "Failed to read request body: %v", err)
		return
	}
	// Restore the body for matching.
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Check if a specific cassette is requested.
	cassetteName := r.Header.Get(CassetteNameHeader)
	if cassetteName != "" {
		// Match against interactions from the specific cassette only.
		// Note: We load all cassettes but filter by name when X-Cassette-Name is provided.
		for _, c := range h.cassettes {
			// Check if this cassette matches the requested name.
			// Cassettes are loaded with paths like "cassettes/name.yaml".
			cassetteNameWithExt := cassetteName + ".yaml"
			if c.Name == cassetteName || c.Name == cassetteNameWithExt ||
				strings.HasSuffix(c.Name, "/"+cassetteName) || strings.HasSuffix(c.Name, "/"+cassetteNameWithExt) {
				for _, interaction := range c.Interactions {
					if matchRequest(r, interaction.Request) {
						writeResponse(w, interaction)
						return
					}
				}
				// We found the cassette but no matching interaction.
				cassetteFile := cassetteName
				if !strings.HasSuffix(cassetteFile, ".yaml") {
					cassetteFile += ".yaml"
				}
				h.errorResponse(w, http.StatusConflict,
					"Interaction out of date for %s %s. To re-record, delete cassettes/%s and re-run with OPENAI_API_KEY set.",
					r.Method, originalPath, cassetteFile)
				return
			}
		}

		// No matching cassette found.
		if h.apiKey == "" {
			// No API key - can't record.
			h.errorResponse(w, http.StatusInternalServerError,
				"No cassette found for %s %s. To record new cassettes, set OPENAI_API_KEY environment variable and provide %s header.",
				r.Method, originalPath, CassetteNameHeader)
			return
		}

		// We have an API key and cassette name - record the interaction.
		err = h.recordNewInteraction(r, body, w, cassetteName)
		if err != nil {
			h.errorResponse(w, http.StatusInternalServerError,
				"Failed to record interaction: %v", err)
		}
		return
	}

	// No specific cassette requested - try to find a match in all cassettes.
	for _, c := range h.cassettes {
		for _, interaction := range c.Interactions {
			if matchRequest(r, interaction.Request) {
				// Found a match! Return the recorded response.
				writeResponse(w, interaction)
				return
			}
		}
	}

	h.errorResponse(w, http.StatusBadRequest,
		"No cassette found for %s %s. To record a new cassette, include the %s header with the cassette name.",
		r.Method, originalPath, CassetteNameHeader)
}

// errorResponse sends a clearly marked error response.
func (h *cassetteHandler) errorResponse(w http.ResponseWriter, code int, format string, args ...interface{}) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-FakeOpenAI-Error", "true")
	w.WriteHeader(code)
	fmt.Fprintf(w, "FakeOpenAI Error: "+format+"\n", args...)
}

// recordNewInteraction attempts to make a real API call and record the response.
func (h *cassetteHandler) recordNewInteraction(r *http.Request, body []byte, w http.ResponseWriter, cassetteName string) error {
	// Ensure cassettes directory exists.
	if err := os.MkdirAll(h.cassettesDir, 0o755); err != nil {
		return fmt.Errorf("failed to create cassettes directory: %w", err)
	}

	// Create cassette path. The VCR recorder will add .yaml extension automatically.
	cassettePath := filepath.Join(h.cassettesDir, cassetteName)

	opts := recorderOptions(defaultConfig())
	rec, err := recorder.New(cassettePath, opts...)
	if err != nil {
		return fmt.Errorf("failed to create recorder: %w", err)
	}
	defer func() {
		if stopErr := rec.Stop(); stopErr != nil && err == nil {
			err = fmt.Errorf("failed to stop recorder: %w", stopErr)
		}
	}()

	// Create a new request to the real API using the configured base URL.
	// Strip /v1 prefix if present since apiBase already includes it.
	pathForAPI := strings.TrimPrefix(r.URL.Path, "/v1")
	targetURL := h.apiBase + pathForAPI
	if r.URL.RawQuery != "" {
		targetURL += "?" + r.URL.RawQuery
	}
	req, err := http.NewRequestWithContext(context.Background(), r.Method, targetURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Copy headers (except our custom header) and add authorization.
	for k, v := range r.Header {
		if k != CassetteNameHeader {
			req.Header[k] = v
		}
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", h.apiKey))

	// Make the request using the recording transport.
	client := &http.Client{Transport: rec}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Copy the response to the client.
	for k, v := range resp.Header {
		w.Header()[k] = v
	}
	w.WriteHeader(resp.StatusCode)

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if _, err := w.Write(respBody); err != nil {
		return fmt.Errorf("failed to write response: %w", err)
	}

	return nil
}

// writeResponse writes a cassette interaction response to the HTTP response writer.
func writeResponse(w http.ResponseWriter, interaction *cassette.Interaction) {
	// Write response headers.
	for key, values := range interaction.Response.Headers {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Write status code.
	w.WriteHeader(interaction.Response.Code)

	// Write response body.
	_, _ = w.Write([]byte(interaction.Response.Body))
}

// matchRequest checks if an HTTP request matches a cassette request.
func matchRequest(r *http.Request, i cassette.Request) bool {
	// Match method.
	if r.Method != i.Method {
		return false
	}

	// Match URL path (cassettes use full URL, but we only care about path).
	if !strings.HasSuffix(i.URL, r.URL.Path) {
		return false
	}

	// Read request body for comparison.
	rBodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		return false
	}
	// Restore body.
	r.Body = io.NopCloser(bytes.NewReader(rBodyBytes))

	// For JSON requests, do semantic comparison.
	if isJSON(r.Header.Get("Content-Type")) || isJSON(getHeaderValue(i.Headers, "Content-Type")) {
		return matchJSONBodies(string(rBodyBytes), i.Body)
	}

	// For non-JSON, exact match.
	return string(rBodyBytes) == i.Body
}

// isJSON checks if a content type indicates JSON.
func isJSON(contentType string) bool {
	return strings.Contains(contentType, "application/json")
}

// getHeaderValue gets the first value for a header key from a map.
func getHeaderValue(headers map[string][]string, key string) string {
	if values, ok := headers[key]; ok && len(values) > 0 {
		return values[0]
	}
	return ""
}
