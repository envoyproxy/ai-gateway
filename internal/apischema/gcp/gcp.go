// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package gcp

import "google.golang.org/genai"

type GenerateContentRequest struct {
	Contents          []genai.Content         `json:"contents"`
	Tools             []genai.Tool            `json:"tools"`
	ToolConfig        *genai.ToolConfig       `json:"tool_config,omitempty"`
	GenerationConfig  *genai.GenerationConfig `json:"generation_config,omitempty"`
	SystemInstruction *genai.Content          `json:"system_instruction,omitempty"`
}
