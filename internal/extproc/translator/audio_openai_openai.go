// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"io"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

type AudioTranscriptionTranslator interface {
	RequestBody(rawBody []byte, body *openai.AudioTranscriptionRequest, onRetry bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error)
	ResponseHeaders(headers map[string]string) (*extprocv3.HeaderMutation, error)
	ResponseBody(headers map[string]string, body io.Reader, endOfStream bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, LLMTokenUsage, internalapi.ResponseModel, error)
	ResponseError(headers map[string]string, body io.Reader) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error)
}

type AudioSpeechTranslator interface {
	RequestBody(rawBody []byte, body *openai.AudioSpeechRequest, onRetry bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error)
	ResponseHeaders(headers map[string]string) (*extprocv3.HeaderMutation, error)
	ResponseBody(headers map[string]string, body io.Reader, endOfStream bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, LLMTokenUsage, internalapi.ResponseModel, error)
	ResponseError(headers map[string]string, body io.Reader) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error)
}

type audioTranscriptionOpenAIToOpenAITranslator struct {
	version           string
	modelNameOverride internalapi.ModelNameOverride
}

func NewAudioTranscriptionOpenAIToOpenAITranslator(version string, modelNameOverride internalapi.ModelNameOverride) AudioTranscriptionTranslator {
	return &audioTranscriptionOpenAIToOpenAITranslator{
		version:           version,
		modelNameOverride: modelNameOverride,
	}
}

func (a *audioTranscriptionOpenAIToOpenAITranslator) RequestBody(rawBody []byte, body *openai.AudioTranscriptionRequest, onRetry bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	return nil, &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{Body: rawBody},
	}, nil
}

func (a *audioTranscriptionOpenAIToOpenAITranslator) ResponseHeaders(headers map[string]string) (*extprocv3.HeaderMutation, error) {
	return nil, nil
}

func (a *audioTranscriptionOpenAIToOpenAITranslator) ResponseBody(headers map[string]string, body io.Reader, endOfStream bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, LLMTokenUsage, internalapi.ResponseModel, error) {
	return nil, nil, LLMTokenUsage{}, "", nil
}

func (a *audioTranscriptionOpenAIToOpenAITranslator) ResponseError(headers map[string]string, body io.Reader) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	return nil, nil, nil
}

type audioSpeechOpenAIToOpenAITranslator struct {
	version           string
	modelNameOverride internalapi.ModelNameOverride
}

func NewAudioSpeechOpenAIToOpenAITranslator(version string, modelNameOverride internalapi.ModelNameOverride) AudioSpeechTranslator {
	return &audioSpeechOpenAIToOpenAITranslator{
		version:           version,
		modelNameOverride: modelNameOverride,
	}
}

func (a *audioSpeechOpenAIToOpenAITranslator) RequestBody(rawBody []byte, body *openai.AudioSpeechRequest, onRetry bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	return nil, &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{Body: rawBody},
	}, nil
}

func (a *audioSpeechOpenAIToOpenAITranslator) ResponseHeaders(headers map[string]string) (*extprocv3.HeaderMutation, error) {
	return nil, nil
}

func (a *audioSpeechOpenAIToOpenAITranslator) ResponseBody(headers map[string]string, body io.Reader, endOfStream bool) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, LLMTokenUsage, internalapi.ResponseModel, error) {
	return nil, nil, LLMTokenUsage{}, "", nil
}

func (a *audioSpeechOpenAIToOpenAITranslator) ResponseError(headers map[string]string, body io.Reader) (*extprocv3.HeaderMutation, *extprocv3.BodyMutation, error) {
	return nil, nil, nil
}

