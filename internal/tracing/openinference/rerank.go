// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openinference

import "fmt"

// Additional Span Kind for Reranker.
const (
	// SpanKindReranker indicates a Reranker operation.
	// Reference: https://github.com/Arize-ai/openinference/blob/main/spec/semantic_conventions.md
	SpanKindReranker = "RERANKER"
)

// Reranker Operation constants (OpenInference).
// Reference: https://github.com/Arize-ai/openinference/blob/main/spec/semantic_conventions.md
const (
	// RerankerInputDocuments base prefix for input documents list.
	// Flattened form: reranker.input_documents.{idx}.document.content, reranker.input_documents.{idx}.document.id, ...
	RerankerInputDocuments = "reranker.input_documents"

	// RerankerOutputDocuments base prefix for output documents list.
	// Flattened form: reranker.output_documents.{idx}.document.score, reranker.output_documents.{idx}.document.id, ...
	RerankerOutputDocuments = "reranker.output_documents"

	// RerankerModelName identifies the reranker model name.
	RerankerModelName = "reranker.model_name"

	// RerankerQuery holds the reranker query string.
	RerankerQuery = "reranker.query"

	// RerankerTopK holds the top K parameter for reranking.
	RerankerTopK = "reranker.top_k"
)

// Reserved document sub-attributes used under input/output documents.
const (
	DocumentID      = "document.id"
	DocumentContent = "document.content"
	DocumentScore   = "document.score"
)

// LLMSystem values (extend with Cohere).
const (
	// LLMSystemCohere for Cohere systems.
	LLMSystemCohere = "cohere"
)

// RerankerInputDocumentAttribute returns a flattened key for an input document sub-attribute.
// Example: reranker.input_documents.0.document.content
func RerankerInputDocumentAttribute(index int, docAttr string) string {
	return fmt.Sprintf("%s.%d.%s", RerankerInputDocuments, index, docAttr)
}

// RerankerOutputDocumentAttribute returns a flattened key for an output document sub-attribute.
// Example: reranker.output_documents.0.document.score
func RerankerOutputDocumentAttribute(index int, docAttr string) string {
	return fmt.Sprintf("%s.%d.%s", RerankerOutputDocuments, index, docAttr)
}
