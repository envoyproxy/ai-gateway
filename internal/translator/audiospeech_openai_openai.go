// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"io"
	"path"
	"strconv"

	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// NewAudioSpeechOpenAIToOpenAITranslator implements [Factory] for OpenAI to OpenAI translation for audio speech (TTS).
func NewAudioSpeechOpenAIToOpenAITranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) OpenAIAudioSpeechTranslator {
	return &openAIToOpenAITranslatorV1AudioSpeech{modelNameOverride: modelNameOverride, path: path.Join("/", apiVersion, "audio/speech")}
}

// openAIToOpenAITranslatorV1AudioSpeech is a passthrough translator for OpenAI Audio Speech (TTS) API.
// May apply model overrides but otherwise preserves the OpenAI format:
// https://platform.openai.com/docs/api-reference/audio/createSpeech
type openAIToOpenAITranslatorV1AudioSpeech struct {
	modelNameOverride internalapi.ModelNameOverride
	// The path of the audio speech endpoint to be used for the request.
	path string
}

// RequestBody implements [OpenAIAudioSpeechTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1AudioSpeech) RequestBody(original []byte, _ *openai.AudioSpeechRequest, onRetry bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	if o.modelNameOverride != "" {
		// If modelName is set we override the model to be used for the request.
		newBody, err = sjson.SetBytesOptions(original, "model", o.modelNameOverride, sjsonOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set model name: %w", err)
		}
	}

	// Always set the path header to the audio speech endpoint so that the request is routed correctly.
	if onRetry && len(newBody) == 0 {
		newBody = original
	}
	newHeaders = []internalapi.Header{{pathHeaderName, o.path}}

	if len(newBody) > 0 {
		newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	}
	return
}

// ResponseHeaders implements [OpenAIAudioSpeechTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1AudioSpeech) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [OpenAIAudioSpeechTranslator.ResponseBody].
// Audio speech returns binary audio data, so we just pass it through.
// There's no token usage for audio speech endpoints.
// TODO: Implement tracing for audio speech when needed.
func (o *openAIToOpenAITranslatorV1AudioSpeech) ResponseBody(_ map[string]string, _ io.Reader, _ bool, _ tracing.AudioSpeechSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	// Audio speech returns binary audio data, no transformation needed.
	// We don't parse the body since it's audio data, not JSON.
	// Token usage is not applicable for audio speech.
	// Pass nil span since tracing is not implemented yet.
	// TODO: Add tracing support when AudioSpeechSpan is implemented.
	return
}

// ResponseError implements [Translator.ResponseError]
// For OpenAI based backend we return the OpenAI error type as is.
// If connection fails the error body is translated to OpenAI error type for events such as HTTP 503 or 504.
func (o *openAIToOpenAITranslatorV1AudioSpeech) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	// Read the upstream error body regardless of content-type.
	buf, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to read error body: %w", err)
	}
	// If upstream already returned JSON, preserve it as-is.
	if json.Valid(buf) {
		return nil, nil, nil
	}
	// Otherwise, wrap the plain-text (or non-JSON) error into OpenAI REST error schema.
	openaiError := openai.Error{
		Type: "error",
		Error: openai.ErrorType{
			Type:    openAIBackendError,
			Message: string(buf),
			Code:    &statusCode,
		},
	}
	newBody, err = json.Marshal(openaiError)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal error body: %w", err)
	}
	newHeaders = []internalapi.Header{
		{contentTypeHeaderName, jsonContentType},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}
