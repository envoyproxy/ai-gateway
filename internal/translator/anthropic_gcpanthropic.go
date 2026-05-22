// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"cmp"
	"fmt"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const (
	anthropicBetaHeaderName    = "anthropic-beta"
	structuredOutputsBetaValue = "structured-outputs-2025-12-15"
)

// NewAnthropicToGCPAnthropicTranslator creates a translator for Anthropic to GCP Anthropic format.
// This is essentially a passthrough translator with GCP-specific modifications.
func NewAnthropicToGCPAnthropicTranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) AnthropicMessagesTranslator {
	return &anthropicToGCPAnthropicTranslator{
		apiVersion:        apiVersion,
		modelNameOverride: modelNameOverride,
	}
}

type anthropicToGCPAnthropicTranslator struct {
	anthropicToAnthropicTranslator
	apiVersion        string
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
	requestHeaders    map[string]string
}

// SetRequestHeaders stores the request headers for use during translation.
// This allows the translator to modify headers like anthropic-beta that are
// not part of the request body.
func (a *anthropicToGCPAnthropicTranslator) SetRequestHeaders(headers map[string]string) {
	a.requestHeaders = headers
}

// RequestBody implements [AnthropicMessagesTranslator.RequestBody] for Anthropic to GCP Anthropic translation.
// This handles the transformation from native Anthropic format to GCP Anthropic format.
func (a *anthropicToGCPAnthropicTranslator) RequestBody(raw []byte, req *anthropicschema.MessagesRequest, _ bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	a.stream = req.Stream

	// Apply model name override if configured.
	a.requestModel = cmp.Or(a.modelNameOverride, req.Model)

	// Add GCP-specific anthropic_version field (required by GCP Vertex AI).
	// Uses backend config version (e.g., "vertex-2023-10-16" for GCP Vertex AI).
	if a.apiVersion == "" {
		return nil, nil, fmt.Errorf("anthropic_version is required for GCP Vertex AI but not provided in backend configuration")
	}

	mutatedBody, _ := sjson.SetBytesOptions(raw, anthropicVersionKey, a.apiVersion, sjsonOptions)

	// Remove the model field since GCP doesn't want it in the body.
	newBody, _ = sjson.DeleteBytesOptions(mutatedBody, "model",
		// It is safe to use sjsonOptionsInPlace here since we have already created a new mutatedBody above.
		sjsonOptionsInPlace)

	// Strip output_config.format from the body since GCP Vertex AI does not support structured outputs.
	// Requests with output_config.format will be rejected by Vertex with "Extra inputs are not permitted".
	// We only strip the format field, preserving other output_config fields like effort.
	if gjson.GetBytes(newBody, "output_config.format").Exists() {
		newBody, _ = sjson.DeleteBytesOptions(newBody, "output_config.format", sjsonOptionsInPlace)
		// If output_config is now empty (format was the only field), remove the entire key
		// to avoid leaving an empty object that GCP may also reject.
		if oc := gjson.GetBytes(newBody, "output_config"); oc.Exists() && len(oc.Map()) == 0 {
			newBody, _ = sjson.DeleteBytesOptions(newBody, "output_config", sjsonOptionsInPlace)
		}
	}

	// Determine the GCP path based on whether streaming is requested.
	specifier := "rawPredict"
	if req.Stream {
		specifier = "streamRawPredict"
	}

	path := buildGCPModelPathSuffix(gcpModelPublisherAnthropic, a.requestModel, specifier)
	newHeaders = []internalapi.Header{{pathHeaderName, path}, {contentLengthHeaderName, strconv.Itoa(len(newBody))}}

	// Strip the structured-outputs beta value from the anthropic-beta header since
	// GCP Vertex AI does not support it and rejects requests containing this beta.
	if betaHeader := a.filteredAnthropicBetaHeader(); betaHeader != nil {
		newHeaders = append(newHeaders, *betaHeader)
	}

	return
}

// filteredAnthropicBetaHeader returns the anthropic-beta header with the
// "structured-outputs-2025-12-15" value removed. If no anthropic-beta header
// exists in the request, returns nil.
// The anthropic-beta header is a comma-separated list of beta feature names.
func (a *anthropicToGCPAnthropicTranslator) filteredAnthropicBetaHeader() *internalapi.Header {
	betaValue, ok := a.requestHeaders[anthropicBetaHeaderName]
	if !ok {
		return nil
	}
	betas := strings.Split(betaValue, ",")
	filtered := make([]string, 0, len(betas))
	for _, b := range betas {
		trimmed := strings.TrimSpace(b)
		if trimmed == "" || trimmed == structuredOutputsBetaValue {
			continue
		}
		filtered = append(filtered, trimmed)
	}
	h := internalapi.Header{anthropicBetaHeaderName, strings.Join(filtered, ",")}
	return &h
}
