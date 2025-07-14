// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"io"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// NewChatCompletionOpenAIToGCPAnthropicTranslator implements [Factory] for OpenAI to GCP Anthropic translation.
// This translator converts OpenAI ChatCompletion API requests to GCP Anthropic API format.
func NewChatCompletionOpenAIToGCPAnthropicTranslator(modelNameOverride string) OpenAIChatCompletionTranslator {
	return &openAIToGCPAnthropicTranslatorV1ChatCompletion{modelNameOverride: modelNameOverride}
}

type openAIToGCPAnthropicTranslatorV1ChatCompletion struct {
	modelNameOverride string
}

// RequestBody implements [Translator.RequestBody] for GCP Anthropic.
// This method translates an OpenAI ChatCompletion request to a GCP Anthropic API request.
func (o *openAIToGCPAnthropicTranslatorV1ChatCompletion) RequestBody(_ []byte, openAIReq *openai.ChatCompletionRequest, onRetry bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	_ = onRetry
	modelName := openAIReq.Model
	if o.modelNameOverride != "" {
		// Use modelName override if set.
		modelName = o.modelNameOverride
	}
	pathSuffix := buildGCPModelPathSuffix(GCPModelPublisherAnthropic, modelName, GCPMethodGenerateContent)

	// TODO: Implement actual translation from OpenAI to Anthropic request.

	headerMutation, bodyMutation = buildGCPRequestMutations(pathSuffix, nil)
	return headerMutation, bodyMutation, nil
}

// ResponseHeaders implements [Translator.ResponseHeaders].
func (o *openAIToGCPAnthropicTranslatorV1ChatCompletion) ResponseHeaders(headers map[string]string) (
	headerMutation *extprocv3.HeaderMutation, err error,
) {
	// TODO: Implement header transformations if needed.
	_ = headers
	return nil, nil
}

// ResponseError implements [Translator.ResponseError].
// This method translates GCP Anthropic API errors to OpenAI-compatible error formats.
func (o *openAIToGCPAnthropicTranslatorV1ChatCompletion) ResponseError(respHeaders map[string]string, body interface{}) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	// TODO: Implement error translation.
	_, _ = respHeaders, body
	return nil, nil, nil
}

func (o *openAIToGCPAnthropicTranslatorV1ChatCompletion) ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, err error,
) {
	// TODO: Implement response translation from Anthropic to OpenAI format.
	_, _, _ = respHeaders, body, endOfStream
	return nil, nil, LLMTokenUsage{}, nil
}
