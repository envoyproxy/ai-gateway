// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package autoconfig

import (
	"fmt"
	"os"
)

// PopulateOpenAIEnvConfig populates ConfigData with OpenAI backend configuration
// from standard OpenAI SDK environment variables.
//
// This errs if neither OPENAI_API_KEY nor AZURE_OPENAI_API_KEY is set.
// Prioritizes AZURE_OPENAI_API_KEY over OPENAI_API_KEY when both are set.
//
// For Azure OpenAI, requires AZURE_OPENAI_ENDPOINT and OPENAI_API_VERSION.
//
// See https://github.com/openai/openai-python/blob/main/src/openai/_client.py
func PopulateOpenAIEnvConfig(data *ConfigData) error {
	if data == nil {
		return fmt.Errorf("ConfigData cannot be nil")
	}

	// Prioritize Azure OpenAI over standard OpenAI
	azureAPIKey := os.Getenv("AZURE_OPENAI_API_KEY")
	openaiAPIKey := os.Getenv("OPENAI_API_KEY")

	var err error
	var parsed *parsedURL
	var schemaName string
	var version string

	switch {
	case azureAPIKey != "":
		// Azure OpenAI mode
		azureEndpoint := os.Getenv("AZURE_OPENAI_ENDPOINT")
		if azureEndpoint == "" {
			return fmt.Errorf("AZURE_OPENAI_ENDPOINT environment variable is required when AZURE_OPENAI_API_KEY is set")
		}
		apiVersion := os.Getenv("OPENAI_API_VERSION")
		if apiVersion == "" {
			return fmt.Errorf("OPENAI_API_VERSION environment variable is required when AZURE_OPENAI_API_KEY is set")
		}

		parsed, err = parseURL(azureEndpoint)
		if err != nil {
			return err
		}
		schemaName = "AzureOpenAI"
		version = apiVersion
	case openaiAPIKey != "":
		// Standard OpenAI mode
		baseURL := os.Getenv("OPENAI_BASE_URL")
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}

		parsed, err = parseURL(baseURL)
		if err != nil {
			return err
		}
		schemaName = "OpenAI"
		version = parsed.version
	default:
		return fmt.Errorf("either OPENAI_API_KEY or AZURE_OPENAI_API_KEY environment variable is required")
	}

	// Create Backend for OpenAI
	backend := Backend{
		Name:             "openai",
		Hostname:         parsed.hostname,
		IPAddress:        parsed.ipAddress,
		OriginalHostname: parsed.originalHostname,
		Port:             parsed.port,
		NeedsTLS:         parsed.needsTLS,
	}

	// Create OpenAIConfig referencing the backend
	openaiConfig := &OpenAIConfig{
		BackendName:    "openai",
		SchemaName:     schemaName,
		Version:        version,
		OrganizationID: os.Getenv("OPENAI_ORG_ID"),
		ProjectID:      os.Getenv("OPENAI_PROJECT_ID"),
	}

	// Add to ConfigData
	data.Backends = append(data.Backends, backend)
	data.OpenAI = openaiConfig

	return nil
}
