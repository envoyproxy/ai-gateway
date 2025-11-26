// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"io"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

type AudioSpeechTranslator = Translator[openai.AudioSpeechRequest, any]

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

func (a *audioSpeechOpenAIToOpenAITranslator) RequestBody(rawBody []byte, _ *openai.AudioSpeechRequest, _ bool) (
	newHeaders []internalapi.Header,
	mutatedBody []byte,
	err error,
) {
	return nil, rawBody, nil
}

func (a *audioSpeechOpenAIToOpenAITranslator) ResponseHeaders(_ map[string]string) (
	newHeaders []internalapi.Header,
	err error,
) {
	return nil, nil
}

func (a *audioSpeechOpenAIToOpenAITranslator) ResponseBody(_ map[string]string, _ io.Reader, _ bool, _ any) (
	newHeaders []internalapi.Header,
	mutatedBody []byte,
	tokenUsage LLMTokenUsage,
	responseModel internalapi.ResponseModel,
	err error,
) {
	return nil, nil, LLMTokenUsage{}, "", nil
}

func (a *audioSpeechOpenAIToOpenAITranslator) ResponseError(_ map[string]string, _ io.Reader) (
	newHeaders []internalapi.Header,
	mutatedBody []byte,
	err error,
) {
	return nil, nil, nil
}
