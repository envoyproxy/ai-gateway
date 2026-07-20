// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	gcpschema "github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestOpenAIToGoogleAIStudioImageTranslator_RequestBody(t *testing.T) {
	t.Run("builds generateContent request and path", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		req := &openai.ImageGenerationRequest{Model: "gemini-2.5-flash-image", Prompt: "a cat"}

		hm, bm, err := tr.RequestBody(nil, req, false)
		require.NoError(t, err)

		// Path and content-length headers.
		require.Len(t, hm, 2)
		require.Equal(t, pathHeaderName, hm[0].Key())
		require.Equal(t, "/v1beta/models/gemini-2.5-flash-image:generateContent", hm[0].Value())
		require.Equal(t, contentLengthHeaderName, hm[1].Key())

		var got gcpschema.GenerateContentRequest
		require.NoError(t, json.Unmarshal(bm, &got))
		require.Len(t, got.Contents, 1)
		require.Equal(t, genai.RoleUser, got.Contents[0].Role)
		require.Len(t, got.Contents[0].Parts, 1)
		require.Equal(t, "a cat", got.Contents[0].Parts[0].Text)
		require.NotNil(t, got.GenerationConfig)
		require.Equal(t, []genai.Modality{genai.ModalityImage, genai.ModalityText}, got.GenerationConfig.ResponseModalities)
	})

	t.Run("model name override takes precedence", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "override-model")
		req := &openai.ImageGenerationRequest{Model: "gemini-2.5-flash-image", Prompt: "a cat"}

		hm, _, err := tr.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Equal(t, "/v1beta/models/override-model:generateContent", hm[0].Value())
	})

	t.Run("defaults schema version to v1beta", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("", "")
		req := &openai.ImageGenerationRequest{Model: "gemini-2.5-flash-image", Prompt: "a cat"}

		hm, _, err := tr.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.Equal(t, "/v1beta/models/gemini-2.5-flash-image:generateContent", hm[0].Value())
	})
}

func TestOpenAIToGoogleAIStudioImageTranslator_ResponseBody(t *testing.T) {
	imgBytes := []byte{0x89, 0x50, 0x4e, 0x47} // arbitrary raw image bytes.

	newResp := func() *genai.GenerateContentResponse {
		return &genai.GenerateContentResponse{
			ModelVersion: "gemini-2.5-flash-image",
			Candidates: []*genai.Candidate{
				{
					Content: &genai.Content{
						Parts: []*genai.Part{
							{InlineData: &genai.Blob{Data: imgBytes}},
						},
					},
				},
			},
			UsageMetadata: &genai.GenerateContentResponseUsageMetadata{
				PromptTokenCount:     11,
				CandidatesTokenCount: 22,
				TotalTokenCount:      33,
			},
		}
	}

	t.Run("base64-encodes inlineData into b64_json", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		buf, err := json.Marshal(newResp())
		require.NoError(t, err)

		hm, bm, usage, respModel, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
		require.NoError(t, err)
		require.Len(t, hm, 1)
		require.Equal(t, contentLengthHeaderName, hm[0].Key())
		require.Equal(t, "gemini-2.5-flash-image", respModel)
		require.Equal(t, tokenUsageFrom(11, -1, -1, 22, 33, -1), usage)

		var got openai.ImageGenerationResponse
		require.NoError(t, json.Unmarshal(bm, &got))
		require.Len(t, got.Data, 1)
		require.Equal(t, base64.StdEncoding.EncodeToString(imgBytes), got.Data[0].B64JSON)
	})

	t.Run("records span", func(t *testing.T) {
		mockSpan := &mockImageGenerationSpan{}
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		buf, err := json.Marshal(newResp())
		require.NoError(t, err)

		_, _, _, _, err = tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, mockSpan)
		require.NoError(t, err)
		require.NotNil(t, mockSpan.recordedResponse)
	})

	t.Run("errors when no image data present", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		resp := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{Content: &genai.Content{Parts: []*genai.Part{genai.NewPartFromText("no image")}}}},
		}
		buf, err := json.Marshal(resp)
		require.NoError(t, err)

		_, _, _, _, err = tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "no image data")
	})

	t.Run("errors on malformed response", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		_, _, _, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader([]byte("not-json")), false, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to decode Google AI Studio response")
	})
}

func TestOpenAIToGoogleAIStudioImageTranslator_ResponseHeaders_NoOp(t *testing.T) {
	tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
	hm, err := tr.ResponseHeaders(map[string]string{"foo": "bar"})
	require.NoError(t, err)
	require.Nil(t, hm)
}

func TestOpenAIToGoogleAIStudioImageTranslator_ResponseError(t *testing.T) {
	tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
	headers := map[string]string{contentTypeHeaderName: "text/plain", statusHeaderName: "503"}
	hm, bm, err := tr.ResponseError(headers, bytes.NewReader([]byte("backend error")))
	require.NoError(t, err)
	require.NotNil(t, hm)
	require.NotNil(t, bm)

	var actual struct {
		Error openai.ErrorType `json:"error"`
	}
	require.NoError(t, json.Unmarshal(bm, &actual))
	require.Equal(t, openAIBackendError, actual.Error.Type)
}
