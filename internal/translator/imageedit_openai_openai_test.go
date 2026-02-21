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
		_, _, _, responseModel, err := translator.ResponseBody(nil, bytes.NewReader([]byte(respBody)), false, nil)
		require.NoError(t, err)
		require.Equal(t, "dall-e-2", string(responseModel))
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
