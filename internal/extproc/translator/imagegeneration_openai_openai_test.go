// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
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
