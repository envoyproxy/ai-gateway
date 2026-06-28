// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"io"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

// There is one concrete identity (pass-through) translator per Files operation, each used for
// OpenAI-compatible backends. Every method is a no-op: the files processor (internal/extproc) keeps its own
// canonical native-id-substituted path/body on the way upstream and re-encodes ids on the raw
// response on the way back, so the translators can only focus on the OpenAI to/from provider API mapping.
type (
	openAIFileUploadTranslator   struct{}
	openAIFileRetrieveTranslator struct{}
	openAIFileDeleteTranslator   struct{}
	openAIFileListTranslator     struct{}
)

// Compile-time assertions that each per-op identity satisfies the reused generic interface for files.
var (
	_ FilesTranslator = (*openAIFileUploadTranslator)(nil)
	_ FilesTranslator = (*openAIFileRetrieveTranslator)(nil)
	_ FilesTranslator = (*openAIFileDeleteTranslator)(nil)
	_ FilesTranslator = (*openAIFileListTranslator)(nil)
)

// --- upload: POST /v1/files ---

// RequestBody implements [Translator.RequestBody]. Returning a nil body keeps the processor's
// canonical (native-id-substituted) request unchanged; returning nil headers leaves :path/:method
// as the processor set them.
func (*openAIFileUploadTranslator) RequestBody(_ []byte, _ *FilesRequest, _ bool) (
	newHeaders []internalapi.Header, mutatedBody []byte, err error,
) {
	return nil, nil, nil
}

// ResponseHeaders implements [Translator.ResponseHeaders]. The OpenAI response headers need no
// translation.
func (*openAIFileUploadTranslator) ResponseHeaders(map[string]string) (
	newHeaders []internalapi.Header, err error,
) {
	return nil, nil
}

// ResponseBody implements [Translator.ResponseBody]. Files carry no token usage and no response
// model, and the body is already OpenAI-shaped, so this returns a nil body (pass through unchanged)
// with zero usage. The processor re-encodes native ids into gateway ids on the raw response itself.
func (*openAIFileUploadTranslator) ResponseBody(_ map[string]string, _ io.Reader, _ bool, _ any) (
	newHeaders []internalapi.Header,
	mutatedBody []byte,
	tokenUsage metrics.TokenUsage,
	responseModel internalapi.ResponseModel,
	err error,
) {
	return nil, nil, metrics.TokenUsage{}, "", nil
}

// ResponseError implements [Translator.ResponseError]. OpenAI error envelopes pass through unchanged.
func (*openAIFileUploadTranslator) ResponseError(_ map[string]string, _ io.Reader) (
	newHeaders []internalapi.Header, mutatedBody []byte, err error,
) {
	return nil, nil, nil
}

// --- retrieve: GET /v1/files/{id} (and the schema gate for /v1/files/{id}/content) ---

// RequestBody implements [Translator.RequestBody]. Returning a nil body keeps the processor's
// canonical (native-id-substituted) request unchanged; returning nil headers leaves :path/:method
// as the processor set them.
func (*openAIFileRetrieveTranslator) RequestBody(_ []byte, _ *FilesRequest, _ bool) (
	newHeaders []internalapi.Header, mutatedBody []byte, err error,
) {
	return nil, nil, nil
}

// ResponseHeaders implements [Translator.ResponseHeaders]. The OpenAI response headers need no
// translation.
func (*openAIFileRetrieveTranslator) ResponseHeaders(map[string]string) (
	newHeaders []internalapi.Header, err error,
) {
	return nil, nil
}

// ResponseBody implements [Translator.ResponseBody]. Files carry no token usage and no response
// model, and the body is already OpenAI-shaped, so this returns a nil body (pass through unchanged)
// with zero usage. The processor re-encodes native ids into gateway ids on the raw response itself.
func (*openAIFileRetrieveTranslator) ResponseBody(_ map[string]string, _ io.Reader, _ bool, _ any) (
	newHeaders []internalapi.Header,
	mutatedBody []byte,
	tokenUsage metrics.TokenUsage,
	responseModel internalapi.ResponseModel,
	err error,
) {
	return nil, nil, metrics.TokenUsage{}, "", nil
}

