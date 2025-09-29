// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package envgen

import (
	"bytes"
	_ "embed"
	"fmt"
	"net/url"
	"os"
	"strings"
	"text/template"
)

//go:embed config.yaml.tmpl
var configTemplate string

// ConfigData holds the template data for generating the configuration.
type ConfigData struct {
	Hostname         string // Hostname for the backend (may be modified for localhost)
	OriginalHostname string // Original hostname for TLS validation
	Port             string // Port number as string
	Version          string // API version path prefix (empty for "v1" to keep output clean)
	NeedsTLS         bool   // Whether to generate BackendTLSPolicy (port 443)
}

// GenerateOpenAIConfig generates the AI Gateway configuration for a single
// OpenAI-compatible backend, using standard OpenAI SDK environment variables.
//
// This errs if OPENAI_API_KEY is not set.
//
// See https://github.com/openai/openai-python/blob/main/src/openai/_client.py
func GenerateOpenAIConfig() (string, error) {
	// Check for required API key
	if os.Getenv("OPENAI_API_KEY") == "" {
		return "", fmt.Errorf("OPENAI_API_KEY environment variable is required")
	}

	// Get base URL with default
	baseURL := os.Getenv("OPENAI_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	// Parse URL to extract components
	data, err := parseURL(baseURL)
	if err != nil {
		return "", err
	}

	// Parse and execute template
	tmpl, err := template.New("config").Parse(configTemplate)
	if err != nil {
		return "", fmt.Errorf("failed to parse template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute template: %w", err)
	}

	return buf.String(), nil
}

// parseURL extracts hostname, port, and version from the base URL.
func parseURL(baseURL string) (*ConfigData, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid OPENAI_BASE_URL: %w", err)
	}

	// Extract hostname
	hostname := u.Hostname()
	if hostname == "" {
		return nil, fmt.Errorf("invalid OPENAI_BASE_URL: missing hostname")
	}
	originalHostname := hostname

	// Convert localhost/127.0.0.1 to nip.io for Docker/K8s compatibility
	if hostname == "localhost" || hostname == "127.0.0.1" {
		hostname = "127.0.0.1.nip.io"
	}

	// Determine port
	port := u.Port()
	if port == "" {
		switch u.Scheme {
		case "https":
			port = "443"
		case "http":
			port = "80"
		default:
			return nil, fmt.Errorf("invalid OPENAI_BASE_URL: unsupported scheme %q", u.Scheme)
		}
	}

	// Extract version from path
	// Strip leading slash and use the entire path as version
	version := strings.TrimPrefix(u.Path, "/")
	// For cleaner output, omit version field when it's just "v1"
	if version == "v1" {
		version = ""
	}

	return &ConfigData{
		Hostname:         hostname,
		OriginalHostname: originalHostname,
		Port:             port,
		Version:          version,
		NeedsTLS:         u.Scheme == "https",
	}, nil
}
