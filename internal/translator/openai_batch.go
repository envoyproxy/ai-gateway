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

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// encodeOutputAndErrorFileIDs encodes the output_file_id and error_file_id fields in a batch
// response body using the file prefix so that clients can later retrieve those files via the
// gateway without needing to know which backend to target.
// Fields that are absent or empty are left untouched.
func encodeOutputAndErrorFileIDs(bodyBytes []byte, modelName, backendName string) ([]byte, error) {
	if outputFileID := gjson.GetBytes(bodyBytes, "output_file_id").String(); outputFileID != "" {
		encoded := EncodeFileIDWithRouting(outputFileID, modelName, backendName, "file")
		var err error
		bodyBytes, err = sjson.SetBytes(bodyBytes, "output_file_id", encoded)
		if err != nil {
			return nil, fmt.Errorf("failed to encode output_file_id in response: %w", err)
		}
	}
	if errorFileID := gjson.GetBytes(bodyBytes, "error_file_id").String(); errorFileID != "" {
		encoded := EncodeFileIDWithRouting(errorFileID, modelName, backendName, "file")
		var err error
		bodyBytes, err = sjson.SetBytes(bodyBytes, "error_file_id", encoded)
		if err != nil {
			return nil, fmt.Errorf("failed to encode error_file_id in response: %w", err)
		}
	}
	return bodyBytes, nil
}

// NewCreateBatchOpenAIToOpenAITranslator implements [OpenAICreateBatchTranslator] for OpenAI to OpenAI
// translation for the Batch API (POST /v1/batches).
func NewCreateBatchOpenAIToOpenAITranslator(prefix string, modelNameOverride internalapi.ModelNameOverride) OpenAICreateBatchTranslator {
	return &openAIToOpenAITranslatorV1CreateBatch{
		modelNameOverride: modelNameOverride,
		path:              path.Join("/", prefix, "batches"),
	}
}

// openAIToOpenAITranslatorV1CreateBatch is a passthrough translator for OpenAI Batch API (create).
// https://platform.openai.com/docs/api-reference/batch/create
type openAIToOpenAITranslatorV1CreateBatch struct {
	modelNameOverride internalapi.ModelNameOverride
	// path is the batches endpoint path, prefixed with the OpenAI path prefix.
	path string
	// requestModel stores the model from the request for encoding the returned batch_id.
	requestModel internalapi.RequestModel
	// requestBackend stores the backend from the request for encoding the returned batch_id.
	requestBackend string
}

// RequestBody implements [OpenAICreateBatchTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1CreateBatch) RequestBody(reqHeaders map[string]string, original []byte, _ *openai.BatchNewParams, forceBodyMutation bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	o.requestModel = reqHeaders[internalapi.ModelNameHeaderKeyDefault]
	o.requestBackend = reqHeaders[internalapi.BackendNameHeaderKey]
	// Always set the path header to the batches endpoint so that the request is routed correctly.
	newHeaders = []internalapi.Header{{pathHeaderName, o.path}}

	if forceBodyMutation && len(newBody) == 0 {
		newBody = original
	}

	if len(newBody) > 0 {
		newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	}
	return
}

// ResponseHeaders implements [OpenAICreateBatchTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1CreateBatch) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [OpenAICreateBatchTranslator.ResponseBody].
// Encodes the batch_id, output_file_id and error_file_id in the response to enable sticky routing
// for subsequent batch and file retrieval operations.
func (o *openAIToOpenAITranslatorV1CreateBatch) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ tracingapi.CreateBatchSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to read response body: %w", err)
	}
	batchID := gjson.GetBytes(bodyBytes, "id").String()
	if batchID != "" {
		encodedID := EncodeFileIDWithRouting(batchID, o.requestModel, o.requestBackend, "batch")
		bodyBytes, err = sjson.SetBytes(bodyBytes, "id", encodedID)
		if err != nil {
			return nil, nil, tokenUsage, "", fmt.Errorf("failed to encode batch ID in response: %w", err)
		}
	}
	bodyBytes, err = encodeOutputAndErrorFileIDs(bodyBytes, o.requestModel, o.requestBackend)
	if err != nil {
		return nil, nil, tokenUsage, "", err
	}
	newBody = bodyBytes
	newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	return
}

// ResponseError implements [OpenAICreateBatchTranslator.ResponseError].
func (o *openAIToOpenAITranslatorV1CreateBatch) ResponseError(respHeaders map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	return convertErrorOpenAIToOpenAIError(respHeaders, body)
}

