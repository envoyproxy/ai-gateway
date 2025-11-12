// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import "github.com/envoyproxy/ai-gateway/internal/apischema/openai"

// ImageCassettes returns a slice of all cassettes for image generation.
func ImageCassettes() []Cassette {
	return cassettes(imageRequests)
}

// imageGenerationRequest is a minimal request body for OpenAI image generation.
// We avoid importing the OpenAI SDK in tests to keep dependencies light.
type imageGenerationRequest struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	// Optional fields like size/quality/response_format can be added later if needed.
	Size    string `json:"size,omitempty"`
	Quality string `json:"quality,omitempty"`
}

// imageRequests contains the actual request body for each image generation cassette.
var imageRequests = map[Cassette]*imageGenerationRequest{
	CassetteImageGenerationBasic: {
		Model:   openai.ModelGPTImage1Mini,
		Prompt:  "A simple black-and-white line drawing of a cat playing with yarn",
		Size:    "1024x1024",
		Quality: "low",
	},
}
