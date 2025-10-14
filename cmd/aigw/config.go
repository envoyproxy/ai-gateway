// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"errors"
	"fmt"
	"os"
	"reflect"

	"github.com/a8m/envsubst"

	"github.com/envoyproxy/ai-gateway/internal/autoconfig"
)

// readConfig returns the configuration as a string from the given path,
// substituting environment variables. If OPENAI_API_KEY or AZURE_OPENAI_API_KEY
// is set, it generates the config from environment variables. Otherwise, it returns an error.
func readConfig(path string, mcpServers *autoconfig.MCPServers, debug bool) (string, error) {
	// If a file path is provided, prefer it.
	if path != "" {
		configBytes, err := envsubst.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("error reading config: %w", err)
		}
		return string(configBytes), nil
	}

	var data autoconfig.ConfigData
	data.Debug = debug
	data.EnvoyVersion = os.Getenv("ENVOY_VERSION")

	// Add MCP servers if provided
	if mcpServers != nil {
		if err := autoconfig.AddMCPServers(&data, mcpServers); err != nil {
			return "", fmt.Errorf("failed to add MCP servers config: %w", err)
		}
	}

	// Add OpenAI config from ENV if available
	if os.Getenv("OPENAI_API_KEY") != "" || os.Getenv("AZURE_OPENAI_API_KEY") != "" {
		if err := autoconfig.PopulateOpenAIEnvConfig(&data); err != nil {
			return "", err
		}
	}

	// If we've found no config data, return an error.
	if reflect.DeepEqual(data, autoconfig.ConfigData{Debug: debug}) {
		return "", errors.New("you must supply at least OPENAI_API_KEY or AZURE_OPENAI_API_KEY or a config file path")
	}

	// We have any auto-generated config: write it and apply envsubst
	config, err := autoconfig.WriteConfig(&data)
	if err != nil {
		return "", err
	}
	return envsubst.String(config)
}
