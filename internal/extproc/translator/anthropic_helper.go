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

const (
	anthropicVersionKey = "anthropic_version"
)

// anthropicResponseHandler provides shared response handling logic for Anthropic-compatible APIs.
// This handler is stateless and used by AWS Bedrock and GCP Vertex AI translators to avoid code duplication.
type anthropicResponseHandler struct{}

// newAnthropicResponseHandler creates a new stateless response handler.
func newAnthropicResponseHandler() *anthropicResponseHandler {
	return &anthropicResponseHandler{}
}

// ResponseBody handles both streaming and non-streaming Anthropic API responses.
// It extracts token usage information and returns the response unchanged (passthrough).
// The requestModel parameter is used to populate the responseModel return value.
func (h *anthropicResponseHandler) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool, requestModel internalapi.RequestModel) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, responseModel string, err error,
) {
	// Read the response body for both streaming and non-streaming.
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, LLMTokenUsage{}, "", fmt.Errorf("failed to read response body: %w", err)
	}

	// For streaming chunks, parse SSE format to extract token usage.
	if !endOfStream {
		tokenUsage = h.extractTokenUsageFromSSE(bodyBytes)
		return nil, &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: bodyBytes},
		}, tokenUsage, requestModel, nil
	}

	// For non-streaming responses, parse the complete Anthropic response.
	tokenUsage, err = h.extractTokenUsageFromResponse(bodyBytes)
	if err != nil {
		// If we can't parse as Anthropic format, pass through as-is.
		return nil, &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: bodyBytes},
		}, LLMTokenUsage{}, requestModel, nil
	}

	// Pass through the response body unchanged since both input and output are Anthropic format.
	headerMutation = &extprocv3.HeaderMutation{}
	setContentLength(headerMutation, bodyBytes)
	bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{Body: bodyBytes},
	}

	return headerMutation, bodyMutation, tokenUsage, requestModel, nil
}

// extractTokenUsageFromSSE parses SSE (Server-Sent Events) format streaming responses
// to extract token usage information from message_start and message_delta events.
func (h *anthropicResponseHandler) extractTokenUsageFromSSE(bodyBytes []byte) LLMTokenUsage {
	var tokenUsage LLMTokenUsage

	// Parse SSE format - split by lines and look for data: lines.
	for line := range bytes.Lines(bodyBytes) {
		line = bytes.TrimSpace(line)
		if !bytes.HasPrefix(line, dataPrefix) {
			continue
		}
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

	return tokenUsage
}

// extractTokenUsageFromResponse parses a complete (non-streaming) Anthropic response
// to extract token usage information.
func (h *anthropicResponseHandler) extractTokenUsageFromResponse(bodyBytes []byte) (LLMTokenUsage, error) {
	var anthropicResp anthropic.Message
	if err := json.Unmarshal(bodyBytes, &anthropicResp); err != nil {
		return LLMTokenUsage{}, err
	}

	tokenUsage := LLMTokenUsage{
		InputTokens:       uint32(anthropicResp.Usage.InputTokens),                                    //nolint:gosec
		OutputTokens:      uint32(anthropicResp.Usage.OutputTokens),                                   //nolint:gosec
		TotalTokens:       uint32(anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens), //nolint:gosec
		CachedInputTokens: uint32(anthropicResp.Usage.CacheReadInputTokens),                           //nolint:gosec
	}

	return tokenUsage, nil
}

// applyModelNameOverride applies model name override logic used by AWS and GCP translators.
func applyModelNameOverride(originalModel internalapi.RequestModel, override internalapi.ModelNameOverride) internalapi.RequestModel {
	if override != "" {
		return override
	}
	return originalModel
}

// prepareAnthropicRequest prepares the request body for cloud providers (AWS/GCP)
// The anthropic_version field is required by cloud provider implementations.
func prepareAnthropicRequest(body *anthropicschema.MessagesRequest, apiVersion string) (map[string]any, error) {
	anthropicReq := make(map[string]any)
	maps.Copy(anthropicReq, *body)

	// Remove model field - cloud providers use it in the URL path instead
	delete(anthropicReq, "model")

	// Add required anthropic_version field
	if apiVersion == "" {
		return nil, fmt.Errorf("anthropic_version is required but not provided in backend configuration")
	}
	anthropicReq[anthropicVersionKey] = apiVersion

	return anthropicReq, nil
}
