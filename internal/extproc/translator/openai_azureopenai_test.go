// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

func TestOpenAIToAzureOpenAITranslatorV1ChatCompletion_RequestBody(t *testing.T) {
	t.Run("valid body", func(t *testing.T) {
		for _, stream := range []bool{true, false} {
			t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
				originalReq := &openai.ChatCompletionRequest{Model: "foo-bar-ai", Stream: stream}

				o := &openAIToAzureOpenAITranslatorV1ChatCompletion{apiVersion: "some-version"}
				hm, bm, err := o.RequestBody(nil, originalReq, false)
				require.Nil(t, bm)
				require.NoError(t, err)
				require.Equal(t, stream, o.stream)
				require.NotNil(t, hm)

				require.Equal(t, ":path", hm.SetHeaders[0].Header.Key)
				require.Equal(t, "/openai/deployments/foo-bar-ai/chat/completions?api-version=some-version", string(hm.SetHeaders[0].Header.RawValue))
			})
		}
	})
	t.Run("model override", func(t *testing.T) {
		modelName := "gpt-4-turbo-2024-04-09"
		originalReq := &openai.ChatCompletionRequest{Model: "gpt-4-turbo", Stream: false}
		o := &openAIToAzureOpenAITranslatorV1ChatCompletion{
			apiVersion: "some-version",
			openAIToOpenAITranslatorV1ChatCompletion: openAIToOpenAITranslatorV1ChatCompletion{
				modelNameOverride: modelName,
			},
		}
		hm, bm, err := o.RequestBody(nil, originalReq, false)
		require.Nil(t, bm)
		require.NoError(t, err)
		require.NotNil(t, hm)
		require.Len(t, hm.SetHeaders, 1)
		require.Equal(t, ":path", hm.SetHeaders[0].Header.Key)
		require.Equal(t, "/openai/deployments/"+modelName+"/chat/completions?api-version=some-version", string(hm.SetHeaders[0].Header.RawValue))
	})
}
