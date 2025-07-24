// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package fakeopenai

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"reflect"
	"slices"
	"strings"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
	"gopkg.in/yaml.v3" //nolint:depguard // sigs.k8s.io/yaml breaks Duration unmarshaling in cassettes
)

// config holds VCR configuration for handling sensitive headers and matching.
type config struct {
	// Headers to remove from requests before saving (e.g., Authorization).
	RequestHeadersToClear []string
	// Headers to remove from responses before saving (e.g., Set-Cookie).
	ResponseHeadersToClear []string
	// Headers to ignore when matching requests (e.g., tracing headers).
	HeadersToIgnoreForMatching []string
}

// defaultConfig returns the default configuration for OpenAI API testing.
func defaultConfig() config {
	return config{
		RequestHeadersToClear:      []string{"Authorization"},
		ResponseHeadersToClear:     []string{"Openai-Organization", "Set-Cookie"},
		HeadersToIgnoreForMatching: []string{"b3", "traceparent", "tracestate", "x-b3-traceid", "x-b3-spanid", "x-b3-sampled", "x-b3-parentspanid", "x-b3-flags"},
	}
}

// recorderOptions returns VCR recorder options configured for OpenAI API testing.
// It sets up request matching logic and post-processing hooks for cassette recordings.
func recorderOptions(cfg config) []recorder.Option {
	return []recorder.Option{
		// Allow replaying existing cassettes and recording new episodes when no match is found.
		recorder.WithMode(recorder.ModeReplayWithNewEpisodes),
		// Custom matcher to compare incoming requests with recorded cassettes.
		recorder.WithMatcher(requestMatcher(cfg)),
		// Hook to sanitize and format cassette data after recording.
		recorder.WithHook(afterCaptureHook(cfg), recorder.AfterCaptureHook),
	}
}

// requestMatcher creates a custom matcher function that compares HTTP requests with cassette recordings.
// It performs semantic comparison for JSON bodies and ignores specified headers like tracing headers.
func requestMatcher(cfg config) func(*http.Request, cassette.Request) bool {
	return func(httpReq *http.Request, cassReq cassette.Request) bool {
		// Basic method and URL matching.
		if httpReq.Method != cassReq.Method {
			return false
		}
		if httpReq.URL.String() != cassReq.URL {
			return false
		}

		// Compare headers, filtering out tracing headers that vary between runs.
		liveHeaders := filterHeaders(httpReq.Header, cfg.HeadersToIgnoreForMatching)
		cassetteHeaders := filterHeaders(cassReq.Headers, cfg.HeadersToIgnoreForMatching)
		if !reflect.DeepEqual(liveHeaders, cassetteHeaders) {
			return false
		}

		// Read and compare request bodies.
		var liveBody string
		if httpReq.Body != nil {
			b, err := io.ReadAll(httpReq.Body)
			if err != nil {
				return false
			}
			liveBody = string(b)
			// Restore body for the actual request.
			httpReq.Body = io.NopCloser(bytes.NewReader(b))
		}

		// For JSON content, do semantic comparison (ignores formatting/key order).
		if slices.Contains(httpReq.Header["Content-Type"], "application/json") {
			return matchJSONBodies(liveBody, cassReq.Body)
		}

		// For non-JSON content, do exact string comparison.
		return liveBody == cassReq.Body
	}
}