// NewListBatchesOpenAIToOpenAITranslator implements [OpenAIListBatchesTranslator] for OpenAI to OpenAI
// translation for the Batch API (GET /v1/batches).
func NewListBatchesOpenAIToOpenAITranslator(prefix string, modelNameOverride internalapi.ModelNameOverride) OpenAIListBatchesTranslator {
	return &openAIToOpenAITranslatorV1ListBatches{
		modelNameOverride: modelNameOverride,
		path:              path.Join("/", prefix, "batches"),
	}
}

// openAIToOpenAITranslatorV1ListBatches is a passthrough translator for OpenAI List Batches API.
// https://platform.openai.com/docs/api-reference/batch/list
type openAIToOpenAITranslatorV1ListBatches struct {
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
	requestBackend    string
	// path is the batches endpoint path.
	path string
}

// RequestBody implements [OpenAIListBatchesTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1ListBatches) RequestBody(reqHeaders map[string]string, original []byte, _ *struct{}, forceBodyMutation bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	o.requestModel = reqHeaders[internalapi.ModelNameHeaderKeyDefault]
	o.requestBackend = reqHeaders[internalapi.BackendNameHeaderKey]
	// Always set the path header to the batches endpoint so that the request is routed correctly.
	newHeaders = []internalapi.Header{{pathHeaderName, o.path}}

	if forceBodyMutation && len(newBody) == 0 {
		newBody = original
	}

	if len(newBody) > 0 {
		newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	}
	return
}

// ResponseHeaders implements [OpenAIListBatchesTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1ListBatches) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [OpenAIListBatchesTranslator.ResponseBody].
// Preserve upstream response as-is for list batches responses.
// No response body mutation is needed, similar to ListFiles endpoint.
func (o *openAIToOpenAITranslatorV1ListBatches) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ tracingapi.Span[struct{}, struct{}]) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	_ = body
	// Preserve upstream response as-is for list batches responses.
	// No response body mutation is needed.
	return
}

// ResponseError implements [OpenAIListBatchesTranslator.ResponseError].
func (o *openAIToOpenAITranslatorV1ListBatches) ResponseError(respHeaders map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	return convertErrorOpenAIToOpenAIError(respHeaders, body)
}

// NewRetrieveBatchOpenAIToOpenAITranslator implements [OpenAIRetrieveBatchTranslator] for OpenAI to OpenAI
// translation for the Batch API (GET /v1/batches/{batch_id}).
func NewRetrieveBatchOpenAIToOpenAITranslator(prefix string, modelNameOverride internalapi.ModelNameOverride) OpenAIRetrieveBatchTranslator {
	return &openAIToOpenAITranslatorV1RetrieveBatch{
		modelNameOverride: modelNameOverride,
		pathPrefix:        path.Join("/", prefix, "batches"),
	}
}

// openAIToOpenAITranslatorV1RetrieveBatch is a passthrough translator for OpenAI Get Batch API.
// https://platform.openai.com/docs/api-reference/batch/retrieve
type openAIToOpenAITranslatorV1RetrieveBatch struct {
	modelNameOverride internalapi.ModelNameOverride
	// pathPrefix is the batches endpoint path prefix without the batch_id.
	pathPrefix string
	// requestBatchID stores the original encoded batch_id to echo back in the response.
	requestBatchID string
	// requestModel and requestBackend are captured from request headers so that
	// output_file_id and error_file_id in the response can be encoded with routing info.
	requestModel   internalapi.RequestModel
	requestBackend string
}

// RequestBody implements [OpenAIRetrieveBatchTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1RetrieveBatch) RequestBody(reqHeaders map[string]string, original []byte, _ *struct{}, forceBodyMutation bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	o.requestBatchID = reqHeaders[internalapi.OriginalFileIDHeaderKey]
	o.requestModel = reqHeaders[internalapi.ModelNameHeaderKeyDefault]
	o.requestBackend = reqHeaders[internalapi.BackendNameHeaderKey]
	// Set path to {prefix}/batches/{decoded_batch_id}.
	newHeaders = []internalapi.Header{{pathHeaderName, path.Join(o.pathPrefix, reqHeaders[internalapi.DecodedFileIDHeaderKey])}}

	if forceBodyMutation && len(newBody) == 0 {
		newBody = original
	}

	if len(newBody) > 0 {
		newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	}
	return
}

