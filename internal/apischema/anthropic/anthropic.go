// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package anthropic contains Anthropic API schema definitions using the official SDK types.
package anthropic

import "github.com/anthropics/anthropic-sdk-go"

// MessagesRequest represents a request to the Anthropic Messages API.
// Uses a dictionary approach to handle any JSON structure flexibly.
type MessagesRequest map[string]any

// Helper methods to extract common fields from the dictionary

func (m MessagesRequest) GetModel() string {
	if model, ok := m["model"].(string); ok {
		return model
	}
	return ""
}

func (m MessagesRequest) GetMaxTokens() int {
	if maxTokens, ok := m["max_tokens"].(float64); ok {
		return int(maxTokens)
	}
	return 0
}

func (m MessagesRequest) GetStream() bool {
	if stream, ok := m["stream"].(bool); ok {
		return stream
	}
	return false
}

// MessagesResponse represents a response from the Anthropic Messages API.
//
// Temporarily alias to the official SDK type to be consistent with other endpoints, not actually used in the main code.
// See https://github.com/envoyproxy/ai-gateway/issues/1389
type MessagesResponse = anthropic.Message

// MessagesStreamChunk represents a streaming chunk from the Anthropic Messages API.
//
// Temporarily alias to the official SDK type to be consistent with other endpoints, not actually used in the main code.
// See https://github.com/envoyproxy/ai-gateway/issues/1389
type MessagesStreamChunk = anthropic.MessageStreamEventUnion
