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

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

// --- RequestBody tests ---

func TestGCPVertexAIImageGeneration_RequestBody_ImagenModel(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	req := &openai.ImageGenerationRequest{
		Model:  "imagen-4.0-generate-001",
		Prompt: "a cat sitting on a couch",
		N:      2,
		Size:   "1792x1024",
	}
	original, _ := json.Marshal(req)

	headers, body, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)
	require.NotNil(t, headers)
	require.Len(t, headers, 2)
	require.Equal(t, pathHeaderName, headers[0].Key())
	require.Contains(t, headers[0].Value(), "imagen-4.0-generate-001:predict")
	require.Equal(t, contentLengthHeaderName, headers[1].Key())

	var got gcp.ImagePredictRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got.Instances, 1)
	require.Equal(t, "a cat sitting on a couch", got.Instances[0].Prompt)
	require.Equal(t, 2, got.Parameters.SampleCount)
	require.Equal(t, "16:9", got.Parameters.AspectRatio)
}

func TestGCPVertexAIImageGeneration_RequestBody_GeminiModel(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	req := &openai.ImageGenerationRequest{
		Model:  "gemini-2.0-flash-exp",
		Prompt: "a dog playing fetch",
		N:      3,
	}
	original, _ := json.Marshal(req)

	headers, body, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)
	require.NotNil(t, headers)
	require.Contains(t, headers[0].Value(), "gemini-2.0-flash-exp:generateContent")

	var got gcp.GenerateContentRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got.Contents, 1)
	require.Equal(t, "a dog playing fetch", got.Contents[0].Parts[0].Text)
	require.Equal(t, int32(3), got.GenerationConfig.CandidateCount)
}

func TestGCPVertexAIImageGeneration_RequestBody_ModelOverride(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("imagen-3.0-generate-001")
	req := &openai.ImageGenerationRequest{
		Model:  "dall-e-3",
		Prompt: "a sunset",
	}
	original, _ := json.Marshal(req)

	headers, _, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)
	require.Contains(t, headers[0].Value(), "imagen-3.0-generate-001:predict")
}

func TestGCPVertexAIImageGeneration_RequestBody_DefaultN(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	req := &openai.ImageGenerationRequest{
		Model:  "imagen-4.0-generate-001",
		Prompt: "a tree",
	}
	original, _ := json.Marshal(req)

	_, body, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)

	var got gcp.ImagePredictRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.Equal(t, 1, got.Parameters.SampleCount)
}

func TestGCPVertexAIImageGeneration_RequestBody_OutputOptions(t *testing.T) {
	compression := 80
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	req := &openai.ImageGenerationRequest{
		Model:             "imagen-4.0-generate-001",
		Prompt:            "a flower",
		OutputFormat:      "jpeg",
		OutputCompression: &compression,
	}
	original, _ := json.Marshal(req)

	_, body, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)

	var got gcp.ImagePredictRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.NotNil(t, got.Parameters.OutputOptions)
	require.Equal(t, "image/jpeg", got.Parameters.OutputOptions.MIMEType)
	require.Equal(t, 80, got.Parameters.OutputOptions.CompressionQuality)
}

func TestGCPVertexAIImageGeneration_RequestBody_NoOutputOptionsWhenUnsupported(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	req := &openai.ImageGenerationRequest{
		Model:        "imagen-4.0-generate-001",
		Prompt:       "a mountain",
		OutputFormat: "webp", // webp not supported, should not create OutputOptions
	}
	original, _ := json.Marshal(req)

	_, body, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)

	var got gcp.ImagePredictRequest
	require.NoError(t, json.Unmarshal(body, &got))
	require.Nil(t, got.Parameters.OutputOptions)
}

// --- ResponseBody tests ---

func TestGCPVertexAIImageGeneration_ResponseBody_Imagen(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	// Set up requestModel by calling RequestBody first.
	req := &openai.ImageGenerationRequest{Model: "imagen-4.0-generate-001", Prompt: "test"}
	original, _ := json.Marshal(req)
	_, _, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)

	resp := gcp.ImagePredictionResponse{
		Predictions: []*gcp.ImagePrediction{
			{MIMEType: "image/png", BytesBase64Encoded: "aW1hZ2VkYXRh", Prompt: "enhanced prompt"},
			{MIMEType: "image/png", BytesBase64Encoded: "aW1hZ2VkYXRhMg=="},
		},
	}
	respBody, _ := json.Marshal(resp)

	headers, body, tokenUsage, responseModel, err := tr.ResponseBody(nil, bytes.NewReader(respBody), false, nil)
	require.NoError(t, err)
	require.NotNil(t, headers)
	require.Equal(t, contentLengthHeaderName, headers[0].Key())
	require.Equal(t, "imagen-4.0-generate-001", string(responseModel))

	var got openai.ImageGenerationResponse
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got.Data, 2)
	require.Equal(t, "aW1hZ2VkYXRh", got.Data[0].B64JSON)
	require.Equal(t, "enhanced prompt", got.Data[0].RevisedPrompt)
	require.Equal(t, "aW1hZ2VkYXRhMg==", got.Data[1].B64JSON)
	require.Equal(t, "png", got.OutputFormat)
	require.True(t, got.Created > 0)

	// Imagen has no token usage.
	inputTokens, inputOk := tokenUsage.InputTokens()
	outputTokens, outputOk := tokenUsage.OutputTokens()
	require.False(t, inputOk)
	require.False(t, outputOk)
	require.Zero(t, inputTokens)
	require.Zero(t, outputTokens)
}

