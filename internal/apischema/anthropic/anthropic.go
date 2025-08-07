// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package anthropic contains Anthropic API schema definitions using the official SDK types.
package anthropic

import (
	"github.com/anthropics/anthropic-sdk-go"
)

// MessagesRequest represents a request to the Anthropic Messages API.
// This extends the SDK's MessageNewParams with additional fields commonly used.
type MessagesRequest struct {
	// Model is the AI model to use for the conversation.
	Model string `json:"model"`

	// Messages are the conversation messages.
	Messages []anthropic.MessageParam `json:"messages"`

	// MaxTokens is the maximum number of tokens to generate.
	MaxTokens int `json:"max_tokens"`

	// Stream indicates whether to stream the response.
	Stream bool `json:"stream,omitempty"`

	// Temperature controls randomness in the response.
	Temperature *float64 `json:"temperature,omitempty"`

	// TopP controls nucleus sampling.
	TopP *float64 `json:"top_p,omitempty"`

	// TopK controls top-k sampling.
	TopK *int `json:"top_k,omitempty"`

	// StopSequences are sequences that will stop generation.
	StopSequences []string `json:"stop_sequences,omitempty"`

	// SystemPrompt is the system message.
	System interface{} `json:"system,omitempty"`

	// Tools available for the model to use.
	Tools []anthropic.ToolParam `json:"tools,omitempty"`

	// ToolChoice controls how the model uses tools.
	ToolChoice interface{} `json:"tool_choice,omitempty"`

	// Metadata for the request.
	Metadata interface{} `json:"metadata,omitempty"`
}

// MessagesResponse represents a response from the Anthropic Messages API.
type MessagesResponse struct {
	// ID is the unique identifier for this message.
	ID string `json:"id"`

	// Type is always "message".
	Type string `json:"type"`

	// Role is always "assistant".
	Role string `json:"role"`

	// Content contains the response content.
	Content []anthropic.ContentBlockUnion `json:"content"`

	// Model is the model that generated this response.
	Model string `json:"model"`

	// StopReason indicates why the model stopped generating.
	StopReason *string `json:"stop_reason"`

	// StopSequence is the stop sequence that triggered the stop (if any).
	StopSequence *string `json:"stop_sequence"`

	// Usage contains token usage information.
	Usage *anthropic.MessageDeltaUsage `json:"usage"`
}
