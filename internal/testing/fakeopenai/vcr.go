// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package fakeopenai

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"reflect"
	"slices"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
	"sigs.k8s.io/yaml"
)

// config defines the configuration for VCR recording behavior.
type config struct {
	// RequestHeadersToClear contains sensitive headers that should be removed from recorded requests.
	RequestHeadersToClear []string
	// ResponseHeadersToClear contains headers that should be removed from recorded responses.
	ResponseHeadersToClear []string
	// HeadersToIgnoreForMatching contains headers that vary between test runs
	// and should be ignored when matching requests to recordings.
	HeadersToIgnoreForMatching []string
}

// defaultConfig returns the default configuration for OpenAI VCR recording.
func defaultConfig() config {
	return config{
		RequestHeadersToClear: []string{
			"Authorization", // Contains API keys that should not be stored.
		},
		ResponseHeadersToClear: []string{
			"Openai-Organization", // May contain sensitive organization data.
			"Set-Cookie",          // Contains ephemeral session data.
		},
		HeadersToIgnoreForMatching: []string{
			// Trace propagation headers contain randomly generated IDs.
			"b3", "traceparent", "tracestate",
			"x-b3-traceid", "x-b3-spanid", "x-b3-sampled",
			"x-b3-parentspanid", "x-b3-flags",
		},
	}
}

// recorderOptions returns recorder options using the provided VCR configuration.
func recorderOptions(config config) []recorder.Option {
	return []recorder.Option{
		// ModeReplayWithNewEpisodes allows both replay of existing cassettes.
		// and recording of new interactions when they don't match existing ones.
		recorder.WithMode(recorder.ModeReplayWithNewEpisodes),
		// AfterCaptureHook runs after recording to sanitize sensitive data.
		recorder.WithHook(func(i *cassette.Interaction) error {
			// Clear sensitive request headers.
			for _, header := range config.RequestHeadersToClear {
				delete(i.Request.Headers, header)
			}

			// Pretty print JSON bodies for legibility in cassette files.
			if slices.Contains(i.Request.Headers["Content-Type"], "application/json") {
				i.Request.ContentLength = -1
				body := unmarshalJSON(i.Request.Body)
				b, err := json.MarshalIndent(body, "", "  ")
				if err != nil {
					panic(err)
				}
				i.Request.Body = string(b)
			}

			// Clear sensitive response headers.
			for _, header := range config.ResponseHeadersToClear {
				delete(i.Response.Headers, header)
			}

			return nil
		}, recorder.AfterCaptureHook),
	}
}

// loadCassettes loads all cassettes from the given file system.
func loadCassettes(recordings fs.FS) []*cassette.Cassette {
	var cassettes []*cassette.Cassette

	if err := fs.WalkDir(recordings, "cassettes", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		content, err := fs.ReadFile(recordings, path)
		if err != nil {
			return fmt.Errorf("read file %s: %w", path, err)
		}
		var c cassette.Cassette
		if err := yaml.Unmarshal(content, &c); err != nil {
			return fmt.Errorf("unmarshal %s: %w", path, err)
		}
		// Set the cassette name based on the file path.
		c.Name = path
		cassettes = append(cassettes, &c)
		return nil
	}); err != nil {
		panic(fmt.Sprintf("failed to load cassettes: %v", err))
	}

	return cassettes
}

// unmarshalJSON is a helper to parse JSON for semantic comparison.
func unmarshalJSON(s string) map[string]any {
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		return nil
	}
	return m
}

// matchJSONBodies does semantic comparison of JSON bodies.
func matchJSONBodies(body1, body2 string) bool {
	obj1 := unmarshalJSON(body1)
	obj2 := unmarshalJSON(body2)

	if obj1 == nil || obj2 == nil {
		return body1 == body2
	}

	return reflect.DeepEqual(obj1, obj2)
}
