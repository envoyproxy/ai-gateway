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

func TestTranscriptionTranslator_RequestBody_NoOverride(t *testing.T) {
	tr := NewTranscriptionOpenAIToOpenAITranslator("v1", "")
	req := &openai.TranscriptionRequest{Model: "whisper-1", FileName: "test.mp3", FileSize: 1024}
	original := []byte("multipart-body-data")

	hm, bm, err := tr.RequestBody(original, req, false)
	require.NoError(t, err)
	require.Len(t, hm, 1)
	require.Equal(t, pathHeaderName, hm[0].Key())
	require.Equal(t, "/v1/audio/transcriptions", hm[0].Value())
	require.Nil(t, bm)
}

func TestTranscriptionTranslator_RequestBody_ForceMutation(t *testing.T) {
	tr := NewTranscriptionOpenAIToOpenAITranslator("v1", "")
	req := &openai.TranscriptionRequest{Model: "whisper-1"}
	original := []byte("multipart-body-data")

	hm, bm, err := tr.RequestBody(original, req, true)
	require.NoError(t, err)
	require.NotNil(t, bm)
	require.Equal(t, original, bm)
	foundCL := false
	for _, h := range hm {
		if h.Key() == contentLengthHeaderName {
			foundCL = true
		}
	}
	require.True(t, foundCL)
}

func TestTranscriptionTranslator_RequestBody_ModelOverride(t *testing.T) {
	tr := NewTranscriptionOpenAIToOpenAITranslator("v1", "whisper-large-v3")

	body, contentType := buildMultipartBody(t, map[string]string{"model": "whisper-1"}, "file", "test.mp3", []byte("audio"))
	// Set content-type on translator.
	tr.(*openAIToOpenAITranslatorV1Transcription).SetContentType(contentType)

	req := &openai.TranscriptionRequest{Model: "whisper-1", FileName: "test.mp3", FileSize: 5}

	hm, bm, err := tr.RequestBody(body, req, false)
	require.NoError(t, err)
	require.NotNil(t, bm)

	foundCT := false
	foundPath := false
	for _, h := range hm {
		if h.Key() == contentTypeHeaderName {
			foundCT = true
			require.Contains(t, h.Value(), "multipart/form-data")
		}
		if h.Key() == pathHeaderName {
			foundPath = true
		}
	}
	require.True(t, foundCT)
	require.True(t, foundPath)

	// Verify the rewritten body has the new model.
	var newCT string
	for _, h := range hm {
		if h.Key() == contentTypeHeaderName {
			newCT = h.Value()
		}
	}
	fields := parseMultipartFields(t, bm, newCT)
	require.Equal(t, "whisper-large-v3", fields["model"])
}

func TestTranscriptionTranslator_ResponseHeaders_NoOp(t *testing.T) {
	tr := NewTranscriptionOpenAIToOpenAITranslator("v1", "")
	hm, err := tr.ResponseHeaders(map[string]string{"foo": "bar"})
	require.NoError(t, err)
	require.Nil(t, hm)
}

func TestTranscriptionTranslator_ResponseBody_NoSpan(t *testing.T) {
	tr := NewTranscriptionOpenAIToOpenAITranslator("v1", "")
	req := &openai.TranscriptionRequest{Model: "whisper-1"}
	_, _, _ = tr.RequestBody([]byte("body"), req, false)

	resp := openai.TranscriptionResponse{Text: "hello world"}
	respBytes, _ := json.Marshal(resp)

	hm, bm, usage, model, err := tr.ResponseBody(nil, bytes.NewReader(respBytes), true, nil)
	require.NoError(t, err)
	require.Nil(t, hm)
	require.Nil(t, bm)
	require.Equal(t, tokenUsageFrom(-1, -1, -1, -1, -1, -1), usage)
	require.Equal(t, "whisper-1", model)
}

func TestTranscriptionTranslator_ResponseBody_WithSpan(t *testing.T) {
	mockSpan := &mockTranscriptionSpan{}
	tr := NewTranscriptionOpenAIToOpenAITranslator("v1", "")
	req := &openai.TranscriptionRequest{Model: "whisper-1"}
	_, _, _ = tr.RequestBody([]byte("body"), req, false)

	resp := openai.TranscriptionResponse{Text: "hello world", Language: "en", Duration: 5.5}
	respBytes, _ := json.Marshal(resp)

	_, _, _, _, err := tr.ResponseBody(nil, bytes.NewReader(respBytes), true, mockSpan)
	require.NoError(t, err)
	require.NotNil(t, mockSpan.recordedResponse)
	require.Equal(t, "hello world", mockSpan.recordedResponse.Text)
}

func TestTranscriptionTranslator_ResponseBody_WithSpan_NonJSON(t *testing.T) {
	mockSpan := &mockTranscriptionSpan{}
	tr := NewTranscriptionOpenAIToOpenAITranslator("v1", "")
	req := &openai.TranscriptionRequest{Model: "whisper-1"}
	_, _, _ = tr.RequestBody([]byte("body"), req, false)

	rawResponse := "hello world\nwith new line and \"quotes\""
	_, _, _, _, err := tr.ResponseBody(nil, bytes.NewReader([]byte(rawResponse)), true, mockSpan)
	require.NoError(t, err)
	require.NotNil(t, mockSpan.recordedResponse)
	require.Equal(t, rawResponse, mockSpan.recordedResponse.Text)
}

func TestTranscriptionTranslator_ResponseError(t *testing.T) {
	tr := NewTranscriptionOpenAIToOpenAITranslator("v1", "")
	headers := map[string]string{contentTypeHeaderName: "text/plain", statusHeaderName: "400"}
	hm, bm, err := tr.ResponseError(headers, bytes.NewReader([]byte("error")))
	require.NoError(t, err)
	require.NotNil(t, hm)
	require.NotNil(t, bm)
}

type mockTranscriptionSpan struct {
	recordedResponse *openai.TranscriptionResponse
}

func (m *mockTranscriptionSpan) RecordResponse(resp *openai.TranscriptionResponse) {
	m.recordedResponse = resp
}

func (m *mockTranscriptionSpan) RecordResponseChunk(*struct{}) {}
func (m *mockTranscriptionSpan) EndSpanOnError(int, []byte)    {}
func (m *mockTranscriptionSpan) EndSpan()                      {}