func TestGCPVertexAIImageGeneration_ResponseBody_Imagen_RAIFiltered(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	req := &openai.ImageGenerationRequest{Model: "imagen-4.0-generate-001", Prompt: "test"}
	original, _ := json.Marshal(req)
	_, _, _ = tr.RequestBody(original, req, false)

	// One image filtered by RAI (empty BytesBase64Encoded), one valid.
	resp := gcp.ImagePredictionResponse{
		Predictions: []*gcp.ImagePrediction{
			{MIMEType: "", BytesBase64Encoded: ""},
			{MIMEType: "image/jpeg", BytesBase64Encoded: "dmFsaWQ="},
		},
	}
	respBody, _ := json.Marshal(resp)

	_, body, _, _, err := tr.ResponseBody(nil, bytes.NewReader(respBody), false, nil)
	require.NoError(t, err)

	var got openai.ImageGenerationResponse
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got.Data, 1)
	require.Equal(t, "dmFsaWQ=", got.Data[0].B64JSON)
	require.Equal(t, "jpeg", got.OutputFormat)
}

func TestGCPVertexAIImageGeneration_ResponseBody_Gemini(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	req := &openai.ImageGenerationRequest{Model: "gemini-2.0-flash-exp", Prompt: "test"}
	original, _ := json.Marshal(req)
	_, _, _ = tr.RequestBody(original, req, false)

	imageBytes := []byte("fake-image-data")
	createTime := time.Date(2025, 1, 15, 10, 30, 0, 0, time.UTC)
	resp := genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{InlineData: &genai.Blob{MIMEType: "image/png", Data: imageBytes}},
					},
				},
			},
		},
		UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:     10,
			CandidatesTokenCount: 50,
			TotalTokenCount:      60,
		},
		CreateTime: createTime,
	}
	respBody, _ := json.Marshal(resp)

	_, body, tokenUsage, responseModel, err := tr.ResponseBody(nil, bytes.NewReader(respBody), false, nil)
	require.NoError(t, err)
	require.Equal(t, "gemini-2.0-flash-exp", string(responseModel))

	var got openai.ImageGenerationResponse
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got.Data, 1)
	require.Equal(t, base64.StdEncoding.EncodeToString(imageBytes), got.Data[0].B64JSON)
	require.Equal(t, "png", got.OutputFormat)
	require.Equal(t, createTime.Unix(), got.Created)

	require.NotNil(t, got.Usage)
	require.Equal(t, 10, got.Usage.InputTokens)
	require.Equal(t, 50, got.Usage.OutputTokens)
	require.Equal(t, 60, got.Usage.TotalTokens)

	inputTokens, _ := tokenUsage.InputTokens()
	outputTokens, _ := tokenUsage.OutputTokens()
	require.Equal(t, uint32(10), inputTokens)
	require.Equal(t, uint32(50), outputTokens)
}

func TestGCPVertexAIImageGeneration_ResponseBody_Gemini_NilContent(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	req := &openai.ImageGenerationRequest{Model: "gemini-2.0-flash-exp", Prompt: "test"}
	original, _ := json.Marshal(req)
	_, _, _ = tr.RequestBody(original, req, false)

	resp := genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{Content: nil}, // nil content should be skipped
		},
	}
	respBody, _ := json.Marshal(resp)

	_, body, _, _, err := tr.ResponseBody(nil, bytes.NewReader(respBody), false, nil)
	require.NoError(t, err)

	var got openai.ImageGenerationResponse
	require.NoError(t, json.Unmarshal(body, &got))
	require.Empty(t, got.Data)
}

func TestGCPVertexAIImageGeneration_ResponseBody_Gemini_TextPartSkipped(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	req := &openai.ImageGenerationRequest{Model: "gemini-2.0-flash-exp", Prompt: "test"}
	original, _ := json.Marshal(req)
	_, _, _ = tr.RequestBody(original, req, false)

	resp := genai.GenerateContentResponse{
		Candidates: []*genai.Candidate{
			{
				Content: &genai.Content{
					Parts: []*genai.Part{
						{Text: "some text"}, // text part, no InlineData — should be skipped
						{InlineData: &genai.Blob{MIMEType: "image/png", Data: []byte("img")}},
					},
				},
			},
		},
	}
	respBody, _ := json.Marshal(resp)

	_, body, _, _, err := tr.ResponseBody(nil, bytes.NewReader(respBody), false, nil)
	require.NoError(t, err)

	var got openai.ImageGenerationResponse
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got.Data, 1)
}

