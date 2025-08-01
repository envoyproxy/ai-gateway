// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

// CassetteNameHeader is the header used to specify which cassette to use for matching.
const CassetteNameHeader = "X-Cassette-Name"

type cassetteHandler struct {
	logger       *log.Logger
	apiBase      string
	cassettes    map[string]*cassette.Cassette
	cassettesDir string
	apiKey       string
	// requestHeadersToRedact contains headers to not emit when logging request matching errors.
	requestHeadersToRedact map[string]struct{}
}

// ServeHTTP implements http.Handler by matching requests against recorded cassettes.
func (h *cassetteHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	for k, v := range r.Header {
		h.logger.Printf("header %q: %s\n", k, v)
	}
	// Read the request body.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.logAndSendError(w, http.StatusInternalServerError, "Failed to read request body: %v", err)
		return
	}
	// Restore the body for potential future use.
	r.Body = io.NopCloser(bytes.NewReader(body))

	// Normalize the request URL to match cassette format, including query parameters.
	originalPath := r.URL.Path
	pathForAPI := strings.TrimPrefix(r.URL.Path, "/v1")
	u, err := url.Parse(h.apiBase + pathForAPI)
	if err != nil {
		h.logAndSendError(w, http.StatusInternalServerError, "Failed to parse URL: %v", err)
		return
	}
	u.RawQuery = r.URL.RawQuery
	r.URL = u

	// Check if a specific cassette is requested.
	cassetteName := r.Header.Get(CassetteNameHeader)
	if cassetteName != "" {
		c := h.cassettes[cassetteName]
		if c != nil {
			for _, interaction := range c.Interactions {
				if h.matchRequest(r, interaction.Request, body) {
					writeResponse(w, interaction)
					h.logger.Println("response sent")
					return
				}
			}
			// We found the cassette but no matching interaction.
			cassetteFile := cassetteName + ".yaml"
			h.logAndSendError(w, http.StatusConflict,
				"Interaction out of date for %s %s. To re-record, delete %s/%s and re-run with OPENAI_API_KEY set.",
				r.Method, originalPath, h.cassettesDir, cassetteFile)
			return
		}

		// No matching cassette found.
		if h.apiKey == "" {
			// No API key - can't record.
			h.logAndSendError(w, http.StatusInternalServerError,
				"No cassette found for %s %s. To record new cassettes, set OPENAI_API_KEY environment variable and provide %s header.",
				r.Method, originalPath, CassetteNameHeader)

			return
		}

		// We have an API key and cassette name - record the interaction.
		err = h.recordNewInteraction(r, body, w, cassetteName)
		if err != nil {
			h.logAndSendError(w, http.StatusInternalServerError, "Failed to record interaction: %v", err)
		}
		return
	}

	// No specific cassette requested - try to find a match in all cassettes.
	for _, c := range h.cassettes {
		for _, interaction := range c.Interactions {
			if h.matchRequest(r, interaction.Request, body) {
				// Found a match! Return the recorded response.
				writeResponse(w, interaction)
				return
			}
		}
	}

	h.logAndSendError(w, http.StatusInternalServerError,
		"No cassette found for %s %s. To record a new cassette, include the %s header with the cassette name.\n%s",
		r.Method, originalPath, CassetteNameHeader, h.formatRequestDetails(r, body))
}

func (h *cassetteHandler) logAndSendError(w http.ResponseWriter, code int, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	log.Println(msg)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-TestOpenAI-Error", "true")
	w.WriteHeader(code)
	fmt.Fprintf(w, "TestOpenAI Error: "+format+"\n", a...)
}

// formatRequestDetails formats the incoming request to make it easier to see what was wrong.
func (h *cassetteHandler) formatRequestDetails(r *http.Request, body []byte) string {
	var b strings.Builder

	b.WriteString("\n--- Actual Request Details ---\n")
	b.WriteString("Method:      " + r.Method + "\n")
	b.WriteString("Path:        " + r.URL.Path + "\n")
	b.WriteString("Query:       " + r.URL.RawQuery + "\n")

	b.WriteString("\nHeaders:\n")
	for key, values := range r.Header {
		if _, ok := h.requestHeadersToRedact[strings.ToLower(key)]; ok {
			b.WriteString("  " + key + ": [REDACTED]\n")
		} else {
			for _, value := range values {
				b.WriteString("  " + key + ": " + value + "\n")
			}
		}
	}

	b.WriteString("\nBody:\n")
	if len(body) == 0 {
		b.WriteString("  <empty>\n")
	} else {
		// Pretty print if it looks like JSON.
		bodyStr := string(body)
		if strings.TrimSpace(bodyStr) != "" &&
			(strings.HasPrefix(strings.TrimSpace(bodyStr), "{") ||
				strings.HasPrefix(strings.TrimSpace(bodyStr), "[")) {
			b.WriteString("  " + bodyStr + "\n")
		} else {
			b.WriteString("  " + strconv.Quote(bodyStr) + "\n")
		}
	}

	b.WriteString("\n--- End Request Details ---\n")

	return b.String()
}

// recordNewInteraction attempts to make a real API call and record the response.
func (h *cassetteHandler) recordNewInteraction(r *http.Request, body []byte, w http.ResponseWriter, cassetteName string) error {
	// Ensure cassettes directory exists.
	if err := os.MkdirAll(h.cassettesDir, 0o755); err != nil {
		return fmt.Errorf("failed to create cassettes directory: %w", err)
	}

	// Create cassette path. The VCR recorder will add .yaml extension automatically.
	cassettePath := filepath.Join(h.cassettesDir, cassetteName)

	rec, err := recorder.New(cassettePath, recorderOptions...)
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

	// TODO: flush between SSE events.
	_, _ = w.Write([]byte(interaction.Response.Body))
}

// matchRequest checks if an HTTP request matches a cassette request.
func (h *cassetteHandler) matchRequest(r *http.Request, i cassette.Request, body []byte) bool {
	// Match method.
	if r.Method != i.Method {
		return false
	}

	// Match full URL (including query parameters).
	if i.URL != r.URL.String() {
		return false
	}

	// For JSON requests, do semantic comparison.
	if isJSON(r.Header.Get("Content-Type")) || isJSON(getHeaderValue(i.Headers, "Content-Type")) {
		return matchJSONBodies(string(body), i.Body)
	}

	// For non-JSON, exact match.
	return string(body) == i.Body
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
