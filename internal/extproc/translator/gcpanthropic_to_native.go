// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicVertex "github.com/anthropics/anthropic-sdk-go/vertex"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// NewGCPAnthropicToNativeAnthropicTranslator creates a translator for Anthropic to GCP Anthropic format.
// This is essentially a passthrough translator with GCP-specific modifications.
func NewGCPAnthropicToNativeAnthropicTranslator(modelNameOverride string) OpenAIChatCompletionTranslator {
	return &gcpAnthropicToNativeTranslator{
		modelNameOverride: modelNameOverride,
	}
}

type gcpAnthropicToNativeTranslator struct {
	modelNameOverride string
}

// RequestBody implements [OpenAIChatCompletionTranslator.RequestBody] for Anthropic to GCP Anthropic translation.
// This handles the transformation from native Anthropic format to GCP Anthropic format.
func (a *gcpAnthropicToNativeTranslator) RequestBody(raw []byte, _ *openai.ChatCompletionRequest, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	fmt.Printf("DEBUG [GCPAnthropicToNative]: RequestBody method called - this should NOT happen for this translator!\n")
	// Parse the incoming Anthropic request.
	var anthropicReq map[string]interface{}
	if err = json.Unmarshal(raw, &anthropicReq); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal Anthropic request: %w", err)
	}

	// Apply model name override if configured.
	if a.modelNameOverride != "" {
		anthropicReq["model"] = a.modelNameOverride
	}

	modelName := anthropicReq["model"].(string)

	// Remove model field from request body since it is specified in the URL path.
	delete(anthropicReq, "model")
	fmt.Printf("DEBUG [GCPAnthropicToNative]: Removed model field from request body\n")

	if _, exists := anthropicReq[anthropicVersionKey]; !exists {
		anthropicReq[anthropicVersionKey] = anthropicVertex.DefaultVersion
	}

	// Marshal the modified request.
	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal modified request: %w", err)
	}

	// Determine the GCP path based on whether streaming is requested.
	specifier := "rawPredict"
	if stream, ok := anthropicReq["stream"].(bool); ok && stream {
		specifier = "streamRawPredict"
	}

	pathSuffix := buildGCPModelPathSuffix(gcpModelPublisherAnthropic, modelName, specifier)
	headerMutation, bodyMutation = buildRequestMutations(pathSuffix, body)
	return
}

// ResponseHeaders implements [OpenAIChatCompletionTranslator.ResponseHeaders] for Anthropic to GCP Anthropic.
func (a *gcpAnthropicToNativeTranslator) ResponseHeaders(_ map[string]string) (
	headerMutation *extprocv3.HeaderMutation, err error,
) {
	// For Anthropic to GCP Anthropic, no header transformation is needed.
	return nil, nil
}

// ResponseBody implements [OpenAIChatCompletionTranslator.ResponseBody] for Anthropic to GCP Anthropic.
// This is essentially a passthrough since both use the same Anthropic response format.
func (a *gcpAnthropicToNativeTranslator) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, err error,
) {
	// Read the response body for both streaming and non-streaming.
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, LLMTokenUsage{}, fmt.Errorf("failed to read response body: %w", err)
	}

	// For streaming chunks, pass through unchanged.
	if !endOfStream {
		return nil, &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: bodyBytes},
		}, LLMTokenUsage{}, nil
	}

	// Parse the Anthropic response to extract token usage.
	var anthropicResp anthropic.Message
	if err = json.Unmarshal(bodyBytes, &anthropicResp); err != nil {
		// If we can't parse as Anthropic format, pass through as-is.
		return nil, &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: bodyBytes},
		}, LLMTokenUsage{}, nil
	}

	// Extract token usage from the response.
	tokenUsage = LLMTokenUsage{
		InputTokens:  uint32(anthropicResp.Usage.InputTokens),                                    //nolint:gosec
		OutputTokens: uint32(anthropicResp.Usage.OutputTokens),                                   //nolint:gosec
		TotalTokens:  uint32(anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens), //nolint:gosec
	}

	// Pass through the response body unchanged since both input and output are Anthropic format.
	headerMutation = &extprocv3.HeaderMutation{}
	setContentLength(headerMutation, bodyBytes)
	bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{Body: bodyBytes},
	}

	return headerMutation, bodyMutation, tokenUsage, nil
}
