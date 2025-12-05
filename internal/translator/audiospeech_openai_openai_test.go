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
)

func TestOpenAIToOpenAITranslatorV1AudioSpeech_RequestBody(t *testing.T) {
	t.Run("without model override", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")
		req := &openai.AudioSpeechRequest{
			Model: "tts-1",
			Input: "Hello world",
			Voice: "alloy",
		}
		body := []byte(`{"model":"tts-1","input":"Hello world","voice":"alloy"}`)

		headers, newBody, err := translator.RequestBody(body, req, false)
		require.NoError(t, err)
		require.Nil(t, newBody)
		require.Len(t, headers, 1)
		require.Equal(t, ":path", headers[0].Key())
		require.Equal(t, "/v1/audio/speech", headers[0].Value())
	})

	t.Run("with model override", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "tts-1-hd")
		req := &openai.AudioSpeechRequest{
			Model: "tts-1",
			Input: "Hello world",
			Voice: "alloy",
		}
		body := []byte(`{"model":"tts-1","input":"Hello world","voice":"alloy"}`)

		headers, newBody, err := translator.RequestBody(body, req, false)
		require.NoError(t, err)
		require.NotNil(t, newBody)
		require.Contains(t, string(newBody), `"model":"tts-1-hd"`)
		require.Len(t, headers, 2) // path + content-length
		require.Equal(t, ":path", headers[0].Key())
		require.Equal(t, "/v1/audio/speech", headers[0].Value())
	})

	t.Run("on retry without model override", func(t *testing.T) {
		translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")
		req := &openai.AudioSpeechRequest{
			Model: "tts-1",
			Input: "Hello world",
			Voice: "alloy",
		}
		body := []byte(`{"model":"tts-1","input":"Hello world","voice":"alloy"}`)

		headers, newBody, err := translator.RequestBody(body, req, true)
		require.NoError(t, err)
		require.Equal(t, body, newBody) // On retry, body is returned as-is
		require.Len(t, headers, 2)      // path + content-length
		require.Equal(t, ":path", headers[0].Key())
		require.Equal(t, "/v1/audio/speech", headers[0].Value())
		require.Equal(t, "content-length", headers[1].Key())
	})
}

func TestOpenAIToOpenAITranslatorV1AudioSpeech_ResponseHeaders(t *testing.T) {
	translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")
	headers, err := translator.ResponseHeaders(map[string]string{
		"content-type": "audio/mpeg",
	})
	require.NoError(t, err)
	require.Nil(t, headers) // No header transformation for audio speech
}

func TestOpenAIToOpenAITranslatorV1AudioSpeech_ResponseBody(t *testing.T) {
	translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")

	// Audio speech returns binary data, so we don't parse it
	audioData := []byte{0x49, 0x44, 0x33} // Fake audio data
	reader := bytes.NewReader(audioData)

	headers, newBody, tokenUsage, responseModel, err := translator.ResponseBody(
		map[string]string{"content-type": "audio/mpeg"},
		reader,
		true, // end of stream
		nil,  // no tracing span
	)
	require.NoError(t, err)
	require.Nil(t, headers)
	require.Nil(t, newBody)
	require.Empty(t, responseModel)

	// Token usage should be empty for audio speech
	in, hasIn := tokenUsage.InputTokens()
	out, hasOut := tokenUsage.OutputTokens()
	require.False(t, hasIn)
	require.False(t, hasOut)
	require.Equal(t, uint32(0), in)
	require.Equal(t, uint32(0), out)
}

func TestOpenAIToOpenAITranslatorV1AudioSpeech_ResponseError(t *testing.T) {
	translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")

	t.Run("json error response", func(t *testing.T) {
		errorBody := bytes.NewReader([]byte(`{"error":{"message":"Invalid voice"}}`))
		headers, newBody, err := translator.ResponseError(
			map[string]string{":status": "400"},
			errorBody,
		)
		require.NoError(t, err)
		require.Nil(t, headers) // Valid JSON, no transformation
		require.Nil(t, newBody)
	})

	t.Run("plain text error response", func(t *testing.T) {
		errorBody := bytes.NewReader([]byte("Internal server error"))
		headers, newBody, err := translator.ResponseError(
			map[string]string{":status": "500"},
			errorBody,
		)
		require.NoError(t, err)
		require.NotNil(t, headers)
		require.NotNil(t, newBody)
		require.Contains(t, string(newBody), "Internal server error")
		require.Contains(t, string(newBody), "OpenAIBackendError")
	})
}
