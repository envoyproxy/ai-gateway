// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"io"
	"strconv"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

const miniMaxSpeechPath = "/v1/t2a_v2"

var miniMaxSpeechModels = map[string]struct{}{
	"speech-2.8-hd":    {},
	"speech-2.8-turbo": {},
	"speech-2.6-hd":    {},
	"speech-2.6-turbo": {},
	"speech-02-hd":     {},
	"speech-02-turbo":  {},
	"speech-01-hd":     {},
	"speech-01-turbo":  {},
}

type miniMaxSpeechRequest struct {
	Model         string                    `json:"model"`
	Text          string                    `json:"text"`
	Stream        bool                      `json:"stream"`
	OutputFormat  string                    `json:"output_format"`
	VoiceSetting  miniMaxVoiceSetting       `json:"voice_setting"`
	AudioSetting  miniMaxAudioSetting       `json:"audio_setting"`
	LanguageBoost string                    `json:"language_boost,omitempty"`
	Subtitle      bool                      `json:"subtitle_enable,omitempty"`
	Pronunciation map[string][]string       `json:"pronunciation_dict,omitempty"`
	VoiceModify   map[string]map[string]int `json:"voice_modify,omitempty"`
}

type miniMaxVoiceSetting struct {
	VoiceID string  `json:"voice_id"`
	Speed   float64 `json:"speed,omitempty"`
}

type miniMaxAudioSetting struct {
	Format string `json:"format"`
}

type miniMaxSpeechResponse struct {
	Data *struct {
		Audio  string `json:"audio"`
		Status int    `json:"status"`
	} `json:"data"`
	BaseResp struct {
		StatusCode int    `json:"status_code"`
		StatusMsg  string `json:"status_msg"`
	} `json:"base_resp"`
}

// NewSpeechOpenAIToMiniMaxTranslator creates a speech translator for the
// native MiniMax HTTP text-to-audio endpoint.
func NewSpeechOpenAIToMiniMaxTranslator(modelNameOverride internalapi.ModelNameOverride) OpenAISpeechTranslator {
	return &openAIToMiniMaxTranslatorV1Speech{modelNameOverride: modelNameOverride}
}

type openAIToMiniMaxTranslatorV1Speech struct {
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
	stream            bool
	streamComplete    bool
	buffered          []byte
}

func (m *openAIToMiniMaxTranslatorV1Speech) RequestBody(_ []byte, req *openai.SpeechRequest, _ bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	model := req.Model
	if m.modelNameOverride != "" {
		model = m.modelNameOverride
	}
	if _, ok := miniMaxSpeechModels[model]; !ok {
		return nil, nil, fmt.Errorf("unsupported MiniMax speech model %q", model)
	}

	format := openai.AudioFormatMP3
	if req.ResponseFormat != nil {
		format = *req.ResponseFormat
	}
	switch format {
	case openai.AudioFormatMP3, openai.AudioFormatWAV, openai.AudioFormatFLAC, openai.AudioFormatPCM:
	default:
		return nil, nil, fmt.Errorf("unsupported MiniMax audio format %q", format)
	}

	request := miniMaxSpeechRequest{
		Model:        model,
		Text:         req.Input,
		OutputFormat: "hex",
		VoiceSetting: miniMaxVoiceSetting{VoiceID: req.Voice},
		AudioSetting: miniMaxAudioSetting{Format: format},
	}
	m.stream = req.StreamFormat != nil && *req.StreamFormat == openai.StreamFormatSSE
	m.streamComplete = false
	m.buffered = nil
	if m.stream && format != openai.AudioFormatMP3 {
		return nil, nil, fmt.Errorf("MiniMax streaming speech only supports mp3 audio")
	}
	if req.Speed != nil {
		request.VoiceSetting.Speed = *req.Speed
	}
	request.Stream = m.stream

	newBody, err = json.Marshal(request)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal MiniMax speech request: %w", err)
	}
	m.requestModel = model
	return []internalapi.Header{
		{pathHeaderName, miniMaxSpeechPath},
		{contentTypeHeaderName, jsonContentType},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}, newBody, nil
}

