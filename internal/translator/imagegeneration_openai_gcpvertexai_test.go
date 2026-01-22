// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestOpenAIToGCPVertexAIImageTranslator_RequestBody_Basic(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	req := &openai.ImageGenerationRequest{
		Model:  "gemini-2.5-flash-image",
		Prompt: "a serene mountain landscape at sunrise",
		N:      1,
	}
	original, _ := json.Marshal(req)

	hm, bm, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)
	require.NotNil(t, hm)
	require.Len(t, hm, 2) // path and content-length headers
	require.Equal(t, pathHeaderName, hm[0].Key())
	require.Contains(t, hm[0].Value(), "publishers/google/models/gemini-2.5-flash-image:generateContent")
	require.Equal(t, contentLengthHeaderName, hm[1].Key())

	require.NotNil(t, bm)
	// Verify the body can be unmarshaled as Gemini request
	var geminiReq struct {
		Contents []genai.Content `json:"contents"`
	}
	require.NoError(t, json.Unmarshal(bm, &geminiReq))
	require.Len(t, geminiReq.Contents, 1)
	require.Equal(t, "user", geminiReq.Contents[0].Role)
	require.Len(t, geminiReq.Contents[0].Parts, 1)
}

func TestOpenAIToGCPVertexAIImageTranslator_RequestBody_ModelOverride(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("gemini-3-pro-image-preview")
	req := &openai.ImageGenerationRequest{
		Model:  "gemini-2.5-flash-image",
		Prompt: "a cat",
	}
	original, _ := json.Marshal(req)

	hm, bm, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)
	require.NotNil(t, hm)
	// Path should use the override model
	require.Contains(t, hm[0].Value(), "gemini-3-pro-image-preview")
	require.NotNil(t, bm)
}

func TestOpenAIToGCPVertexAIImageTranslator_RequestBody_WithParameters(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	req := &openai.ImageGenerationRequest{
		Model:   "gemini-2.5-flash-image",
		Prompt:  "a cat",
		N:       2,
		Quality: "high",
		Size:    "1024x1024",
	}
	original, _ := json.Marshal(req)

	hm, bm, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)
	require.NotNil(t, hm)
	require.NotNil(t, bm)

	// Verify generation config has candidate count set
	var geminiReq struct {
		Contents         []genai.Content         `json:"contents"`
		GenerationConfig *genai.GenerationConfig `json:"generation_config"`
	}
	require.NoError(t, json.Unmarshal(bm, &geminiReq))
	require.NotNil(t, geminiReq.GenerationConfig)
	require.Equal(t, int32(2), geminiReq.GenerationConfig.CandidateCount)
}

func TestOpenAIToGCPVertexAIImageTranslator_ResponseBody_Basic(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")

	// Set up request first to initialize requestModel
	req := &openai.ImageGenerationRequest{
		Model:  "gemini-2.5-flash-image",
		Prompt: "test",
	}
	original, _ := json.Marshal(req)
	_, _, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)

	// Create a mock Gemini response with image data
	imageData := []byte("fake-image-data")
	geminiResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{
							InlineData: &genai.Blob{
								MIMEType: "image/png",
								Data:     imageData,
							},
						},
					},
				},
			},
		},
		CreateTime: time.Unix(1736890000, 0),
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 20,
			TotalTokenCount:      30,
		},
	}

	buf, _ := json.Marshal(geminiResp)
	hm, bm, usage, responseModel, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
	require.NoError(t, err)
	require.NotNil(t, hm)
	require.NotNil(t, bm)

	// Verify OpenAI response format
	var openAIResp openai.ImageGenerationResponse
	require.NoError(t, json.Unmarshal(bm, &openAIResp))
	require.Len(t, openAIResp.Data, 1)
	require.NotEmpty(t, openAIResp.Data[0].B64JSON)

	// Verify base64 encoding
	decoded, err := base64.StdEncoding.DecodeString(openAIResp.Data[0].B64JSON)
	require.NoError(t, err)
	require.Equal(t, imageData, decoded)

	// Verify usage
	require.Equal(t, tokenUsageFrom(10, -1, -1, 20, 30), usage)
	require.NotEmpty(t, responseModel)
}

func TestOpenAIToGCPVertexAIImageTranslator_ResponseBody_WithURI(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")

	// Create a mock Gemini response with URI
	geminiResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{
							FileData: &genai.FileData{
								FileURI:  "https://example.com/image.png",
								MIMEType: "image/png",
							},
						},
					},
				},
			},
		},
		CreateTime: time.Unix(1736890000, 0),
	}

	buf, _ := json.Marshal(geminiResp)
	hm, bm, _, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
	require.NoError(t, err)
	require.NotNil(t, hm)
	require.NotNil(t, bm)

	// Verify OpenAI response format
	var openAIResp openai.ImageGenerationResponse
	require.NoError(t, json.Unmarshal(bm, &openAIResp))
	require.Len(t, openAIResp.Data, 1)
	require.Equal(t, "https://example.com/image.png", openAIResp.Data[0].URL)
	require.Empty(t, openAIResp.Data[0].B64JSON)
}

