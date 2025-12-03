// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"cmp"
	"fmt"
	"strconv"

	"github.com/tidwall/sjson"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
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
}

// RequestBody implements [AnthropicMessagesTranslator.RequestBody] for Anthropic to GCP Anthropic translation.
// This handles the transformation from native Anthropic format to GCP Anthropic format.
func (a *anthropicToGCPAnthropicTranslator) RequestBody(raw []byte, req *anthropicschema.MessagesRequest, _ bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	// Extract model name for GCP endpoint from the parsed request.
	a.stream = req.Stream

	// Apply model name override if configured.
	a.requestModel = cmp.Or(a.modelNameOverride, req.Model)

	mutatedBody, _ := sjson.SetBytesOptions(raw, anthropicVersionKey, a.apiVersion, sjsonOptions)

	// Remove the model field since GCP doesn't want it in the body.
	// Note: Do not operate on raw here, as that would mutate the original request body.
	// Hence, we do the SetBytesOptions above to create mutatedBody first.
	newBody, _ = sjson.DeleteBytes(mutatedBody, "model")

	// Add GCP-specific anthropic_version field (required by GCP Vertex AI).
	// Uses backend config version (e.g., "vertex-2023-10-16" for GCP Vertex AI).
	if a.apiVersion == "" {
		return nil, nil, fmt.Errorf("anthropic_version is required for GCP Vertex AI but not provided in backend configuration")
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