func (m *openAIToMiniMaxTranslatorV1Speech) ResponseHeaders(map[string]string) ([]internalapi.Header, error) {
	if m.stream {
		return []internalapi.Header{{contentTypeHeaderName, "text/event-stream"}}, nil
	}
	return []internalapi.Header{{contentTypeHeaderName, "application/octet-stream"}}, nil
}

func (m *openAIToMiniMaxTranslatorV1Speech) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool, span tracingapi.SpeechSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	if m.stream {
		return m.handleStreamingResponse(body, endOfStream, span)
	}

	var response miniMaxSpeechResponse
	if err = json.NewDecoder(body).Decode(&response); err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to decode MiniMax speech response: %w", err)
	}
	if response.BaseResp.StatusCode != 0 {
		return nil, nil, tokenUsage, "", fmt.Errorf("MiniMax speech request failed: %s", response.BaseResp.StatusMsg)
	}
	if response.Data == nil || response.Data.Status != 2 {
		status := 0
		if response.Data != nil {
			status = response.Data.Status
		}
		return nil, nil, tokenUsage, "", fmt.Errorf("MiniMax speech response is incomplete: status=%d", status)
	}
	newBody, err = hex.DecodeString(response.Data.Audio)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to decode MiniMax speech audio: %w", err)
	}
	if span != nil {
		span.RecordResponse(&newBody)
	}
	return nil, newBody, tokenUsage, m.requestModel, nil
}

func (m *openAIToMiniMaxTranslatorV1Speech) handleStreamingResponse(body io.Reader, endOfStream bool, span tracingapi.SpeechSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	chunks, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to read MiniMax speech stream: %w", err)
	}
	m.buffered = append(m.buffered, chunks...)

	for {
		i := bytes.Index(m.buffered, []byte("\n\n"))
		if i == -1 {
			break
		}
		event := m.buffered[:i]
		m.buffered = m.buffered[i+2:]
		translated, translateErr := m.translateStreamingEvent(event, span)
		if translateErr != nil {
			return nil, nil, tokenUsage, "", translateErr
		}
		newBody = append(newBody, translated...)
	}

	if endOfStream {
		if event := bytes.TrimSpace(m.buffered); len(event) > 0 {
			translated, translateErr := m.translateStreamingEvent(event, span)
			if translateErr != nil {
				return nil, nil, tokenUsage, "", translateErr
			}
			newBody = append(newBody, translated...)
		}
		m.buffered = nil
		if !m.streamComplete {
			return nil, nil, tokenUsage, "", fmt.Errorf("MiniMax speech stream ended before completion")
		}
		newBody = append(newBody, "data: [DONE]\n\n"...)
	}
	return nil, newBody, tokenUsage, m.requestModel, nil
}

func (m *openAIToMiniMaxTranslatorV1Speech) translateStreamingEvent(event []byte, span tracingapi.SpeechSpan) ([]byte, error) {
	var translated []byte
	for line := range bytes.SplitSeq(event, []byte("\n")) {
		data, ok := cutSSEDataPrefix(line)
		if !ok || len(data) == 0 || bytes.Equal(data, sseDoneMessage) {
			continue
		}
		var response miniMaxSpeechResponse
		if err := json.Unmarshal(data, &response); err != nil {
			return nil, fmt.Errorf("failed to decode MiniMax speech stream event: %w", err)
		}
		if response.BaseResp.StatusCode != 0 {
			return nil, fmt.Errorf("MiniMax speech request failed: %s", response.BaseResp.StatusMsg)
		}
		if response.Data == nil {
			continue
		}
		if response.Data.Status == 2 {
			m.streamComplete = true
		}
		if response.Data.Audio == "" {
			continue
		}
		audio, err := hex.DecodeString(response.Data.Audio)
		if err != nil {
			return nil, fmt.Errorf("failed to decode MiniMax speech audio: %w", err)
		}
		chunk := &openai.SpeechStreamChunk{Data: audio}
		if span != nil {
			span.RecordResponseChunk(chunk)
		}
		payload, err := json.Marshal(chunk)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal OpenAI speech stream event: %w", err)
		}
		translated = append(translated, "data: "...)
		translated = append(translated, payload...)
		translated = append(translated, '\n', '\n')
	}
	return translated, nil
}

func (m *openAIToMiniMaxTranslatorV1Speech) ResponseError(_ map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	contents, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read MiniMax speech error response: %w", err)
	}
	return nil, contents, nil
}
