// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package huggingfacetei contains HuggingFace Text Embeddings Inference (TEI) API schema definitions.
//
// https://huggingface.github.io/text-embeddings-inference
package huggingfacetei

// RerankRequest represents the request body for the TEI native /rerank endpoint.
// TEI serves a single model per instance, so there is no model field.
type RerankRequest struct {
	// Query to rank the texts against.
	Query string `json:"query"`
	// Texts to be compared with the query.
	Texts []string `json:"texts"`
}

// RerankResult is a single ranked item in the TEI /rerank response.
type RerankResult struct {
	// Index is the position of the matched item in the input texts slice.
	Index int `json:"index"`
	// Score is the model-assigned relevance score (higher means more relevant).
	Score float64 `json:"score"`
}

// RerankResponse represents the response from the TEI /rerank endpoint:
// a JSON array of results sorted by score in descending order.
type RerankResponse []RerankResult

// Error describes a TEI error response body.
type Error struct {
	// Error is a human-readable description of the error.
	Error string `json:"error"`
	// ErrorType is the category of the error, e.g. "Validation".
	ErrorType string `json:"error_type,omitempty"`
}
