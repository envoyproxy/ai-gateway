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
// https://developers.openai.com/api/reference/resources/files/methods/create
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

// ResponseHeaders implements [OpenAICreateFileTranslator.ResponseHeaders].
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

// ResponseError implements [OpenAICreateFileTranslator.ResponseError].
func (o *openAIToOpenAITranslatorV1CreateFile) ResponseError(respHeaders map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	return convertErrorOpenAIToOpenAIError(respHeaders, body)
}

// NewRetrieveFileOpenAIToOpenAITranslator implements [OpenAIRetrieveFileTranslator] for OpenAI to OpenAI translation for File API.
func NewRetrieveFileOpenAIToOpenAITranslator(prefix string, modelNameOverride internalapi.ModelNameOverride) *openAIToOpenAITranslatorV1RetrieveFile {
	return &openAIToOpenAITranslatorV1RetrieveFile{
		modelNameOverride: modelNameOverride,
		path:              path.Join("/", prefix, "files"),
	}
}

// openAIToOpenAITranslatorV1RetrieveFile is a passthrough translator for OpenAI File API.
// May apply model overrides but otherwise preserves the OpenAI format:
// https://developers.openai.com/api/reference/resources/files/methods/retrieve
type openAIToOpenAITranslatorV1RetrieveFile struct {
	modelNameOverride internalapi.ModelNameOverride
	// The path of the file endpoint to be used for the request. It is prefixed with the OpenAI path prefix.
	path string
	// requestModel serves as fallback for non-compliant OpenAI backends that
	// don't return model in responses, ensuring metrics/tracing always have a model.
	// requestModel internalapi.RequestModel
}

// RequestBody implements [OpenAIRetrieveFileTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1RetrieveFile) RequestBody(original []byte, _ *struct{}, forceBodyMutation bool) (
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

// ResponseHeaders implements [OpenAIRetrieveFileTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1RetrieveFile) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	// For OpenAI to OpenAI translation, we don't need to mutate the response headers.
	return nil, nil
}

// ResponseBody implements [OpenAIRetrieveFileTranslator.ResponseBody].
func (o *openAIToOpenAITranslatorV1RetrieveFile) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ tracingapi.RetrieveFileSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	var resp openai.FileObject
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to unmarshal body: %w", err)
	}
	return
}

// ResponseError implements [OpenAIRetrieveFileTranslator.ResponseError].
func (o *openAIToOpenAITranslatorV1RetrieveFile) ResponseError(respHeaders map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	return convertErrorOpenAIToOpenAIError(respHeaders, body)
}

// NewRetrieveFileContentOpenAIToOpenAITranslator implements [OpenAIRetrieveFileContentTranslator] for OpenAI to OpenAI translation for File API.
func NewRetrieveFileContentOpenAIToOpenAITranslator(prefix string, modelNameOverride internalapi.ModelNameOverride) *openAIToOpenAITranslatorV1RetrieveFileContent {
	return &openAIToOpenAITranslatorV1RetrieveFileContent{
		modelNameOverride: modelNameOverride,
		path:              path.Join("/", prefix, "files"),
	}
}

// openAIToOpenAITranslatorV1RetrieveFile is a passthrough translator for OpenAI File API.
// May apply model overrides but otherwise preserves the OpenAI format:
// https://developers.openai.com/api/reference/resources/files/methods/content
type openAIToOpenAITranslatorV1RetrieveFileContent struct {
	modelNameOverride internalapi.ModelNameOverride
	// The path of the file endpoint to be used for the request. It is prefixed with the OpenAI path prefix.
	path string
	// requestModel serves as fallback for non-compliant OpenAI backends that
	// don't return model in responses, ensuring metrics/tracing always have a model.
	// requestModel internalapi.RequestModel
}

// RequestBody implements [OpenAIRetrieveFileContentTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1RetrieveFileContent) RequestBody(original []byte, _ *struct{}, forceBodyMutation bool) (
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

// ResponseHeaders implements [OpenAIRetrieveFileContentTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1RetrieveFileContent) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	// For OpenAI to OpenAI translation, we don't need to mutate the response headers.
	return nil, nil
}

// ResponseBody implements [OpenAIRetrieveFileContentTranslator.ResponseBody].
func (o *openAIToOpenAITranslatorV1RetrieveFileContent) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ tracingapi.RetrieveFileContentSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	return
}

// ResponseError implements [OpenAIRetrieveFileContentTranslator.ResponseError].
func (o *openAIToOpenAITranslatorV1RetrieveFileContent) ResponseError(respHeaders map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	return convertErrorOpenAIToOpenAIError(respHeaders, body)
}

// NewDeleteFileOpenAIToOpenAITranslator implements [OpenAIDeleteFileTranslator] for OpenAI to OpenAI translation for File API.
func NewDeleteFileOpenAIToOpenAITranslator(prefix string, modelNameOverride internalapi.ModelNameOverride) *openAIToOpenAITranslatorV1DeleteFile {
	return &openAIToOpenAITranslatorV1DeleteFile{
		modelNameOverride: modelNameOverride,
		path:              path.Join("/", prefix, "files"),
	}
}

// openAIToOpenAITranslatorV1DeleteFile is a passthrough translator for OpenAI File API.
// May apply model overrides but otherwise preserves the OpenAI format:
// https://developers.openai.com/api/reference/resources/files/methods/delete
type openAIToOpenAITranslatorV1DeleteFile struct {
	modelNameOverride internalapi.ModelNameOverride
	// The path of the file endpoint to be used for the request. It is prefixed with the OpenAI path prefix.
	path string
	// requestModel serves as fallback for non-compliant OpenAI backends that
	// don't return model in responses, ensuring metrics/tracing always have a model.
	// requestModel internalapi.RequestModel
}

// RequestBody implements [OpenAIDeleteFileTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1DeleteFile) RequestBody(original []byte, _ *struct{}, forceBodyMutation bool) (
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

// ResponseHeaders implements [OpenAIDeleteFileTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1DeleteFile) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	// For OpenAI to OpenAI translation, we don't need to mutate the response headers.
	return nil, nil
}

// ResponseBody implements [OpenAIDeleteFileTranslator.ResponseBody].
func (o *openAIToOpenAITranslatorV1DeleteFile) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ tracingapi.DeleteFileSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	var resp openai.FileDeleted
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to unmarshal body: %w", err)
	}
	return
}

// ResponseError implements [OpenAIDeleteFileTranslator.ResponseError].
func (o *openAIToOpenAITranslatorV1DeleteFile) ResponseError(respHeaders map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	return convertErrorOpenAIToOpenAIError(respHeaders, body)
}