func TestOpenAIToGCPVertexAIImageTranslator_ResponseBody_MultipleImages(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")

	// Create a mock Gemini response with multiple images
	geminiResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{
							InlineData: &genai.Blob{
								MIMEType: "image/png",
								Data:     []byte("image1"),
							},
						},
					},
				},
			},
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{
							InlineData: &genai.Blob{
								MIMEType: "image/png",
								Data:     []byte("image2"),
							},
						},
					},
				},
			},
		},
		CreateTime: time.Unix(1736890000, 0),
	}

	buf, _ := json.Marshal(geminiResp)
	hm, bm, _, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
	require.NoError(t, err)
	require.NotNil(t, hm)
	require.NotNil(t, bm)

	// Verify OpenAI response format
	var openAIResp openai.ImageGenerationResponse
	require.NoError(t, json.Unmarshal(bm, &openAIResp))
	require.Len(t, openAIResp.Data, 2)
}

func TestOpenAIToGCPVertexAIImageTranslator_ResponseBody_NoImages(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")

	// Create a mock Gemini response with no image data
	geminiResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{},
				},
			},
		},
		CreateTime: time.Unix(1736890000, 0),
	}

	buf, _ := json.Marshal(geminiResp)
	_, _, _, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no image data found")
}

func TestOpenAIToGCPVertexAIImageTranslator_ResponseBody_DecodeError(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	_, _, _, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader([]byte("not-json")), false, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to decode Gemini response")
}

func TestOpenAIToGCPVertexAIImageTranslator_ResponseError_GCPError(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")

	gcpError := gcpVertexAIError{
		Error: gcpVertexAIErrorDetails{
			Code:    400,
			Message: "Invalid request",
			Status:  "INVALID_ARGUMENT",
		},
	}
	buf, _ := json.Marshal(gcpError)

	headers := map[string]string{statusHeaderName: "400"}
	hm, bm, err := tr.ResponseError(headers, bytes.NewReader(buf))
	require.NoError(t, err)
	require.NotNil(t, hm)
	require.NotNil(t, bm)

	// Verify OpenAI error format
	var openAIError openai.Error
	require.NoError(t, json.Unmarshal(bm, &openAIError))
	require.Equal(t, "INVALID_ARGUMENT", openAIError.Error.Type)
	require.Contains(t, openAIError.Error.Message, "Invalid request")
}

func TestOpenAIToGCPVertexAIImageTranslator_ResponseError_PlainText(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")

	headers := map[string]string{statusHeaderName: "503"}
	hm, bm, err := tr.ResponseError(headers, bytes.NewReader([]byte("Service unavailable")))
	require.NoError(t, err)
	require.NotNil(t, hm)
	require.NotNil(t, bm)

	// Verify OpenAI error format
	var openAIError openai.Error
	require.NoError(t, json.Unmarshal(bm, &openAIError))
	require.Equal(t, gcpVertexAIBackendError, openAIError.Error.Type)
	require.Equal(t, "Service unavailable", openAIError.Error.Message)
}

func TestOpenAIToGCPVertexAIImageTranslator_ResponseHeaders_NoOp(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	hm, err := tr.ResponseHeaders(map[string]string{"foo": "bar"})
	require.NoError(t, err)
	require.Nil(t, hm)
}

func TestOpenAIToGCPVertexAIImageTranslator_ResponseBody_RecordsSpan(t *testing.T) {
	mockSpan := &mockImageGenerationSpan{}
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")

	geminiResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{
							InlineData: &genai.Blob{
								MIMEType: "image/png",
								Data:     []byte("test"),
							},
						},
					},
				},
			},
		},
		CreateTime: time.Unix(1736890000, 0),
	}

	buf, _ := json.Marshal(geminiResp)
	_, _, _, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, mockSpan)
	require.NoError(t, err)
	require.NotNil(t, mockSpan.recordedResponse)
}

func TestOpenAIToGCPVertexAIImageTranslator_ResponseBody_ModelVersion(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("gemini-2.5-flash-image")

	geminiResp := &genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{
							InlineData: &genai.Blob{
								MIMEType: "image/png",
								Data:     []byte("test"),
							},
						},
					},
				},
			},
		},
		CreateTime:   time.Unix(1736890000, 0),
		ModelVersion: "gemini-2.5-flash-image-001",
	}

	buf, _ := json.Marshal(geminiResp)
	_, _, _, responseModel, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
	require.NoError(t, err)
	require.Equal(t, "gemini-2.5-flash-image-001", responseModel)
}

func TestIsImagenModel(t *testing.T) {
	tests := []struct {
		model    string
		expected bool
	}{
		{"imagen-4.0-generate-001", true},
		{"imagen-3.0-generate-002", true},
		{"imagen-4.0-fast-generate-001", true},
		{"gemini-2.5-flash-image", false},
		{"gemini-3-pro-image-preview", false},
		{"gpt-image-1-mini", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			result := isImagenModel(tt.model)
			require.Equal(t, tt.expected, result)
		})
	}
}
