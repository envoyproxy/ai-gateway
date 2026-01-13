// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"bytes"
	"compress/gzip"
	"embed"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
	"gopkg.in/yaml.v3" //nolint:depguard // sigs.k8s.io/yaml breaks Duration unmarshaling in cassettes

	"github.com/envoyproxy/ai-gateway/internal/json"
)

var (

	//go:embed cassettes
	embeddedCassettes embed.FS

	cassettesDir = func() string {
		_, file, _, ok := runtime.Caller(0)
		if !ok {
			panic("could not determine source file location")
		}
		return filepath.Join(filepath.Dir(file), "cassettes")
	}()

	allVCRCassettes = func() map[string]*cassette.Cassette {
		c, err := loadVCRCassettes(embeddedCassettes)
		if err != nil {
			panic(err)
		}
		return c
	}()

	// requestHeadersToRedact are sensitive or ephemeral headers to remove from requests and matching.
	requestHeadersToRedact = []string{
		"Authorization",
		"Api-Key",             // Azure OpenAI API key header
		"Openai-Organization", // OpenAI organization ID
		"Openai-Project",      // OpenAI project ID
		"Cookie",              // Session cookies
		"b3", "traceparent", "tracestate", "x-b3-traceid", "x-b3-spanid", "x-b3-sampled", "x-b3-parentspanid", "x-b3-flags",
	}
	// responseHeadersToRedact are sensitive or ephemeral headers to remove from responses before saving to cassettes.
	responseHeadersToRedact = []string{
		"Openai-Organization",
		"Set-Cookie",
	}
	recorderOptions = []recorder.Option{
		// Allow replaying existing cassettes and recording new episodes when no match is found.
		recorder.WithMode(recorder.ModeReplayWithNewEpisodes),
		// Custom matcher to compare incoming requests with recorded cassettes.
		recorder.WithMatcher(requestMatcher),
		// Hook to sanitize and format cassette data after recording.
		recorder.WithHook(afterCaptureHook, recorder.AfterCaptureHook),
	}
)

// requestMatcher creates a custom matcher function that compares HTTP requests with cassette recordings.
// It performs semantic comparison for JSON bodies and ignores specified headers like tracing headers.
func requestMatcher(httpReq *http.Request, cassReq cassette.Request) bool { // nolint:gocritic
	// Basic method and URL matching.
	if httpReq.Method != cassReq.Method {
		return false
	}
	if httpReq.URL.String() != cassReq.URL {
		return false
	}

	// Compare headers, filtering out tracing headers that vary between runs.
	liveHeaders := filterHeaders(httpReq.Header, requestHeadersToRedact)
	cassetteHeaders := filterHeaders(cassReq.Headers, requestHeadersToRedact)
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

// afterCaptureHook creates a hook function that processes cassette
// interactions after recording. It removes sensitive data, decompresses
// responses, and pretty-prints JSON for readability.
func afterCaptureHook(i *cassette.Interaction) error {
	// Scrub Azure endpoint URLs to remove private resource identifiers.
	if isAzureURL(i.Request.URL) {
		model := extractModelFromBody(i.Request.Body)
		if model == "" {
			return fmt.Errorf("cannot scrub Azure URL: model field missing or empty in request body")
		}
		i.Request.URL = scrubAzureURL(i.Request.URL, model)
		i.Request.Host = "resource-name" + azureHostnameSuffix
	}

	// Clear sensitive request headers like Authorization and api-key.
	// Use case-insensitive deletion since HTTP headers are case-insensitive.
	for _, headerToRedact := range requestHeadersToRedact {
		for k := range i.Request.Headers {
			if strings.EqualFold(k, headerToRedact) {
				delete(i.Request.Headers, k)
			}
		}
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
	// Update Content-Length header to match the actual body size.
	if i.Request.Headers != nil {
		i.Request.Headers["Content-Length"] = []string{fmt.Sprintf("%d", len(i.Request.Body))}
	}

	// Clear sensitive response headers (case-insensitive).
	for _, headerToRedact := range responseHeadersToRedact {
		for k := range i.Response.Headers {
			if strings.EqualFold(k, headerToRedact) {
				delete(i.Response.Headers, k)
			}
		}
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
	// Update Content-Length header to match the actual body size.
	if i.Response.Headers != nil {
		i.Response.Headers["Content-Length"] = []string{fmt.Sprintf("%d", len(i.Response.Body))}
	}
	return nil
}

// filterHeaders creates a new header map excluding specified headers.
// This is used to ignore headers that vary between test runs (like tracing headers).
func filterHeaders(headers http.Header, headersToIgnore []string) http.Header {
	filtered := make(http.Header)
	for k, vs := range headers {
		// Case-insensitive header matching
		shouldIgnore := false
		for _, ignore := range headersToIgnore {
			if strings.EqualFold(k, ignore) {
				shouldIgnore = true
				break
			}
		}
		if shouldIgnore {
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

// loadVCRCassettes walks the cassettes directory and loads all YAML files.
// It returns a slice of cassettes ready for playback by the fake server.
func loadVCRCassettes(cassettesFS fs.FS) (map[string]*cassette.Cassette, error) {
	cassettes := map[string]*cassette.Cassette{}
	err := fs.WalkDir(cassettesFS, "cassettes", func(path string, _ fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		// Skip non-YAML files.
		if !strings.HasSuffix(path, ".yaml") {
			return nil
		}

		// Read and unmarshal cassette file.
		content, err := fs.ReadFile(cassettesFS, path)
		if err != nil {
			return fmt.Errorf("read file %s: %w", path, err)
		}
		var c cassette.Cassette
		if err := yaml.Unmarshal(content, &c); err != nil {
			return fmt.Errorf("unmarshal %s: %w", path, err)
		}
		// Store the path as the cassette name for identification.
		c.Name = path
		name := strings.TrimPrefix(path, "cassettes/")
		name = strings.TrimSuffix(name, ".yaml")
		cassettes[name] = &c
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("failed to load cassettes: %w", err)
	}
	return cassettes, nil
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
