// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"cmp"
	"fmt"
	"log/slog"
	"strconv"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// NewAnthropicToGCPAnthropicTranslator creates a translator for Anthropic to GCP Anthropic format.
// This is essentially a passthrough translator with GCP-specific modifications.
func NewAnthropicToGCPAnthropicTranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride, unsupportedFields ...string) AnthropicMessagesTranslator {
	return &anthropicToGCPAnthropicTranslator{
		apiVersion:        apiVersion,
		modelNameOverride: modelNameOverride,
		unsupportedFields: unsupportedFields,
	}
}

type anthropicToGCPAnthropicTranslator struct {
	anthropicToAnthropicTranslator
	apiVersion        string
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
	unsupportedFields []string
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

	// Strip Vertex-unsupported Anthropic-only fields the operator has declared via the
	// backend's unsupportedFields config (e.g. "context_management" — Claude Code sends it
	// on every request, but Vertex rejects it with "Extra inputs are not permitted").
	for _, field := range a.unsupportedFields {
		if gjson.GetBytes(newBody, field).Exists() {
			newBody, _ = sjson.DeleteBytesOptions(newBody, field, sjsonOptionsInPlace)
			if a.debugLogEnabled && a.logger != nil {
				a.logger.Debug("stripped unsupported Anthropic field", slog.String("field", field))
			}
		}
	}

	// Determine the GCP path based on whether streaming is requested.
	specifier := "rawPredict"
	if req.Stream {
		specifier = "streamRawPredict"
	}

	path := buildGCPModelPathSuffix(gcpModelPublisherAnthropic, a.requestModel, specifier)
	newHeaders = []internalapi.Header{{pathHeaderName, path}, {contentLengthHeaderName, strconv.Itoa(len(newBody))}}
	return
}
