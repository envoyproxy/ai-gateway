// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package testopenai provides Azure OpenAI support for VCR cassette recording and playback.
//
// Azure OpenAI cassettes follow a specific workflow to enable recording and playback
// while protecting sensitive information:
//
// RECORDING FLOW:
//  1. NewRequest builds Azure-format path from request body model:
//     Input: endpoint="/chat/completions", body={"model":"gpt-4"}
//     Output: path="/openai/deployments/gpt-4/chat/completions"
//     Note: No api-version in the path built by NewRequest.
//
// 2. Server forwards to upstream Azure API:
//   - Reads AZURE_OPENAI_ENDPOINT (e.g., https://your-resource.eastus2.cognitiveservices.azure.com)
//   - Reads AZURE_OPENAI_DEPLOYMENT (deployment name configured in Azure portal)
//   - Reads OPENAI_API_VERSION (e.g., 2024-12-01-preview)
//   - Builds URL: {endpoint}/openai/deployments/{deployment}/{endpoint}?api-version={version}
//   - Example: https://your-resource.eastus2.cognitiveservices.azure.com/openai/deployments/prod-gpt4/chat/completions?api-version=2024-12-01-preview
//
// 3. VCR afterCaptureHook scrubs sensitive data from recorded cassette:
//   - Replaces actual hostname with "resource-name.cognitiveservices.azure.com"
//   - Replaces deployment name with model name from request body
//   - Strips api-version query parameter (not needed for playback matching)
//   - Result: https://resource-name.cognitiveservices.azure.com/openai/deployments/gpt-4/chat/completions
//
// PLAYBACK FLOW:
// 1. NewRequest builds same Azure-format path (no api-version)
// 2. Server normalizes cassette URL by replacing hostname only
// 3. Request path matches cassette path exactly
//
// ENVIRONMENT VARIABLES:
// Recording Azure cassettes requires:
// - AZURE_OPENAI_API_KEY: Authentication for Azure OpenAI
// - AZURE_OPENAI_ENDPOINT: Base URL (hostname is scrubbed in cassette)
// - AZURE_OPENAI_DEPLOYMENT: Deployment name (replaced with model in cassette)
// - OPENAI_API_VERSION: API version (stripped from cassette, only used upstream)
//
// Recording OpenAI cassettes requires:
// - OPENAI_API_KEY: Authentication for OpenAI
package testopenai

import (
	"net/url"
	"regexp"
)

const azureHostnameSuffix = ".cognitiveservices.azure.com"

var azureDeploymentPattern = regexp.MustCompile(`(/openai/deployments/)([^/]+)(/)`)

// isAzureURL checks if a URL matches the Azure OpenAI deployment URL pattern.
func isAzureURL(urlStr string) bool {
	return azureDeploymentPattern.MatchString(urlStr)
}

// buildAzurePath builds an Azure OpenAI request path from a standard endpoint and model.
// Input: endpoint="/chat/completions", model="gpt-4"
// Output: "/openai/deployments/gpt-4/chat/completions"
func buildAzurePath(endpoint, model string) string {
	return "/openai/deployments/" + model + endpoint
}

// scrubAzureURL removes sensitive information from an Azure cassette URL for recording.
// - Replaces hostname with "resource-name.cognitiveservices.azure.com"
// - Replaces deployment name with model
// - Strips api-version query parameter
func scrubAzureURL(cassetteURL, model string) string {
	u, _ := url.Parse(cassetteURL)
	u.Host = "resource-name" + azureHostnameSuffix
	u.Path = azureDeploymentPattern.ReplaceAllString(u.Path, "${1}"+model+"${3}")
	u.RawQuery = ""
	return u.String()
}
