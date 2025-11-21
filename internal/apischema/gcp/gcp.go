// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package gcp

import "google.golang.org/genai"

type GenerateContentRequest struct {
	// Contains the multipart content of a message.
	//
	// https://github.com/googleapis/go-genai/blob/6a8184fcaf8bf15f0c566616a7b356560309be9b/types.go#L858
	Contents []genai.Content `json:"contents"`
	// Tool details of a tool that the model may use to generate a response.
	//
	// https://github.com/googleapis/go-genai/blob/6a8184fcaf8bf15f0c566616a7b356560309be9b/types.go#L1406
	Tools []genai.Tool `json:"tools"`
	// Optional. Tool config.
	// This config is shared for all tools provided in the request.
	//
	// https://github.com/googleapis/go-genai/blob/6a8184fcaf8bf15f0c566616a7b356560309be9b/types.go#L1466
	ToolConfig *genai.ToolConfig `json:"tool_config,omitempty"`
	// Optional. Generation config.
	// You can find API default values and more details at https://cloud.google.com/vertex-ai/generative-ai/docs/model-reference/inference#generationconfig
	// and https://cloud.google.com/vertex-ai/generative-ai/docs/multimodal/content-generation-parameters.
	GenerationConfig *genai.GenerationConfig `json:"generation_config,omitempty"`
	// Optional. Instructions for the model to steer it toward better performance.
	// For example, "Answer as concisely as possible" or "Don't use technical
	// terms in your response".
	//
	// https://github.com/googleapis/go-genai/blob/6a8184fcaf8bf15f0c566616a7b356560309be9b/types.go#L858
	SystemInstruction *genai.Content `json:"system_instruction,omitempty"`
	// Optional: Safety settings in the request to block unsafe content in the response.
	//
	// https://github.com/googleapis/go-genai/blob/6a8184fcaf8bf15f0c566616a7b356560309be9b/types.go#L1057
	SafetySettings []*genai.SafetySetting `json:"safetySettings,omitempty"`
}

// CreateAuthTokenRequest represents a request to create an ephemeral auth token for Gemini Live API
// Reference: https://ai.google.dev/api/generate-content#auth-tokens
type CreateAuthTokenRequest struct {
	// Uses specifies how many times the token can be used (typically 1)
	Uses int `json:"uses"`
	// ExpireTime is the ISO8601 formatted expiration time
	ExpireTime string `json:"expireTime"`
}

// CreateAuthTokenResponse represents the response from creating an auth token
// Reference: https://ai.google.dev/api/generate-content#auth-tokens
type CreateAuthTokenResponse struct {
	// Name is the resource name of the auth token in format "auth_tokens/{token}"
	Name string `json:"name"`
	// ExpireTime is the ISO8601 formatted expiration time
	ExpireTime string `json:"expireTime,omitempty"`
}

