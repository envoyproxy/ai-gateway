// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestNewImageEditOpenAIToOpenAITranslator(t *testing.T) {
	t.Run("path header set", func(t *testing.T) {
		translator := NewImageEditOpenAIToOpenAITranslator("v1", "")
		req := &openai.ImageEditRequest{
			Prompt: "A sunlit indoor lounge area with a pool",
			Model:  "dall-e-2",
		}
		hm, _, err := translator.RequestBody([]byte(`{"prompt":"A sunlit indoor lounge area with a pool","model":"dall-e-2"}`), req, false)
		require.NoError(t, err)
		require.Len(t, hm, 1)
		require.Equal(t, "/v1/images/edits", hm[0].Value())
	})

	t.Run("model override", func(t *testing.T) {
		translator := NewImageEditOpenAIToOpenAITranslator("v1", "dalle-custom")
		req := &openai.ImageEditRequest{
			Prompt: "A sunlit indoor lounge area with a pool",
			Model:  "dall-e-2",
		}
		hm, body, err := translator.RequestBody([]byte(`{"prompt":"A sunlit indoor lounge area with a pool","model":"dall-e-2"}`), req, false)
		require.NoError(t, err)
		require.Len(t, hm, 2) // path header + content-length
		require.Contains(t, string(body), `"model":"dalle-custom"`)
	})

	t.Run("response body", func(t *testing.T) {
		translator := NewImageEditOpenAIToOpenAITranslator("v1", "")
		req := &openai.ImageEditRequest{
			Prompt: "A sunlit indoor lounge area with a pool",
			Model:  "dall-e-2",
		}
		_, _, _ = translator.RequestBody([]byte(`{"prompt":"test","model":"dall-e-2"}`), req, false)

		respBody := `{"created":1589478378,"data":[{"url":"https://example.com/image.png"}]}`
		_, _, usage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader([]byte(respBody)), false, nil)
		require.NoError(t, err)
		require.Equal(t, "dall-e-2", string(responseModel))
		require.Equal(t, tokenUsageFrom(-1, -1, -1, -1, -1), usage)
	})

	t.Run("response error", func(t *testing.T) {
		translator := NewImageEditOpenAIToOpenAITranslator("v1", "")
		errBody := `{"error":{"message":"Invalid image","type":"invalid_request_error"}}`
		headers, body, err := translator.ResponseError(nil, strings.NewReader(errBody))
		require.NoError(t, err)
		require.NotNil(t, headers) // Headers are set for content-type and content-length
		require.Contains(t, string(body), "Invalid image")
	})
}

func TestOpenAIToOpenAIImageEditTranslator_RequestBody_NoOverrideNoForce(t *testing.T) {
	tr := NewImageEditOpenAIToOpenAITranslator("v1", "")
	req := &openai.ImageEditRequest{Model: "dall-e-2", Prompt: "a cat"}
	original, _ := json.Marshal(req)

	hm, bm, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)
	require.NotNil(t, hm)
	// Only path header present; content-length should not be set when no mutation.
	require.Len(t, hm, 1)
	require.Equal(t, pathHeaderName, hm[0].Key())
	require.Nil(t, bm)
}

func TestOpenAIToOpenAIImageEditTranslator_RequestBody_ForceMutation(t *testing.T) {
	tr := NewImageEditOpenAIToOpenAITranslator("v1", "")
	req := &openai.ImageEditRequest{Model: "dall-e-2", Prompt: "a cat"}
	original, _ := json.Marshal(req)

	hm, bm, err := tr.RequestBody(original, req, true)
	require.NoError(t, err)
	require.NotNil(t, hm)
	// Content-Length is set only when body mutated; with force it should be mutated to original.
	foundCL := false
	for _, h := range hm {
		if h.Key() == contentLengthHeaderName {
			foundCL = true
			break
		}
	}
	require.True(t, foundCL)
	require.NotNil(t, bm)
	require.Equal(t, original, bm)
}

func TestOpenAIToOpenAIImageEditTranslator_ResponseHeaders_NoOp(t *testing.T) {
	tr := NewImageEditOpenAIToOpenAITranslator("v1", "")
	hm, err := tr.ResponseHeaders(map[string]string{"foo": "bar"})
	require.NoError(t, err)
	require.Nil(t, hm)
}

