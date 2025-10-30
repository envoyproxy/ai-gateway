// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"cmp"
	"fmt"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
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
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	a.stream = req.Stream

	// Apply model name override if configured.
	a.requestModel = cmp.Or(a.modelNameOverride, req.Model)

	mutatedBody, _ := sjson.SetBytesOptions(raw, anthropicVersionKey, a.apiVersion, sjsonOptions)

	// Remove the model field since GCP doesn't want it in the body.
	//
	// TODO: no idea if this comment "GCP doesn't want it in the body" is accurate.
	// 	at least it's not documented in https://docs.claude.com/en/api/claude-on-vertex-ai.
	// 	Either delete this line or confirm the behavior in the documentation.
	mutatedBody, _ = sjson.DeleteBytes(mutatedBody, "model")

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

	pathSuffix := buildGCPModelPathSuffix(gcpModelPublisherAnthropic, a.requestModel, specifier)

	headerMutation, bodyMutation = buildRequestMutations(pathSuffix, mutatedBody)
	return
}
