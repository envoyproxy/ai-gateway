// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

// FilesRequest is the OpenAI-shaped, processor-owned request context passed to a Files translator
// as the reused generic Translator's ReqT. The bespoke files processor (internal/extproc) has
// ALREADY substituted the backend-native id before calling, so NativeID/Path are native. The
// upload's raw multipart bytes flow through RequestBody's `raw` parameter (matching the existing
// raw-vs-parsed split used by the other endpoints); bodyless ops pass raw = nil.
//
// NOTE: the Files operation enum stays UNEXPORTED in internal/extproc (filesOperation). It is never
// passed into this package as a value — instead the processor's `switch p.op` calls the matching
// per-op constructor (NewFileUpload/Retrieve/Delete/ListTranslator), so the chosen translator
// *type* already encodes the op. This keeps the op an internal processor concern while still giving
// each operation its own translator type.
type FilesRequest struct {
	// NativeID is the backend-native file id, already substituted by the processor.
	NativeID string
	// Path is the request :path, already native-rewritten by the processor.
	Path string
	// Method is the request :method.
	Method string
	// ContentType is the request content-type (carries the multipart boundary for upload).
	ContentType string
	// Model is the upload routing model; "" for the other operations.
	Model string
	// Query is the list query with the native "after" cursor already substituted; "" otherwise.
	Query string
}

// FilesTranslator maps the OpenAI Files request/response shapes to a provider's file API. It reuses
// the existing generic Translator interface so provider authors keep a single mental model across
// chat/embeddings/files/batch. SpanT is `any` because files have no tracing span, so instantiating
// the generic here introduces NO dependency on internal/tracing.
type FilesTranslator = Translator[FilesRequest, any]

// There is one translator TYPE per Files operation, each a separate concrete identity type declared
// in openai_openai_files.go (openAIFile{Upload,Retrieve,Delete,List}Translator). They are NOT
// aliases of, nor embedders of, a shared no-op base: today all four implement the same no-op
// methods, but a future provider replaces each body with its own per-op mapping (upload→FileObject,
// retrieve→FileObject, delete→FileDeleted, list→envelope), so each op owns its method set from the
// start. This mirrors the translator-package convention of free-function constructors
// (NewSpeechOpenAIToOpenAITranslator, …) rather than a spec struct.

// NewFileUploadTranslator selects the POST /v1/files translator.
func NewFileUploadTranslator(schema filterapi.VersionedAPISchema) (FilesTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return &openAIFileUploadTranslator{}, nil
	default:
		return nil, errUnsupportedFilesSchema(schema)
	}
}

// NewFileRetrieveTranslator selects the GET /v1/files/{id} translator. The processor also calls this
// for GET /v1/files/{id}/content solely to apply the fail-closed schema gate; the content body
// itself is raw bytes and is never passed through the translator.
func NewFileRetrieveTranslator(schema filterapi.VersionedAPISchema) (FilesTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return &openAIFileRetrieveTranslator{}, nil
	default:
		return nil, errUnsupportedFilesSchema(schema)
	}
}

// NewFileDeleteTranslator selects the DELETE /v1/files/{id} translator.
func NewFileDeleteTranslator(schema filterapi.VersionedAPISchema) (FilesTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return &openAIFileDeleteTranslator{}, nil
	default:
		return nil, errUnsupportedFilesSchema(schema)
	}
}

// NewFileListTranslator selects the GET /v1/files translator.
func NewFileListTranslator(schema filterapi.VersionedAPISchema) (FilesTranslator, error) {
	switch schema.Name {
	case filterapi.APISchemaOpenAI:
		return &openAIFileListTranslator{}, nil
	default:
		return nil, errUnsupportedFilesSchema(schema)
	}
}

// errUnsupportedFilesSchema is the selection error the processor maps to HTTP 501 when
// a Files route points at a backend whose file translation does not yet exist.
func errUnsupportedFilesSchema(schema filterapi.VersionedAPISchema) error {
	return fmt.Errorf("unsupported API schema for files: backend=%s", schema)
}