// ResponseError implements [Translator.ResponseError]. OpenAI error envelopes pass through unchanged.
func (*openAIFileRetrieveTranslator) ResponseError(_ map[string]string, _ io.Reader) (
	newHeaders []internalapi.Header, mutatedBody []byte, err error,
) {
	return nil, nil, nil
}

// --- delete: DELETE /v1/files/{id} ---

// RequestBody implements [Translator.RequestBody]. Returning a nil body keeps the processor's
// canonical (native-id-substituted) request unchanged; returning nil headers leaves :path/:method
// as the processor set them.
func (*openAIFileDeleteTranslator) RequestBody(_ []byte, _ *FilesRequest, _ bool) (
	newHeaders []internalapi.Header, mutatedBody []byte, err error,
) {
	return nil, nil, nil
}

// ResponseHeaders implements [Translator.ResponseHeaders]. The OpenAI response headers need no
// translation.
func (*openAIFileDeleteTranslator) ResponseHeaders(map[string]string) (
	newHeaders []internalapi.Header, err error,
) {
	return nil, nil
}

// ResponseBody implements [Translator.ResponseBody]. Files carry no token usage and no response
// model, and the body is already OpenAI-shaped, so this returns a nil body (pass through unchanged)
// with zero usage. The processor re-encodes native ids into gateway ids on the raw response itself.
func (*openAIFileDeleteTranslator) ResponseBody(_ map[string]string, _ io.Reader, _ bool, _ any) (
	newHeaders []internalapi.Header,
	mutatedBody []byte,
	tokenUsage metrics.TokenUsage,
	responseModel internalapi.ResponseModel,
	err error,
) {
	return nil, nil, metrics.TokenUsage{}, "", nil
}

// ResponseError implements [Translator.ResponseError]. OpenAI error envelopes pass through unchanged.
func (*openAIFileDeleteTranslator) ResponseError(_ map[string]string, _ io.Reader) (
	newHeaders []internalapi.Header, mutatedBody []byte, err error,
) {
	return nil, nil, nil
}

// --- list: GET /v1/files ---

// RequestBody implements [Translator.RequestBody]. Returning a nil body keeps the processor's
// canonical (native-cursor-substituted) request unchanged; returning nil headers leaves :path/:method
// as the processor set them.
func (*openAIFileListTranslator) RequestBody(_ []byte, _ *FilesRequest, _ bool) (
	newHeaders []internalapi.Header, mutatedBody []byte, err error,
) {
	return nil, nil, nil
}

// ResponseHeaders implements [Translator.ResponseHeaders]. The OpenAI response headers need no
// translation.
func (*openAIFileListTranslator) ResponseHeaders(map[string]string) (
	newHeaders []internalapi.Header, err error,
) {
	return nil, nil
}

// ResponseBody implements [Translator.ResponseBody]. Files carry no token usage and no response
// model, and the body is already an OpenAI-shaped list envelope, so this returns a nil body (pass
// through unchanged) with zero usage. The processor re-encodes native ids into gateway ids and
// stitches the cross-backend walk on the raw response itself.
func (*openAIFileListTranslator) ResponseBody(_ map[string]string, _ io.Reader, _ bool, _ any) (
	newHeaders []internalapi.Header,
	mutatedBody []byte,
	tokenUsage metrics.TokenUsage,
	responseModel internalapi.ResponseModel,
	err error,
) {
	return nil, nil, metrics.TokenUsage{}, "", nil
}

// ResponseError implements [Translator.ResponseError]. OpenAI error envelopes pass through unchanged.
func (*openAIFileListTranslator) ResponseError(_ map[string]string, _ io.Reader) (
	newHeaders []internalapi.Header, mutatedBody []byte, err error,
) {
	return nil, nil, nil
}
