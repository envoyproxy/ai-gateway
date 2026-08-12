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
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestOpenAIToMiniMaxSpeechTranslator_RequestBody(t *testing.T) {
	format, speed, stream := "mp3", 1.25, "sse"
	req := &openai.SpeechRequest{
		Model: "speech-2.8-hd", Input: "Hello", Voice: "English_Graceful_Lady",
		ResponseFormat: &format, Speed: &speed, StreamFormat: &stream,
	}
	translator := NewSpeechOpenAIToMiniMaxTranslator("")
	headers, body, err := translator.RequestBody(nil, req, false)
	require.NoError(t, err)
	require.Equal(t, miniMaxSpeechPath, headers[0].Value())

	var actual miniMaxSpeechRequest
	require.NoError(t, json.Unmarshal(body, &actual))
	require.Equal(t, "speech-2.8-hd", actual.Model)
	require.Equal(t, "Hello", actual.Text)
	require.True(t, actual.Stream)
	require.Equal(t, "hex", actual.OutputFormat)
	require.Equal(t, "English_Graceful_Lady", actual.VoiceSetting.VoiceID)
	require.Equal(t, 1.25, actual.VoiceSetting.Speed)
	require.Equal(t, "mp3", actual.AudioSetting.Format)
}

func TestOpenAIToMiniMaxSpeechTranslator_Models(t *testing.T) {
	for model := range miniMaxSpeechModels {
		t.Run(model, func(t *testing.T) {
			translator := NewSpeechOpenAIToMiniMaxTranslator("")
			_, _, err := translator.RequestBody(nil, &openai.SpeechRequest{
				Model: model, Input: "Hello", Voice: "voice-id",
			}, false)
			require.NoError(t, err)
		})
	}
}

func TestOpenAIToMiniMaxSpeechTranslator_RejectsUnsupportedValues(t *testing.T) {
	translator := NewSpeechOpenAIToMiniMaxTranslator("")
	_, _, err := translator.RequestBody(nil, &openai.SpeechRequest{
		Model: "unknown", Input: "Hello", Voice: "voice-id",
	}, false)
	require.ErrorContains(t, err, "unsupported MiniMax speech model")

	format := "opus"
	_, _, err = translator.RequestBody(nil, &openai.SpeechRequest{
		Model: "speech-2.8-hd", Input: "Hello", Voice: "voice-id", ResponseFormat: &format,
	}, false)
	require.ErrorContains(t, err, "unsupported MiniMax audio format")

	format, stream := "wav", "sse"
	_, _, err = translator.RequestBody(nil, &openai.SpeechRequest{
		Model: "speech-2.8-hd", Input: "Hello", Voice: "voice-id", ResponseFormat: &format, StreamFormat: &stream,
	}, false)
	require.ErrorContains(t, err, "streaming speech only supports mp3")
}

func TestOpenAIToMiniMaxSpeechTranslator_ResponseBody(t *testing.T) {
	translator := NewSpeechOpenAIToMiniMaxTranslator("")
	_, _, err := translator.RequestBody(nil, &openai.SpeechRequest{
		Model: "speech-2.8-hd", Input: "Hello", Voice: "voice-id",
	}, false)
	require.NoError(t, err)

	response := `{"data":{"audio":"494433","status":2},"base_resp":{"status_code":0}}`
	_, body, _, model, err := translator.ResponseBody(nil, bytes.NewBufferString(response), true, nil)
	require.NoError(t, err)
	require.Equal(t, []byte("ID3"), body)
	require.Equal(t, "speech-2.8-hd", model)
}

func TestOpenAIToMiniMaxSpeechTranslator_ResponseErrors(t *testing.T) {
	translator := NewSpeechOpenAIToMiniMaxTranslator("")
	_, _, _, _, err := translator.ResponseBody(nil, bytes.NewBufferString(
		`{"base_resp":{"status_code":1001,"status_msg":"invalid request"}}`,
	), true, nil)
	require.ErrorContains(t, err, "invalid request")

	_, _, _, _, err = translator.ResponseBody(nil, bytes.NewBufferString(
		`{"data":{"audio":"494433","status":1},"base_resp":{"status_code":0}}`,
	), true, nil)
	require.ErrorContains(t, err, "response is incomplete")
}

func TestOpenAIToMiniMaxSpeechTranslator_StreamingResponse(t *testing.T) {
	stream := "sse"
	translator := NewSpeechOpenAIToMiniMaxTranslator("")
	_, _, err := translator.RequestBody(nil, &openai.SpeechRequest{
		Model: "speech-2.8-hd", Input: "Hello", Voice: "voice-id", StreamFormat: &stream,
	}, false)
	require.NoError(t, err)

	headers, err := translator.ResponseHeaders(nil)
	require.NoError(t, err)
	require.Equal(t, "text/event-stream", headers[0].Value())

	first := `data: {"data":{"audio":"4944","status":1},"base_resp":{"status_code":0}}` + "\n\n"
	_, body, _, model, err := translator.ResponseBody(nil, bytes.NewBufferString(first), false, nil)
	require.NoError(t, err)
	require.Equal(t, "data: {\"data\":\"SUQ=\"}\n\n", string(body))
	require.Equal(t, "speech-2.8-hd", model)

	last := `data: {"data":{"audio":"33","status":2},"base_resp":{"status_code":0}}` + "\n\n"
	_, body, _, _, err = translator.ResponseBody(nil, bytes.NewBufferString(last), true, nil)
	require.NoError(t, err)
	require.Equal(t, "data: {\"data\":\"Mw==\"}\n\ndata: [DONE]\n\n", string(body))
}
