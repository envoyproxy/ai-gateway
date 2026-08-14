// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/base64"
	"math"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/genai"

	gcpschema "github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
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

	t.Run("maps n to candidateCount", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		req := &openai.ImageGenerationRequest{Model: "gemini-2.5-flash-image", Prompt: "a cat", N: 3}

		_, bm, err := tr.RequestBody(nil, req, false)
		require.NoError(t, err)

		var got gcpschema.GenerateContentRequest
		require.NoError(t, json.Unmarshal(bm, &got))
		require.Equal(t, int32(3), got.GenerationConfig.CandidateCount)
	})

	t.Run("leaves candidateCount unset when n is omitted", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		req := &openai.ImageGenerationRequest{Model: "gemini-2.5-flash-image", Prompt: "a cat"}

		_, bm, err := tr.RequestBody(nil, req, false)
		require.NoError(t, err)
		require.NotContains(t, string(bm), "candidateCount")
	})

	t.Run("accepts an explicit b64_json response_format", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		req := &openai.ImageGenerationRequest{Model: "gemini-2.5-flash-image", Prompt: "a cat", ResponseFormat: "b64_json"}

		_, _, err := tr.RequestBody(nil, req, false)
		require.NoError(t, err)
	})

	t.Run("rejects a url response_format", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		req := &openai.ImageGenerationRequest{Model: "gemini-2.5-flash-image", Prompt: "a cat", ResponseFormat: "url"}

		_, _, err := tr.RequestBody(nil, req, false)
		require.ErrorIs(t, err, internalapi.ErrInvalidRequestBody)
		require.Contains(t, err.Error(), "unsupported response_format")
	})

	t.Run("rejects stream", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		req := &openai.ImageGenerationRequest{Model: "gemini-2.5-flash-image", Prompt: "a cat", Stream: true}

		_, _, err := tr.RequestBody(nil, req, false)
		require.ErrorIs(t, err, internalapi.ErrInvalidRequestBody)
		require.Contains(t, err.Error(), "stream is not supported")
	})

	t.Run("rejects out of range n", func(t *testing.T) {
		// n is forwarded as candidateCount, an int32, so an out-of-range value must not be
		// narrowed silently.
		for _, n := range []int{-1, 11, math.MaxInt32 + 1} {
			tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
			req := &openai.ImageGenerationRequest{Model: "gemini-2.5-flash-image", Prompt: "a cat", N: n}

			_, _, err := tr.RequestBody(nil, req, false)
			require.ErrorIs(t, err, internalapi.ErrInvalidRequestBody, "n=%d", n)
			require.Contains(t, err.Error(), "n must be between 1 and 10")
		}
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
		require.Equal(t, &openai.ImageGenerationUsage{InputTokens: 11, OutputTokens: 22, TotalTokens: 33}, got.Usage)
	})

	t.Run("folds thinking tokens into output and reports them as reasoning", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		resp := newResp()
		resp.UsageMetadata = &genai.GenerateContentResponseUsageMetadata{
			PromptTokenCount:        11,
			CachedContentTokenCount: 3,
			CandidatesTokenCount:    22,
			ThoughtsTokenCount:      7,
			TotalTokenCount:         40,
		}
		buf, err := json.Marshal(resp)
		require.NoError(t, err)

		_, bm, usage, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
		require.NoError(t, err)
		require.Equal(t, tokenUsageFrom(11, 3, -1, 29, 40, 7), usage)

		var got openai.ImageGenerationResponse
		require.NoError(t, json.Unmarshal(bm, &got))
		require.Equal(t, &openai.ImageGenerationUsage{InputTokens: 11, OutputTokens: 29, TotalTokens: 40}, got.Usage)
	})

	t.Run("maps the prompt modality breakdown to input_tokens_details", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		resp := newResp()
		resp.UsageMetadata.PromptTokensDetails = []*genai.ModalityTokenCount{
			{Modality: genai.MediaModalityText, TokenCount: 6},
			{Modality: genai.MediaModalityImage, TokenCount: 5},
			// Modalities OpenAI has no field for are dropped rather than mis-attributed.
			{Modality: genai.MediaModalityAudio, TokenCount: 9},
		}
		buf, err := json.Marshal(resp)
		require.NoError(t, err)

		_, bm, _, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
		require.NoError(t, err)

		var got openai.ImageGenerationResponse
		require.NoError(t, json.Unmarshal(bm, &got))
		require.Equal(t, &openai.ImageGenerationInputTokensDetails{TextTokens: 6, ImageTokens: 5}, got.Usage.InputTokensDetails)
	})

	t.Run("omits input_tokens_details when nothing maps", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		resp := newResp()
		resp.UsageMetadata.PromptTokensDetails = []*genai.ModalityTokenCount{
			{Modality: genai.MediaModalityAudio, TokenCount: 9},
		}
		buf, err := json.Marshal(resp)
		require.NoError(t, err)

		_, bm, _, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
		require.NoError(t, err)
		require.NotContains(t, string(bm), "input_tokens_details")

		var got openai.ImageGenerationResponse
		require.NoError(t, json.Unmarshal(bm, &got))
		require.Nil(t, got.Usage.InputTokensDetails)
	})

	t.Run("omits usage when the response carries no usage metadata", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		resp := newResp()
		resp.UsageMetadata = nil
		buf, err := json.Marshal(resp)
		require.NoError(t, err)

		_, bm, usage, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
		require.NoError(t, err)
		require.Equal(t, tokenUsageFrom(-1, -1, -1, -1, -1, -1), usage)

		var got openai.ImageGenerationResponse
		require.NoError(t, json.Unmarshal(bm, &got))
		require.Nil(t, got.Usage)
	})

	t.Run("collects images across candidates", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		second := []byte{0xff, 0xd8, 0xff, 0xe0}
		resp := newResp()
		resp.Candidates = append(resp.Candidates,
			// A candidate with no content at all must be skipped rather than panic.
			&genai.Candidate{},
			&genai.Candidate{Content: &genai.Content{Parts: []*genai.Part{
				genai.NewPartFromText("some text"),
				{InlineData: &genai.Blob{Data: second}},
			}}},
		)
		buf, err := json.Marshal(resp)
		require.NoError(t, err)

		_, bm, _, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
		require.NoError(t, err)

		var got openai.ImageGenerationResponse
		require.NoError(t, json.Unmarshal(bm, &got))
		require.Len(t, got.Data, 2)
		require.Equal(t, base64.StdEncoding.EncodeToString(imgBytes), got.Data[0].B64JSON)
		require.Equal(t, base64.StdEncoding.EncodeToString(second), got.Data[1].B64JSON)
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

	t.Run("surfaces the candidate finish reason when no image is returned", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		resp := &genai.GenerateContentResponse{
			Candidates: []*genai.Candidate{{FinishReason: genai.FinishReasonSafety}},
		}
		buf, err := json.Marshal(resp)
		require.NoError(t, err)

		_, _, _, _, err = tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
		require.ErrorContains(t, err, "candidate finish reasons: SAFETY")
	})

	t.Run("surfaces the prompt block reason when no image is returned", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		resp := &genai.GenerateContentResponse{
			PromptFeedback: &genai.GenerateContentResponsePromptFeedback{
				BlockReason:        genai.BlockedReasonSafety,
				BlockReasonMessage: "blocked by safety settings",
			},
		}
		buf, err := json.Marshal(resp)
		require.NoError(t, err)

		_, _, _, _, err = tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), false, nil)
		require.ErrorContains(t, err, "prompt blocked: SAFETY (blocked by safety settings)")
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
	decode := func(t *testing.T, bm []byte) openai.ErrorType {
		var actual struct {
			Error openai.ErrorType `json:"error"`
		}
		require.NoError(t, json.Unmarshal(bm, &actual))
		return actual.Error
	}

	t.Run("google error envelope is converted", func(t *testing.T) {
		// Gemini answers with Google's envelope, whose "code" is a number and which has no "type".
		// Passing it through would hand an OpenAI client a body it cannot parse.
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		headers := map[string]string{contentTypeHeaderName: jsonContentType, statusHeaderName: "400"}
		body := `{"error":{"code":400,"message":"API key not valid","status":"INVALID_ARGUMENT"}}`

		hm, bm, err := tr.ResponseError(headers, bytes.NewReader([]byte(body)))
		require.NoError(t, err)
		require.NotNil(t, hm)

		actual := decode(t, bm)
		require.Equal(t, "INVALID_ARGUMENT", actual.Type)
		require.Equal(t, "API key not valid", actual.Message)
		require.Equal(t, "400", *actual.Code)
	})

	t.Run("error details are appended to the message", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		headers := map[string]string{contentTypeHeaderName: jsonContentType, statusHeaderName: "429"}
		body := `{"error":{"code":429,"message":"quota exceeded","status":"RESOURCE_EXHAUSTED","details":[{"reason":"RATE_LIMIT"}]}}`

		_, bm, err := tr.ResponseError(headers, bytes.NewReader([]byte(body)))
		require.NoError(t, err)

		actual := decode(t, bm)
		require.Equal(t, "RESOURCE_EXHAUSTED", actual.Type)
		require.Contains(t, actual.Message, "quota exceeded")
		require.Contains(t, actual.Message, "RATE_LIMIT")
	})

	t.Run("non-envelope body falls back to the backend error type", func(t *testing.T) {
		tr := NewImageGenerationOpenAIToGoogleAIStudioTranslator("v1beta", "")
		headers := map[string]string{contentTypeHeaderName: "text/plain", statusHeaderName: "503"}

		hm, bm, err := tr.ResponseError(headers, bytes.NewReader([]byte("backend error")))
		require.NoError(t, err)
		require.NotNil(t, hm)

		actual := decode(t, bm)
		require.Equal(t, googleAIStudioBackendError, actual.Type)
		require.Equal(t, "backend error", actual.Message)
		require.Equal(t, "503", *actual.Code)
	})
}
