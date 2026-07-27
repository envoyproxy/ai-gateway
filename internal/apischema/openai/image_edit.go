// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

// ImageEditRequest represents the request body for /v1/images/edits.
// For gpt-image-1 models, the request is sent as JSON. For DALL-E 2, multipart/form-data
// is used, but the gateway only handles JSON bodies through the extproc pipeline.
// https://platform.openai.com/docs/api-reference/images/createEdit
type ImageEditRequest struct {
	// Required: A text description of the desired image(s). The maximum length is 1000 characters
	// for dall-e-2 and 32000 characters for gpt-image-1.
	Prompt string `json:"prompt"`

	// The model to use for image edit. Defaults to dall-e-2.
	Model string `json:"model,omitempty"`

	// The image(s) to edit. For gpt-image-1, this is an array of image objects
	// (with file_id or image_url). For dall-e-2, a single PNG file reference.
	Image any `json:"image,omitempty"`

	// An additional image whose fully transparent areas (e.g. where alpha is zero)
	// indicate where image should be edited. Must be a valid PNG file, less than 4MB,
	// and have the same dimensions as image.
	Mask string `json:"mask,omitempty"`

	// The number of images to generate. Must be between 1 and 10.
	// Defaults to 1.
	N int `json:"n,omitempty"`

	// The size of the generated images.
	// - dall-e-2: 256x256, 512x512, or 1024x1024.
	// - gpt-image-1: 1024x1024, 1536x1024, 1024x1536, or auto.
	// Defaults to 1024x1024 (dall-e-2) or auto (gpt-image-1).
	Size string `json:"size,omitempty"`

	// The quality of the image that will be generated.
	// high, medium, or low for gpt-image-1.
	// Defaults to auto.
	Quality string `json:"quality,omitempty"`

	// The format in which the generated images are returned. Must be one of url or b64_json.
	// URLs are only valid for 60 minutes after the image has been generated.
	// This parameter isn't supported for gpt-image-1.
	// Defaults to url.
	ResponseFormat string `json:"response_format,omitempty"`

	// A unique identifier representing your end-user, which can help OpenAI to monitor and detect abuse.
	User string `json:"user,omitempty"`

	// The output format of the image. Either png, webp, or jpeg.
	// This parameter is only supported for gpt-image-1.
	// Defaults to png.
	OutputFormat string `json:"output_format,omitempty"`

	// The background parameter for the edited image. Either transparent, opaque, or auto.
	// This parameter is only supported for gpt-image-1.
	// Defaults to auto.
	Background string `json:"background,omitempty"`

	// Control the content-moderation level. Must be either low or auto.
	// This parameter is only supported for gpt-image-1.
	// Defaults to auto.
	Moderation string `json:"moderation,omitempty"`

	// The compression level (0-100%) for the generated images.
	// This parameter is only supported for gpt-image-1 with webp or jpeg output formats.
	OutputCompression *int `json:"output_compression,omitempty"`

	// The number of partial images to generate for streaming responses.
	// Value must be between 0 and 3. Defaults to 0.
	PartialImages int `json:"partial_images,omitempty"`
}

// ImageEditResponse represents the response body for /v1/images/edits.
// OpenAI returns the same ImagesResponse schema for both image generation and image edit endpoints,
// so we reuse ImageGenerationResponse directly.
// https://platform.openai.com/docs/api-reference/images/object
type ImageEditResponse = ImageGenerationResponse

// ImageEditResponseData represents a single edited image in the response.
// This is the same structure as ImageGenerationResponseData.
type ImageEditResponseData = ImageGenerationResponseData
