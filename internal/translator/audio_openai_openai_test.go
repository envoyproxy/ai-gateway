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

func TestNewAudioTranscriptionOpenAIToOpenAITranslator(t *testing.T) {
	translator := NewAudioTranscriptionOpenAIToOpenAITranslator("v1", "override-model")
	require.NotNil(t, translator)

	impl, ok := translator.(*audioTranscriptionOpenAIToOpenAITranslator)
	require.True(t, ok)
	require.Equal(t, "v1", impl.version)
	require.Equal(t, internalapi.ModelNameOverride("override-model"), impl.modelNameOverride)
}

func TestAudioTranscriptionOpenAIToOpenAITranslator_RequestBody(t *testing.T) {
	translator := NewAudioTranscriptionOpenAIToOpenAITranslator("v1", "")
	
	rawBody := []byte("test-raw-body")
	req := &openai.AudioTranscriptionRequest{
		Model: "whisper-1",
	}

	headerMutation, bodyMutation, err := translator.RequestBody(rawBody, req, false)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.NotNil(t, bodyMutation)
	require.Equal(t, rawBody, bodyMutation.GetBody())
}

func TestAudioTranscriptionOpenAIToOpenAITranslator_ResponseHeaders(t *testing.T) {
	translator := NewAudioTranscriptionOpenAIToOpenAITranslator("v1", "")
	
	headers := map[string]string{
		"content-type": "application/json",
	}

	headerMutation, err := translator.ResponseHeaders(headers)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
}

func TestAudioTranscriptionOpenAIToOpenAITranslator_ResponseBody(t *testing.T) {
	translator := NewAudioTranscriptionOpenAIToOpenAITranslator("v1", "")
	
	headers := map[string]string{}
	body := bytes.NewReader([]byte(`{"text":"transcribed text"}`))

	headerMutation, bodyMutation, tokenUsage, responseModel, err := translator.ResponseBody(headers, body, true)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
	require.Equal(t, LLMTokenUsage{}, tokenUsage)
	require.Equal(t, internalapi.ResponseModel(""), responseModel)
}

func TestAudioTranscriptionOpenAIToOpenAITranslator_ResponseError(t *testing.T) {
	translator := NewAudioTranscriptionOpenAIToOpenAITranslator("v1", "")
	
	headers := map[string]string{}
	body := bytes.NewReader([]byte(`{"error":{"message":"error message"}}`))

	headerMutation, bodyMutation, err := translator.ResponseError(headers, body)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
}

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
	
	rawBody := []byte(`{"model":"tts-1","input":"test input","voice":"alloy"}`)
	req := &openai.AudioSpeechRequest{
		Model: "tts-1",
		Input: "test input",
		Voice: "alloy",
	}

	headerMutation, bodyMutation, err := translator.RequestBody(rawBody, req, false)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.NotNil(t, bodyMutation)
	require.Equal(t, rawBody, bodyMutation.GetBody())
}

func TestAudioSpeechOpenAIToOpenAITranslator_RequestBody_OnRetry(t *testing.T) {
	translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")
	
	rawBody := []byte(`{"model":"tts-1","input":"test input","voice":"alloy"}`)
	req := &openai.AudioSpeechRequest{
		Model: "tts-1",
		Input: "test input",
		Voice: "alloy",
	}

	headerMutation, bodyMutation, err := translator.RequestBody(rawBody, req, true)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.NotNil(t, bodyMutation)
	require.Equal(t, rawBody, bodyMutation.GetBody())
}

func TestAudioSpeechOpenAIToOpenAITranslator_ResponseHeaders(t *testing.T) {
	translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")
	
	headers := map[string]string{
		"content-type": "audio/mpeg",
	}

	headerMutation, err := translator.ResponseHeaders(headers)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
}

func TestAudioSpeechOpenAIToOpenAITranslator_ResponseBody(t *testing.T) {
	translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")
	
	headers := map[string]string{}
	body := bytes.NewReader([]byte("audio-binary-data"))

	headerMutation, bodyMutation, tokenUsage, responseModel, err := translator.ResponseBody(headers, body, true)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
	require.Equal(t, LLMTokenUsage{}, tokenUsage)
	require.Equal(t, internalapi.ResponseModel(""), responseModel)
}

func TestAudioSpeechOpenAIToOpenAITranslator_ResponseBody_NotEndOfStream(t *testing.T) {
	translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")
	
	headers := map[string]string{}
	body := bytes.NewReader([]byte("audio-binary-data"))

	headerMutation, bodyMutation, tokenUsage, responseModel, err := translator.ResponseBody(headers, body, false)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
	require.Equal(t, LLMTokenUsage{}, tokenUsage)
	require.Equal(t, internalapi.ResponseModel(""), responseModel)
}

func TestAudioSpeechOpenAIToOpenAITranslator_ResponseError(t *testing.T) {
	translator := NewAudioSpeechOpenAIToOpenAITranslator("v1", "")
	
	headers := map[string]string{}
	body := bytes.NewReader([]byte(`{"error":{"message":"error message"}}`))

	headerMutation, bodyMutation, err := translator.ResponseError(headers, body)
	require.NoError(t, err)
	require.Nil(t, headerMutation)
	require.Nil(t, bodyMutation)
}

