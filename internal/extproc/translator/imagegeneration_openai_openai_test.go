// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"io"
	"testing"

	openaisdk "github.com/openai/openai-go/v2"
	"github.com/stretchr/testify/require"
)

func TestOpenAIToOpenAIImageTranslator_RequestBody_ModelOverrideAndPath(t *testing.T) {
	tr := NewImageGenerationOpenAIToOpenAITranslator("v1", "gpt-image-1", nil)
	req := &openaisdk.ImageGenerateParams{Model: openaisdk.ImageModelDallE3, Prompt: "a cat"}
	original, _ := json.Marshal(req)

	hm, bm, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)
	require.NotNil(t, hm)
	require.Len(t, hm.SetHeaders, 2) // path and content-length headers
	require.Equal(t, ":path", hm.SetHeaders[0].Header.Key)
	require.Equal(t, "/v1/images/generations", string(hm.SetHeaders[0].Header.RawValue))
	require.Equal(t, "content-length", hm.SetHeaders[1].Header.Key)

	require.NotNil(t, bm)
	mutated := bm.GetBody()
	var got openaisdk.ImageGenerateParams
	require.NoError(t, json.Unmarshal(mutated, &got))
	require.Equal(t, "gpt-image-1", got.Model)
}

func TestOpenAIToOpenAIImageTranslator_RequestBody_ForceMutation(t *testing.T) {
	tr := NewImageGenerationOpenAIToOpenAITranslator("v1", "", nil)
	req := &openaisdk.ImageGenerateParams{Model: openaisdk.ImageModelDallE2, Prompt: "a cat"}
	original, _ := json.Marshal(req)

	hm, bm, err := tr.RequestBody(original, req, true)
	require.NoError(t, err)
	require.NotNil(t, hm)
	// Content-Length is set only when body mutated; with force it should be mutated to original.
	foundCL := false
	for _, h := range hm.SetHeaders {
		if h.Header.Key == "content-length" {
			foundCL = true
			break
		}
	}
	require.True(t, foundCL)
	require.NotNil(t, bm)
	require.Equal(t, original, bm.GetBody())
}

func TestOpenAIToOpenAIImageTranslator_ResponseError_NonJSON(t *testing.T) {
	tr := NewImageGenerationOpenAIToOpenAITranslator("v1", "", nil)
	headers := map[string]string{contentTypeHeaderName: "text/plain", statusHeaderName: "503"}
	hm, bm, err := tr.ResponseError(headers, bytes.NewReader([]byte("backend error")))
	require.NoError(t, err)
	require.NotNil(t, hm)
	require.NotNil(t, bm)

	// Body should be OpenAI error JSON
	var got ImageGenerationError
	require.NoError(t, json.Unmarshal(bm.GetBody(), &got))
	require.Equal(t, openAIBackendError, got.Error.Type)
}

func TestOpenAIToOpenAIImageTranslator_ResponseBody_OK(t *testing.T) {
	tr := NewImageGenerationOpenAIToOpenAITranslator("v1", "", nil)
	resp := &openaisdk.ImagesResponse{Size: openaisdk.ImagesResponseSize1024x1024}
	buf, _ := json.Marshal(resp)
	hm, bm, usage, metadata, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), true)
	require.NoError(t, err)
	require.Nil(t, hm)
	require.Nil(t, bm)
	require.Equal(t, uint32(0), usage.InputTokens)
	require.Equal(t, uint32(0), usage.TotalTokens)
	require.Equal(t, 0, metadata.ImageCount)
	require.Empty(t, metadata.Model)
	require.Equal(t, string(openaisdk.ImagesResponseSize1024x1024), metadata.Size)
}

func TestOpenAIToOpenAIImageTranslator_RequestBody_NoOverrideNoForce(t *testing.T) {
	tr := NewImageGenerationOpenAIToOpenAITranslator("v1", "", nil)
	req := &openaisdk.ImageGenerateParams{Model: openaisdk.ImageModelDallE2, Prompt: "a cat"}
	original, _ := json.Marshal(req)

	hm, bm, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)
	require.NotNil(t, hm)
	// Only path header present; content-length should not be set when no mutation
	require.Len(t, hm.SetHeaders, 1)
	require.Equal(t, ":path", hm.SetHeaders[0].Header.Key)
	require.Nil(t, bm)
}

func TestOpenAIToOpenAIImageTranslator_ResponseError_JSONPassthrough(t *testing.T) {
	tr := NewImageGenerationOpenAIToOpenAITranslator("v1", "", nil)
	headers := map[string]string{contentTypeHeaderName: jsonContentType, statusHeaderName: "500"}
	// Already JSON — should be passed through (no mutation)
	hm, bm, err := tr.ResponseError(headers, bytes.NewReader([]byte(`{"error":"msg"}`)))
	require.NoError(t, err)
	require.Nil(t, hm)
	require.Nil(t, bm)
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestOpenAIToOpenAIImageTranslator_ResponseError_ReadError(t *testing.T) {
	tr := NewImageGenerationOpenAIToOpenAITranslator("v1", "", nil)
	headers := map[string]string{statusHeaderName: "503"}
	hm, bm, err := tr.ResponseError(headers, errReader{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to read error body")
	require.Nil(t, hm)
	require.Nil(t, bm)
}

func TestOpenAIToOpenAIImageTranslator_ResponseBody_ModelPropagatesFromRequest(t *testing.T) {
	// Use override so effective model differs from original
	tr := NewImageGenerationOpenAIToOpenAITranslator("v1", "gpt-image-1", nil)
	req := &openaisdk.ImageGenerateParams{Model: openaisdk.ImageModelDallE3, Prompt: "a cat"}
	original, _ := json.Marshal(req)
	// Call RequestBody first to set requestModel inside translator
	_, _, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)

	resp := &openaisdk.ImagesResponse{
		// Two images returned
		Data: make([]openaisdk.Image, 2),
		Size: openaisdk.ImagesResponseSize1024x1024,
	}
	buf, _ := json.Marshal(resp)
	_, _, _, md, err := tr.ResponseBody(map[string]string{}, bytes.NewReader(buf), true)
	require.NoError(t, err)
	require.Equal(t, 2, md.ImageCount)
	require.Equal(t, "gpt-image-1", md.Model)
	require.Equal(t, string(openaisdk.ImagesResponseSize1024x1024), md.Size)
}

func TestOpenAIToOpenAIImageTranslator_ResponseHeaders_NoOp(t *testing.T) {
	tr := NewImageGenerationOpenAIToOpenAITranslator("v1", "", nil)
	hm, err := tr.ResponseHeaders(map[string]string{"foo": "bar"})
	require.NoError(t, err)
	require.Nil(t, hm)
}

func TestOpenAIToOpenAIImageTranslator_ResponseBody_DecodeError(t *testing.T) {
	tr := NewImageGenerationOpenAIToOpenAITranslator("v1", "", nil)
	_, _, _, _, err := tr.ResponseBody(map[string]string{}, bytes.NewReader([]byte("not-json")), true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to decode response body")
}