func TestGCPVertexAIImageGeneration_ResponseBody_DecodeError(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	// Imagen path
	req := &openai.ImageGenerationRequest{Model: "imagen-4.0-generate-001", Prompt: "test"}
	original, _ := json.Marshal(req)
	_, _, _ = tr.RequestBody(original, req, false)

	_, _, _, _, err := tr.ResponseBody(nil, bytes.NewReader([]byte("not-json")), false, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to decode response")
}

func TestGCPVertexAIImageGeneration_ResponseBody_RecordsSpan(t *testing.T) {
	mockSpan := &mockImageGenerationSpan{}
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	req := &openai.ImageGenerationRequest{Model: "imagen-4.0-generate-001", Prompt: "test"}
	original, _ := json.Marshal(req)
	_, _, _ = tr.RequestBody(original, req, false)

	resp := gcp.ImagePredictionResponse{
		Predictions: []*gcp.ImagePrediction{
			{MIMEType: "image/png", BytesBase64Encoded: "aW1hZ2VkYXRh"},
		},
	}
	respBody, _ := json.Marshal(resp)

	_, _, _, _, err := tr.ResponseBody(nil, bytes.NewReader(respBody), false, mockSpan)
	require.NoError(t, err)
	require.NotNil(t, mockSpan.recordedResponse)
	require.Len(t, mockSpan.recordedResponse.Data, 1)
}

// --- ResponseHeaders / ResponseError tests ---

func TestGCPVertexAIImageGeneration_ResponseHeaders_Nil(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	headers, err := tr.ResponseHeaders(map[string]string{"foo": "bar"})
	require.NoError(t, err)
	require.Nil(t, headers)
}

func TestGCPVertexAIImageGeneration_ResponseError(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	respHeaders := map[string]string{contentTypeHeaderName: "text/plain", statusHeaderName: "500"}
	headers, body, err := tr.ResponseError(respHeaders, bytes.NewReader([]byte("backend error")))
	require.NoError(t, err)
	require.NotNil(t, headers)
	require.NotNil(t, body)

	var actual struct {
		Error openai.ErrorType `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &actual))
	require.Equal(t, gcpVertexAIBackendError, actual.Error.Type)
}

func TestGCPVertexAIImageGeneration_ResponseError_JSONPassthrough(t *testing.T) {
	tr := NewImageGenerationOpenAIToGCPVertexAITranslator("")
	// GCP error handler always converts the response to OpenAI error format,
	// even when the input is already JSON (unlike the OpenAI translator).
	headers := map[string]string{contentTypeHeaderName: jsonContentType, statusHeaderName: "400"}
	gcpErr := `{"error":{"code":400,"message":"Invalid request","status":"INVALID_ARGUMENT"}}`
	hm, bm, err := tr.ResponseError(headers, bytes.NewReader([]byte(gcpErr)))
	require.NoError(t, err)
	require.NotNil(t, hm)
	require.NotNil(t, bm)

	var actual struct {
		Error openai.ErrorType `json:"error"`
	}
	require.NoError(t, json.Unmarshal(bm, &actual))
	require.Equal(t, "INVALID_ARGUMENT", actual.Error.Type)
	require.Contains(t, actual.Error.Message, "Invalid request")
}

// --- Helper function tests ---

func TestSizeToAspectRatio(t *testing.T) {
	tests := []struct {
		size     string
		expected string
	}{
		{"1792x1024", "16:9"},
		{"1024x1792", "9:16"},
		{"1536x1024", "4:3"},
		{"1024x1536", "3:4"},
		{"1024x1024", "1:1"},
		{"512x512", "1:1"},
		{"256x256", "1:1"},
		{"", "1:1"},
	}
	for _, tc := range tests {
		t.Run(tc.size, func(t *testing.T) {
			require.Equal(t, tc.expected, sizeToAspectRatio(tc.size))
		})
	}
}

func TestOutputFormatToMIMEType(t *testing.T) {
	tests := []struct {
		format   string
		expected string
	}{
		{"png", "image/png"},
		{"jpeg", "image/jpeg"},
		{"webp", ""},
		{"", ""},
		{"gif", ""},
	}
	for _, tc := range tests {
		t.Run(tc.format, func(t *testing.T) {
			require.Equal(t, tc.expected, outputFormatToMIMEType(tc.format))
		})
	}
}

func TestQualityToImageSize(t *testing.T) {
	tests := []struct {
		quality  string
		expected string
	}{
		{"low", "1K"},
		{"medium", "2K"},
		{"high", "4K"},
		{"standard", "1K"},
		{"hd", "2K"},
		{"", ""},
		{"unknown", ""},
	}
	for _, tc := range tests {
		t.Run(tc.quality, func(t *testing.T) {
			require.Equal(t, tc.expected, qualityToImageSize(tc.quality))
		})
	}
}