// afterCaptureHook creates a hook function that processes cassette
// interactions after recording. It removes sensitive data, decompresses
// responses, and pretty-prints JSON for readability.
func afterCaptureHook(cfg config) func(*cassette.Interaction) error {
	return func(i *cassette.Interaction) error {
		// Clear sensitive request headers like Authorization.
		for _, header := range cfg.RequestHeadersToClear {
			delete(i.Request.Headers, header)
		}

		// Pretty-print JSON request so the cassettes are readable.
		if slices.Contains(i.Request.Headers["Content-Type"], "application/json") {
			pretty, err := prettyPrintJSON(i.Request.Body)
			if err != nil {
				return err
			}
			i.Request.Body = pretty
		}
		i.Request.ContentLength = int64(len(i.Request.Body))

		// Clear sensitive response headers.
		for _, header := range cfg.ResponseHeadersToClear {
			delete(i.Response.Headers, header)
		}

		// Decompress gzipped responses rather than check in binary data.
		if slices.Contains(i.Response.Headers["Content-Encoding"], "gzip") {
			reader, err := gzip.NewReader(bytes.NewReader([]byte(i.Response.Body)))
			if err != nil {
				return fmt.Errorf("create gzip reader: %w", err)
			}
			decompressed, err := io.ReadAll(reader)
			if err != nil {
				return fmt.Errorf("decompress response body: %w", err)
			}
			if err := reader.Close(); err != nil {
				return fmt.Errorf("close gzip reader: %w", err)
			}
			i.Response.Body = string(decompressed)
			// Remove Content-Encoding header since body is no longer compressed.
			delete(i.Response.Headers, "Content-Encoding")
		}

		// Pretty-print JSON response so the cassettes are readable.
		if slices.Contains(i.Response.Headers["Content-Type"], "application/json") {
			pretty, err := prettyPrintJSON(i.Response.Body)
			if err != nil {
				return err
			}
			i.Response.Body = pretty
		}
		i.Response.ContentLength = int64(len(i.Response.Body))
		return nil
	}
}

// filterHeaders creates a new header map excluding specified headers.
// This is used to ignore headers that vary between test runs (like tracing headers).
func filterHeaders(headers http.Header, headersToIgnore []string) http.Header {
	filtered := make(http.Header)
	for k, vs := range headers {
		if slices.Contains(headersToIgnore, k) {
			continue
		}
		filtered[k] = vs
	}
	return filtered
}

// matchJSONBodies compares two JSON strings semantically (ignoring formatting/key order).
// This allows cassettes to match requests even if JSON keys are in different order.
func matchJSONBodies(liveBody, cassetteBody string) bool {
	var liveData, cassetteData any

	// Try to parse both as JSON.
	err1 := json.Unmarshal([]byte(liveBody), &liveData)
	err2 := json.Unmarshal([]byte(cassetteBody), &cassetteData)

	// If either fails to parse, fall back to exact string comparison.
	if err1 != nil || err2 != nil {
		return liveBody == cassetteBody
	}

	// Deep comparison handles nested objects and arrays.
	return reflect.DeepEqual(liveData, cassetteData)
}

// loadCassettes walks the cassettes directory and loads all YAML files.
// It returns a slice of cassettes ready for playback by the fake server.
func loadCassettes(recordings fs.FS) []*cassette.Cassette {
	var cassettes []*cassette.Cassette
	err := fs.WalkDir(recordings, "cassettes", func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Skip non-YAML files.
		if !strings.HasSuffix(path, ".yaml") {
			return nil
		}

		// Read and unmarshal cassette file.
		content, err := fs.ReadFile(recordings, path)
		if err != nil {
			return fmt.Errorf("read file %s: %w", path, err)
		}
		var c cassette.Cassette
		if err := yaml.Unmarshal(content, &c); err != nil {
			return fmt.Errorf("unmarshal %s: %w", path, err)
		}
		// Store the path as the cassette name for identification.
		c.Name = path
		cassettes = append(cassettes, &c)
		return nil
	})
	if err != nil {
		panic(fmt.Sprintf("failed to load cassettes: %v", err))
	}
	return cassettes
}

// prettyPrintJSON formats JSON for readability in cassette files.
// It indents the JSON and disables HTML escaping so characters like '>' remain as-is.
func prettyPrintJSON(body string) (string, error) {
	var data any
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		// Not valid JSON, return unchanged.
		return body, nil
	}

	// Use a buffer and encoder to control formatting.
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	// Disable HTML escaping to keep characters like '<', '>', '&' readable.
	encoder.SetEscapeHTML(false)
	// Indent with 2 spaces for readability.
	encoder.SetIndent("", "  ")

	if err := encoder.Encode(data); err != nil {
		return "", fmt.Errorf("marshal JSON: %w", err)
	}

	// Remove trailing newline added by Encode.
	result := strings.TrimSuffix(buf.String(), "\n")

	return result, nil
}
