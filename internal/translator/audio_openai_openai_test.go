// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestNewAudioSpeechOpenAIToOpenAITranslator(t *testing.T) {
	translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "override-model")
	require.NotNil(t, translator)

	impl, ok := translator.(*audioSpeechOpenAIToOpenAITranslator)
	require.True(t, ok)
	require.Equal(t, "v1", impl.version)
	require.Equal(t, internalapi.ModelNameOverride("override-model"), impl.modelNameOverride)
}

func TestAudioSpeechOpenAIToOpenAITranslator_RequestBody(t *testing.T) {
	translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")

	rawBody := []byte("test-raw-body")
	req := &openai.AudioSpeechRequest{
		Model: "tts-1",
	}

	newHeaders, mutatedBody, err := translator.RequestBody(rawBody, req, false)
	require.NoError(t, err)
	require.Nil(t, newHeaders)
	require.NotNil(t, mutatedBody)
	require.Equal(t, rawBody, mutatedBody)
}

func TestAudioSpeechOpenAIToOpenAITranslator_ResponseHeaders(t *testing.T) {
	translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")

	headers := map[string]string{
		"content-type": "application/json",
	}

	newHeaders, err := translator.ResponseHeaders(headers)
	require.NoError(t, err)
	require.Nil(t, newHeaders)
}

func TestAudioSpeechOpenAIToOpenAITranslator_ResponseBody(t *testing.T) {
	translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")

	headers := map[string]string{}
	body := bytes.NewReader([]byte(`{"text":"transcribed text"}`))

	newHeaders, mutatedBody, tokenUsage, responseModel, err := translator.ResponseBody(headers, body, true, nil)
	require.NoError(t, err)
	require.Nil(t, newHeaders)
	require.Nil(t, mutatedBody)
	require.Equal(t, LLMTokenUsage{}, tokenUsage)
	require.Equal(t, internalapi.ResponseModel(""), responseModel)
}

func TestAudioSpeechOpenAIToOpenAITranslator_ResponseError(t *testing.T) {
	translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")

	headers := map[string]string{}
	body := bytes.NewReader([]byte(`{"error":{"message":"error message"}}`))

	newHeaders, mutatedBody, err := translator.ResponseError(headers, body)
	require.NoError(t, err)
	require.Nil(t, newHeaders)
	require.Nil(t, mutatedBody)
}
