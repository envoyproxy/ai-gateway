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

// NewCreateFileOpenAIToOpenAITranslator implements [OpenAICreateFileTranslator] for OpenAI to OpenAI translation for File API.
func NewCreateFileOpenAIToOpenAITranslator(prefix string, modelNameOverride internalapi.ModelNameOverride) OpenAICreateFileTranslator {
	return &openAIToOpenAITranslatorV1CreateFile{
		modelNameOverride: modelNameOverride,
		path:              path.Join("/", prefix, "files"),
	}
}

// openAIToOpenAITranslatorV1CreateFile is a passthrough translator for OpenAI File API.
// May apply model overrides but otherwise preserves the OpenAI format:
// https://platform.openai.com/docs/api-reference/files/create
type openAIToOpenAITranslatorV1CreateFile struct {
	modelNameOverride internalapi.ModelNameOverride
	// The path of the file endpoint to be used for the request. It is prefixed with the OpenAI path prefix.
	path string
	// requestModel serves as fallback for non-compliant OpenAI backends that
	// don't return model in responses, ensuring metrics/tracing always have a model.
	// requestModel internalapi.RequestModel
}

// RequestBody implements [OpenAICreateFileTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1CreateFile) RequestBody(original []byte, _ *openai.FileNewParams, forceBodyMutation bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	// Always set the path header to the files endpoint so that the request is routed correctly.
	newHeaders = []internalapi.Header{{pathHeaderName, o.path}}

	if forceBodyMutation && len(newBody) == 0 {
		newBody = original
	}

	if len(newBody) > 0 {
		newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	}
	return
}

// ResponseHeaders implements [OpenAIResponsesTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1CreateFile) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	// For OpenAI to OpenAI translation, we don't need to mutate the response headers.
	return nil, nil
}

// ResponseBody implements [OpenAICreateFileTranslator.ResponseBody].
func (o *openAIToOpenAITranslatorV1CreateFile) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ tracingapi.CreateFileSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	var resp openai.FileObject
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to unmarshal body: %w", err)
	}
	return
}

// ResponseError implements [OpenAIResponsesTranslator.ResponseError].
func (o *openAIToOpenAITranslatorV1CreateFile) ResponseError(respHeaders map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	return convertErrorOpenAIToOpenAIError(respHeaders, body)
}
