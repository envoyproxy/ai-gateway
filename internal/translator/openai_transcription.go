// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
	"io"
	"path"
	"strconv"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// NewTranscriptionOpenAIToOpenAITranslator implements [OpenAIAudioTranscriptionTranslator]
// for OpenAI to OpenAI translation for audio transcription.
func NewTranscriptionOpenAIToOpenAITranslator(prefix string, modelNameOverride internalapi.ModelNameOverride) OpenAIAudioTranscriptionTranslator {
	return &openAIToOpenAITranslatorV1Transcription{
		modelNameOverride: modelNameOverride,
		path:              path.Join("/", prefix, "audio", "transcriptions"),
	}
}

type openAIToOpenAITranslatorV1Transcription struct {
	modelNameOverride internalapi.ModelNameOverride
	path              string
	requestModel      internalapi.RequestModel
	contentType       string
}

// RequestBody implements [OpenAIAudioTranscriptionTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1Transcription) RequestBody(original []byte, req *openai.TranscriptionRequest, forceBodyMutation bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	o.requestModel = req.Model
	// Store content-type from request for potential multipart rewrite.
	// The content-type is propagated via the request headers, not here.

	if o.modelNameOverride != "" && o.contentType != "" {
		var newContentType string
		var rewriteErr error
		newBody, newContentType, rewriteErr = rewriteMultipartModel(original, o.contentType, o.modelNameOverride)
		if rewriteErr != nil {
			return nil, nil, fmt.Errorf("failed to rewrite multipart model: %w", rewriteErr)
		}
		newHeaders = append(newHeaders,
			internalapi.Header{contentTypeHeaderName, newContentType},
			internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))},
		)
		o.requestModel = o.modelNameOverride
	}

	newHeaders = append(newHeaders, internalapi.Header{pathHeaderName, o.path})

	if forceBodyMutation && len(newBody) == 0 {
		newBody = original
	}

	if len(newBody) > 0 && o.modelNameOverride == "" {
		newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	}
	return
}

// ResponseHeaders implements [OpenAIAudioTranscriptionTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1Transcription) ResponseHeaders(_ map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [OpenAIAudioTranscriptionTranslator.ResponseBody].
func (o *openAIToOpenAITranslatorV1Transcription) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracingapi.TranscriptionSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	responseModel = o.requestModel
	if span != nil {
		data, readErr := io.ReadAll(body)
		if readErr == nil {
			var resp openai.TranscriptionResponse
			if jsonErr := json.Unmarshal(data, &resp); jsonErr == nil {
				span.RecordResponse(&resp)
			}
		}
	}
	return
}

// ResponseError implements [OpenAIAudioTranscriptionTranslator.ResponseError].
func (o *openAIToOpenAITranslatorV1Transcription) ResponseError(respHeaders map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	return convertErrorOpenAIToOpenAIError(respHeaders, body)
}

// SetContentType sets the content-type from the original request for multipart parsing during model rewrite.
func (o *openAIToOpenAITranslatorV1Transcription) SetContentType(ct string) {
	o.contentType = ct
}
