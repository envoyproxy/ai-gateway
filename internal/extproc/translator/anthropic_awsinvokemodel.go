// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

const anthropicVersionForBedrock = "bedrock-2023-05-31"

// NewAnthropicToAWSInvokeModelTranslator creates a translator for Anthropic to AWS Bedrock InvokeModel format.
// This is a lightweight translator that adds minimal AWS-specific fields while preserving the native Anthropic format.
func NewAnthropicToAWSInvokeModelTranslator(modelNameOverride string) OpenAIChatCompletionTranslator {
	return &anthropicToAWSInvokeModelTranslator{
		modelNameOverride: modelNameOverride,
	}
}

type anthropicToAWSInvokeModelTranslator struct {
	modelNameOverride string
	stream            bool
}

// RequestBody implements [OpenAIChatCompletionTranslator.RequestBody] for Anthropic to AWS InvokeModel.
// Core transformation: Add anthropic_version field and change the API endpoint path.
func (a *anthropicToAWSInvokeModelTranslator) RequestBody(raw []byte, _ *openai.ChatCompletionRequest, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	// Parse the incoming Anthropic request (already in correct format)
	var anthropicReq map[string]interface{}
	if err = json.Unmarshal(raw, &anthropicReq); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal Anthropic request: %w", err)
	}

	// Add required AWS Bedrock field
	anthropicReq["anthropic_version"] = anthropicVersionForBedrock

	// Debug log to verify this translator is being used
	slog.Info("AnthropicToAWSInvokeModelTranslator processing request", "model", a.modelNameOverride)

	// Determine if this is a streaming request
	if stream, ok := anthropicReq["stream"].(bool); ok && stream {
		a.stream = true
	}

	// Get model name for path construction
	var modelName string
	if a.modelNameOverride != "" {
		modelName = a.modelNameOverride
	} else if model, ok := anthropicReq["model"].(string); ok && model != "" {
		modelName = model
	} else {
		return nil, nil, fmt.Errorf("model name is required for AWS Bedrock InvokeModel")
	}

	// Construct the AWS Bedrock InvokeModel path
	var pathTemplate string
	if a.stream {
		pathTemplate = "/model/%s/invoke-with-response-stream"
	} else {
		pathTemplate = "/model/%s/invoke"
	}

	// Create header mutation with new path
	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{
				Key:      ":path",
				RawValue: []byte(fmt.Sprintf(pathTemplate, modelName)),
			}},
		},
	}

	// Marshal the modified request body
	body, err := json.Marshal(anthropicReq)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal modified request: %w", err)
	}

	// Create body mutation
	setContentLength(headerMutation, body)
	bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{Body: body},
	}

	return headerMutation, bodyMutation, nil
}

// ResponseHeaders implements [OpenAIChatCompletionTranslator.ResponseHeaders] for Anthropic to AWS InvokeModel.
// Minimal processing - mostly passthrough.
func (a *anthropicToAWSInvokeModelTranslator) ResponseHeaders(headers map[string]string) (
	headerMutation *extprocv3.HeaderMutation, err error,
) {
	// For streaming responses, AWS might return different content-type
	if a.stream {
		contentType := headers["content-type"]
		if contentType == "application/vnd.amazon.eventstream" {
			// Change to expected streaming content type
			return &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{Header: &corev3.HeaderValue{Key: "content-type", Value: "text/event-stream"}},
				},
			}, nil
		}
	}
	// Most headers pass through unchanged
	return nil, nil
}

// ResponseBody implements [OpenAIChatCompletionTranslator.ResponseBody] for Anthropic to AWS InvokeModel.
// AWS InvokeModel returns native Anthropic format, so this is mostly passthrough with token usage extraction.
func (a *anthropicToAWSInvokeModelTranslator) ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, err error,
) {
	// Read the response body
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, LLMTokenUsage{}, fmt.Errorf("failed to read response body: %w", err)
	}

	if a.stream {
		// For streaming, AWS wraps Anthropic events in event-stream format
		// TODO: Handle streaming response unwrapping if needed
		// For now, pass through as-is since the format might already be compatible
		return nil, &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: bodyBytes},
		}, LLMTokenUsage{}, nil
	}

	// For non-streaming responses, extract token usage and pass through
	if endOfStream {
		// Parse response to extract token usage (native Anthropic format)
		var anthropicResp map[string]interface{}
		if err = json.Unmarshal(bodyBytes, &anthropicResp); err == nil {
			// Extract token usage if present
			if usage, ok := anthropicResp["usage"].(map[string]interface{}); ok {
				if inputTokens, ok := usage["input_tokens"].(float64); ok {
					tokenUsage.InputTokens = uint32(inputTokens) //nolint:gosec
				}
				if outputTokens, ok := usage["output_tokens"].(float64); ok {
					tokenUsage.OutputTokens = uint32(outputTokens) //nolint:gosec
				}
				tokenUsage.TotalTokens = tokenUsage.InputTokens + tokenUsage.OutputTokens
			}
		}
	}

	// Pass through the response body unchanged (already in native Anthropic format)
	headerMutation = &extprocv3.HeaderMutation{}
	setContentLength(headerMutation, bodyBytes)
	bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{Body: bodyBytes},
	}

	return headerMutation, bodyMutation, tokenUsage, nil
}
