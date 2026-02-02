// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package autoconfig

import (
	"fmt"
	"os"
)

// PopulateAnthropicEnvConfig populates ConfigData with Anthropic backend configuration
// from standard Anthropic SDK environment variables.
//
// This errs if ANTHROPIC_API_KEY is not set.
//
// See https://docs.anthropic.com/en/api/client-sdks
func PopulateAnthropicEnvConfig(data *ConfigData) error {
	if data == nil {
		return fmt.Errorf("ConfigData cannot be nil")
	}

	// Check for Anthropic API key
	anthropicAPIKey := os.Getenv("ANTHROPIC_API_KEY")
	if anthropicAPIKey == "" {
		return fmt.Errorf("ANTHROPIC_API_KEY environment variable is required")
	}

	// Get base URL, defaulting to the official Anthropic API endpoint
	baseURL := os.Getenv("ANTHROPIC_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.anthropic.com/v1"
	}

	parsed, err := parseURL(baseURL)
	if err != nil {
		return err
	}

	// Create Backend for Anthropic
	backend := Backend{
		Name:     "anthropic",
		Hostname: parsed.hostname,
		IP:       parsed.ip,
		Port:     parsed.port,
		NeedsTLS: parsed.needsTLS,
	}

	// Create AnthropicConfig referencing the backend
	anthropicConfig := &AnthropicConfig{
		BackendName: "anthropic",
		SchemaName:  "Anthropic",
		Version:     parsed.version,
	}

	// Add to ConfigData
	data.Backends = append(data.Backends, backend)
	data.Anthropic = anthropicConfig

	return nil
}