// ResponseHeaders implements [OpenAIRetrieveBatchTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1RetrieveBatch) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [OpenAIRetrieveBatchTranslator.ResponseBody].
// Echoes the encoded batch_id back in the response and encodes output_file_id/error_file_id
// so clients can retrieve output and error files without knowing the backend.
func (o *openAIToOpenAITranslatorV1RetrieveBatch) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ tracingapi.RetrieveBatchSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to read response body: %w", err)
	}
	if o.requestBatchID != "" {
		bodyBytes, err = sjson.SetBytes(bodyBytes, "id", o.requestBatchID)
		if err != nil {
			return nil, nil, tokenUsage, "", fmt.Errorf("failed to set batch ID: %w", err)
		}
	}
	bodyBytes, err = encodeOutputAndErrorFileIDs(bodyBytes, o.requestModel, o.requestBackend)
	if err != nil {
		return nil, nil, tokenUsage, "", err
	}
	newBody = bodyBytes
	newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	return
}

// ResponseError implements [OpenAIRetrieveBatchTranslator.ResponseError].
func (o *openAIToOpenAITranslatorV1RetrieveBatch) ResponseError(respHeaders map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	return convertErrorOpenAIToOpenAIError(respHeaders, body)
}

// NewCancelBatchOpenAIToOpenAITranslator implements [OpenAICancelBatchTranslator] for OpenAI to OpenAI
// translation for the Batch API (POST /v1/batches/{batch_id}/cancel).
func NewCancelBatchOpenAIToOpenAITranslator(prefix string, modelNameOverride internalapi.ModelNameOverride) OpenAICancelBatchTranslator {
	return &openAIToOpenAITranslatorV1CancelBatch{
		modelNameOverride: modelNameOverride,
		pathPrefix:        path.Join("/", prefix, "batches"),
	}
}

// openAIToOpenAITranslatorV1CancelBatch is a passthrough translator for OpenAI Cancel Batch API.
// https://platform.openai.com/docs/api-reference/batch/cancel
type openAIToOpenAITranslatorV1CancelBatch struct {
	modelNameOverride internalapi.ModelNameOverride
	// pathPrefix is the batches endpoint path prefix without the batch_id.
	pathPrefix string
	// requestBatchID stores the original encoded batch_id to echo back in the response.
	requestBatchID string
	// requestModel and requestBackend are captured from request headers so that
	// output_file_id and error_file_id in the response can be encoded with routing info.
	requestModel   internalapi.RequestModel
	requestBackend string
}

// RequestBody implements [OpenAICancelBatchTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1CancelBatch) RequestBody(reqHeaders map[string]string, original []byte, _ *struct{}, forceBodyMutation bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	o.requestBatchID = reqHeaders[internalapi.OriginalFileIDHeaderKey]
	o.requestModel = reqHeaders[internalapi.ModelNameHeaderKeyDefault]
	o.requestBackend = reqHeaders[internalapi.BackendNameHeaderKey]
	// Set path to {prefix}/batches/{decoded_batch_id}/cancel.
	newHeaders = []internalapi.Header{{pathHeaderName, path.Join(o.pathPrefix, reqHeaders[internalapi.DecodedFileIDHeaderKey], "cancel")}}

	if forceBodyMutation && len(newBody) == 0 {
		newBody = original
	}

	if len(newBody) > 0 {
		newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	}
	return
}

// ResponseHeaders implements [OpenAICancelBatchTranslator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1CancelBatch) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [OpenAICancelBatchTranslator.ResponseBody].
// Echoes the encoded batch_id back in the response and encodes output_file_id/error_file_id
// so clients can retrieve output and error files without knowing the backend.
func (o *openAIToOpenAITranslatorV1CancelBatch) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ tracingapi.CancelBatchSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel internalapi.ResponseModel, err error,
) {
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to read response body: %w", err)
	}
	if o.requestBatchID != "" {
		bodyBytes, err = sjson.SetBytes(bodyBytes, "id", o.requestBatchID)
		if err != nil {
			return nil, nil, tokenUsage, "", fmt.Errorf("failed to set batch ID: %w", err)
		}
	}
	bodyBytes, err = encodeOutputAndErrorFileIDs(bodyBytes, o.requestModel, o.requestBackend)
	if err != nil {
		return nil, nil, tokenUsage, "", err
	}
	newBody = bodyBytes
	newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	return
}

// ResponseError implements [OpenAICancelBatchTranslator.ResponseError].
func (o *openAIToOpenAITranslatorV1CancelBatch) ResponseError(respHeaders map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	return convertErrorOpenAIToOpenAIError(respHeaders, body)
}