func TestOpenAIToOpenAIImageEditTranslator_ResponseBody_OK(t *testing.T) {
	tr := NewImageEditOpenAIToOpenAITranslator("v1", "")
	resp := &openai.ImageEditResponse{}
	buf, _ := json.Marshal(resp)
	hm, bm, usage, responseModel, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), true, nil)
	require.NoError(t, err)
	require.Nil(t, hm)
	require.Nil(t, bm)
	require.Equal(t, tokenUsageFrom(-1, -1, -1, -1, -1), usage)
	require.Empty(t, responseModel)
}

func TestOpenAIToOpenAIImageEditTranslator_ResponseBody_DecodeError(t *testing.T) {
	tr := NewImageEditOpenAIToOpenAITranslator("v1", "")
	_, _, _, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader([]byte("not-json")), true, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to decode response body")
}

func TestOpenAIToOpenAIImageEditTranslator_ResponseBody_ModelPropagatesFromRequest(t *testing.T) {
	// Use override so effective model differs from original.
	tr := NewImageEditOpenAIToOpenAITranslator("v1", "gpt-image-1")
	req := &openai.ImageEditRequest{Model: "dall-e-2", Prompt: "a cat"}
	original, _ := json.Marshal(req)
	// Call RequestBody first to set requestModel inside translator.
	_, _, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)

	resp := &openai.ImageEditResponse{
		Data: make([]openai.ImageEditResponseData, 2),
	}
	buf, _ := json.Marshal(resp)
	_, _, _, respModel, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), true, nil)
	require.NoError(t, err)
	require.Equal(t, "gpt-image-1", string(respModel))
}

func TestOpenAIToOpenAIImageEditTranslator_ResponseBody_RecordsSpan(t *testing.T) {
	mockSpan := &mockImageEditSpan{}
	tr := NewImageEditOpenAIToOpenAITranslator("v1", "")

	resp := &openai.ImageEditResponse{
		Data: []openai.ImageEditResponseData{{URL: "https://example.com/img.png"}},
	}
	buf, _ := json.Marshal(resp)
	_, _, _, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), true, mockSpan)
	require.NoError(t, err)
	require.NotNil(t, mockSpan.recordedResponse)
}

type mockImageEditSpan struct {
	recordedResponse *openai.ImageEditResponse
}

func (m *mockImageEditSpan) RecordResponse(resp *openai.ImageEditResponse) {
	m.recordedResponse = resp
}

func (m *mockImageEditSpan) EndSpanOnError(int, []byte)    {}
func (m *mockImageEditSpan) EndSpan()                      {}
func (m *mockImageEditSpan) RecordResponseChunk(*struct{}) {}

func TestOpenAIToOpenAIImageEditTranslator_ResponseBody_Usage(t *testing.T) {
	tr := NewImageEditOpenAIToOpenAITranslator("v1", "")
	resp := &openai.ImageEditResponse{
		Usage: &openai.ImageGenerationUsage{
			TotalTokens:  100,
			InputTokens:  40,
			OutputTokens: 60,
		},
	}
	buf, _ := json.Marshal(resp)
	_, _, usage, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), true, nil)
	require.NoError(t, err)
	require.Equal(t, tokenUsageFrom(40, -1, -1, 60, 100), usage)
}

func TestOpenAIToOpenAIImageEditTranslator_ResponseError_NonJSON(t *testing.T) {
	tr := NewImageEditOpenAIToOpenAITranslator("v1", "")
	headers := map[string]string{contentTypeHeaderName: "text/plain", statusHeaderName: "503"}
	hm, bm, err := tr.ResponseError(headers, bytes.NewReader([]byte("backend error")))
	require.NoError(t, err)
	require.NotNil(t, hm)
	require.NotNil(t, bm)

	// Body should be OpenAI error JSON.
	var actual struct {
		Error openai.ErrorType `json:"error"`
	}
	require.NoError(t, json.Unmarshal(bm, &actual))
	require.Equal(t, openAIBackendError, actual.Error.Type)
}

func TestOpenAIToOpenAIImageEditTranslator_ResponseError_JSONPassthrough(t *testing.T) {
	tr := NewImageEditOpenAIToOpenAITranslator("v1", "")
	headers := map[string]string{contentTypeHeaderName: jsonContentType, statusHeaderName: "500"}
	// Already JSON — should be passed through (no mutation).
	hm, bm, err := tr.ResponseError(headers, bytes.NewReader([]byte(`{"error":"msg"}`)))
	require.NoError(t, err)
	require.Nil(t, hm)
	require.Nil(t, bm)
}
