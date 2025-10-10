// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"

	"github.com/anthropics/anthropic-sdk-go"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

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
	apiVersion        string
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
}

// RequestBody implements [AnthropicMessagesTranslator.RequestBody] for Anthropic to GCP Anthropic translation.
// This handles the transformation from native Anthropic format to GCP Anthropic format.
func (a *anthropicToGCPAnthropicTranslator) RequestBody(_ []byte, body *anthropicschema.MessagesRequest, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	// Extract model name for GCP endpoint from the parsed request.
	modelName := body.GetModel()

	// Work directly with the map since MessagesRequest is already map[string]interface{}.
	anthropicReq := make(map[string]any)
	maps.Copy(anthropicReq, *body)

	// Apply model name override if configured.
	a.requestModel = modelName
	if a.modelNameOverride != "" {
		a.requestModel = a.modelNameOverride
	}

	// Remove the model field since GCP doesn't want it in the body.
	delete(anthropicReq, "model")

	// Add GCP-specific anthropic_version field (required by GCP Vertex AI).
	// Uses backend config version (e.g., "vertex-2023-10-16" for GCP Vertex AI).
	if a.apiVersion == "" {
		return nil, nil, fmt.Errorf("anthropic_version is required for GCP Vertex AI but not provided in backend configuration")
	}
	anthropicReq[anthropicVersionKey] = a.apiVersion

	// Marshal the modified request.
	mutatedBody, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal modified request: %w", err)
	}

	// Determine the GCP path based on whether streaming is requested.
	specifier := "rawPredict"
	if stream, ok := anthropicReq["stream"].(bool); ok && stream {
		specifier = "streamRawPredict"
	}

	pathSuffix := buildGCPModelPathSuffix(gcpModelPublisherAnthropic, a.requestModel, specifier)

	headerMutation, bodyMutation = buildRequestMutations(pathSuffix, mutatedBody)
	return
}

// ResponseHeaders implements [AnthropicMessagesTranslator.ResponseHeaders] for Anthropic to GCP Anthropic.
func (a *anthropicToGCPAnthropicTranslator) ResponseHeaders(_ map[string]string) (
	headerMutation *extprocv3.HeaderMutation, err error,
) {
	// For Anthropic to GCP Anthropic, no header transformation is needed.
	return nil, nil
}

// ResponseBody implements [AnthropicMessagesTranslator.ResponseBody] for Anthropic to GCP Anthropic.
// This is essentially a passthrough since both use the same Anthropic response format.
func (a *anthropicToGCPAnthropicTranslator) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, responseModel string, err error,
) {
	// Read the response body for both streaming and non-streaming.
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, LLMTokenUsage{}, "", fmt.Errorf("failed to read response body: %w", err)
	}

	// For streaming chunks, parse SSE format to extract token usage.
	if !endOfStream {
		// Parse SSE format - split by lines and look for data: lines.
		for line := range bytes.Lines(bodyBytes) {
			line = bytes.TrimSpace(line)
			dataPrefix := []byte("data: ")
			if bytes.HasPrefix(line, dataPrefix) {
				jsonData := bytes.TrimPrefix(line, dataPrefix)

				var eventData map[string]any
				if unmarshalErr := json.Unmarshal(jsonData, &eventData); unmarshalErr != nil {
					// Skip lines with invalid JSON (like ping events or malformed data).
					continue
				}
				if eventType, ok := eventData["type"].(string); ok {
					switch eventType {
					case "message_start":
						// Extract input tokens from message.usage.
						if messageData, ok := eventData["message"].(map[string]any); ok {
							if usageData, ok := messageData["usage"].(map[string]any); ok {
								if inputTokens, ok := usageData["input_tokens"].(float64); ok {
									tokenUsage.InputTokens = uint32(inputTokens) //nolint:gosec
								}
								// Some message_start events may include initial output tokens.
								if outputTokens, ok := usageData["output_tokens"].(float64); ok && outputTokens > 0 {
									tokenUsage.OutputTokens = uint32(outputTokens) //nolint:gosec
								}
								tokenUsage.TotalTokens = tokenUsage.InputTokens + tokenUsage.OutputTokens
							}
						}

					case "message_delta":
						if usageData, ok := eventData["usage"].(map[string]any); ok {
							if outputTokens, ok := usageData["output_tokens"].(float64); ok {
								// Add to existing output tokens (in case message_start had some initial ones).
								tokenUsage.OutputTokens += uint32(outputTokens) //nolint:gosec
								tokenUsage.TotalTokens = tokenUsage.InputTokens + tokenUsage.OutputTokens
							}
						}
					}
				}
			}
		}

		return nil, &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: bodyBytes},
		}, tokenUsage, a.requestModel, nil
	}

	// Parse the Anthropic response to extract token usage.
	var anthropicResp anthropic.Message
	if err = json.Unmarshal(bodyBytes, &anthropicResp); err != nil {
		// If we can't parse as Anthropic format, pass through as-is.
		return nil, &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: bodyBytes},
		}, LLMTokenUsage{}, a.requestModel, nil
	}

	// Extract token usage from the response.
	tokenUsage = LLMTokenUsage{
		InputTokens:  uint32(anthropicResp.Usage.InputTokens),                                    //nolint:gosec
		OutputTokens: uint32(anthropicResp.Usage.OutputTokens),                                   //nolint:gosec
		TotalTokens:  uint32(anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens), //nolint:gosec
		CachedTokens: uint32(anthropicResp.Usage.CacheReadInputTokens),                           //nolint:gosec
	}

	// Pass through the response body unchanged since both input and output are Anthropic format.
	headerMutation = &extprocv3.HeaderMutation{}
	setContentLength(headerMutation, bodyBytes)
	bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{Body: bodyBytes},
	}

	return headerMutation, bodyMutation, tokenUsage, a.requestModel, nil
}
