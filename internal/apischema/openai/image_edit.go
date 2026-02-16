// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

// ImageEditRequest represents the request body for /v1/images/edits.
// https://platform.openai.com/docs/api-reference/images/createEdit
type ImageEditRequest struct {
	// Required: The image to edit. Must be a valid PNG file, less than 4MB, and square.
	// If mask is not provided, image must have transparency, which will be used as the mask.
	Image string `json:"image"`

	// Required: A text description of the desired image(s). The maximum length is 1000 characters.
	Prompt string `json:"prompt"`

	// The model to use for image generation. Only dall-e-2 is supported at this time.
	// Defaults to dall-e-2.
	Model string `json:"model,omitempty"`

	// An additional image whose fully transparent areas (e.g. where alpha is zero)
	// indicate where image should be edited. Must be a valid PNG file, less than 4MB,
	// and have the same dimensions as image.
	Mask string `json:"mask,omitempty"`

	// The number of images to generate. Must be between 1 and 10.
	// Defaults to 1.
	N int `json:"n,omitempty"`

	// The size of the generated images. Must be one of 256x256, 512x512, or 1024x1024.
	// Defaults to 1024x1024.
	Size string `json:"size,omitempty"`

	// The format in which the generated images are returned. Must be one of url or b64_json.
	// URLs are only valid for 60 minutes after the image has been generated.
	// Defaults to url.
	ResponseFormat string `json:"response_format,omitempty"`

	// A unique identifier representing your end-user, which can help OpenAI to monitor and detect abuse.
	User string `json:"user,omitempty"`
}

// ImageEditResponse represents the response body for /v1/images/edits.
// This is the same structure as ImageGenerationResponse.
// https://platform.openai.com/docs/api-reference/images/object
type ImageEditResponse struct {
	// The Unix timestamp (in seconds) of when the image was created.
	Created int64 `json:"created"`
	// The list of generated/edited images.
	Data []ImageEditResponseData `json:"data"`
}

// ImageEditResponseData represents a single edited image in the response.
type ImageEditResponseData struct {
	B64JSON       string `json:"b64_json,omitempty"`
	URL           string `json:"url,omitempty"`
	RevisedPrompt string `json:"revised_prompt,omitempty"`
}
