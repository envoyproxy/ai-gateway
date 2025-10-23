// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package autoconfig

import (
	"bytes"
	_ "embed"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"text/template"
)

//go:embed config.yaml.tmpl
var configTemplate string

// Backend represents a network backend endpoint (OpenAI or MCP server).
// Backends are rendered as Kubernetes Backend resources with optional TLS policy.
type Backend struct {
	Name             string // Backend resource name (e.g., "openai", "github")
	Hostname         string // Hostname for Backend endpoint (modified from "localhost"/"127.0.0.1" to "127.0.0.1.nip.io" for Docker/K8s compatibility)
	OriginalHostname string // Original unmodified hostname for TLS certificate validation (e.g., "localhost" when Hostname is "127.0.0.1.nip.io")
	Port             int    // Port number
	NeedsTLS         bool   // Whether to generate BackendTLSPolicy resource
}

// OpenAIConfig holds OpenAI-specific configuration for generating AIServiceBackend resources.
// This is nil when no OpenAI configuration is present (MCP-only mode).
type OpenAIConfig struct {
	BackendName    string // References a Backend.Name (typically "openai")
	SchemaName     string // Schema name: "OpenAI" or "AzureOpenAI"
	Version        string // API version (OpenAI path prefix or Azure query param version)
	OrganizationID string // Optional OpenAI-Organization header value
	ProjectID      string // Optional OpenAI-Project header value
}

// AnthropicConfig holds Anthropic-specific configuration for generating AIServiceBackend resources.
// This is nil when no Anthropic configuration is present.
type AnthropicConfig struct {
	BackendName string // References a Backend.Name (typically "anthropic")
	SchemaName  string // Schema name: "Anthropic"
	Version     string // API version (Anthropic path prefix)
}

// MCPBackendRef references a backend with MCP-specific routing configuration.
// Used to generate MCPRoute backendRefs with path, tool filtering, and authentication.
type MCPBackendRef struct {
	BackendName  string            // References a Backend.Name
	Path         string            // MCP endpoint path
	IncludeTools []string          // Only the specified tools will be available
	APIKey       string            // Optional API key extracted from Authorization: Bearer header
	Headers      map[string]string // Optional arbitrary headers for headerMutation (excluding Authorization)
}

// ConfigData holds all template data for generating the AI Gateway configuration.
// It supports OpenAI-only, Anthropic-only, MCP-only, or combined configurations.
type ConfigData struct {
	Backends       []Backend        // All backend endpoints (unified - includes OpenAI, Anthropic, and MCP backends)
	OpenAI         *OpenAIConfig    // OpenAI-specific configuration (nil when not present)
	Anthropic      *AnthropicConfig // Anthropic-specific configuration (nil when not present)
	MCPBackendRefs []MCPBackendRef  // MCP routing configuration (nil/empty for OpenAI-only or Anthropic-only mode)
	Debug          bool             // Enable debug logging for Envoy (includes component-level logging for ext_proc, http, connection)
	EnvoyVersion   string           // Explicitly configure the version of Envoy to use.
}

// WriteConfig generates the AI Gateway configuration.
func WriteConfig(data *ConfigData) (string, error) {
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

// parsedURL holds parsed URL components for creating Backend and OpenAIConfig.
type parsedURL struct {
	hostname         string
	originalHostname string
	port             int
	version          string
	needsTLS         bool
}

// parseURL extracts hostname, port, and version from the base URL.
func parseURL(baseURL string) (*parsedURL, error) {
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
	portStr := u.Port()
	var port int
	if portStr == "" {
		switch u.Scheme {
		case "https":
			port = 443
		case "http":
			port = 80
		default:
			return nil, fmt.Errorf("invalid OPENAI_BASE_URL: unsupported scheme %q", u.Scheme)
		}
	} else {
		var err error
		port, err = strconv.Atoi(portStr)
		if err != nil {
			return nil, fmt.Errorf("invalid port in OPENAI_BASE_URL: %w", err)
		}
	}

	// Extract version from path
	// Strip leading slash and use the entire path as version
	version := strings.TrimPrefix(u.Path, "/")
	// For cleaner output, omit version field when it's just "v1"
	if version == "v1" {
		version = ""
	}

	return &parsedURL{
		hostname:         hostname,
		originalHostname: originalHostname,
		port:             port,
		version:          version,
		needsTLS:         u.Scheme == "https",
	}, nil
}
