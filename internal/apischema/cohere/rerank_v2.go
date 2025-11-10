// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package cohere contains Cohere API schema definitions.
package cohere

// RerankV2Request represents the request body for Cohere Rerank API v2.
// Docs: https://docs.cohere.com/reference/rerank
type RerankV2Request struct {
	// Model identifier to use, e.g. "rerank-v3.5".
	Model string `json:"model"`
	// Query to rank documents against.
	Query string `json:"query"`
	// Documents to be compared with the query. For best performance, keep under 1000.
	// Long documents may be truncated server-side by max_tokens_per_doc.
	Documents []string `json:"documents"`
	// Optional: limit returned results to top_n.
	TopN *int `json:"top_n,omitempty"`
	// Optional: truncate long documents to this many tokens. Default: 4096.
	MaxTokensPerDoc *int `json:"max_tokens_per_doc,omitempty"`
}

// RerankV2Response represents the response from Cohere Rerank API v2.
// Docs: https://docs.cohere.com/reference/rerank
type RerankV2Response struct {
	// Ordered list of ranked documents with scores.
	Results []*RerankV2Result `json:"results"`
	// Unique request ID.
	ID *string `json:"id,omitempty"`
	// Additional metadata including API version and billing.
	Meta *RerankV2Meta `json:"meta,omitempty"`
}

// RerankV2Result is a single ranked item in the response.
type RerankV2Result struct {
	// Index is the position of the matched item in the input documents slice.
	Index int `json:"index"`
	// RelevanceScore is the model-assigned score indicating how well the
	// document matches the query (higher means more relevant).
	RelevanceScore float64 `json:"relevance_score"`
}

// RerankV2Meta contains metadata returned by the API.
type RerankV2Meta struct {
	// APIVersion contains the version information for the API that processed the request.
	APIVersion *RerankV2APIVersion `json:"api_version,omitempty"`
	// BilledUnits reports the billed resource usage for this request.
	BilledUnits *RerankV2BilledUnits `json:"billed_units,omitempty"`
	// Tokens provides the token usage breakdown for the request/response.
	Tokens *RerankV2Tokens `json:"tokens,omitempty"`
	// CachedTokens is the number of prompt tokens that hit the inference cache.
	CachedTokens *float64 `json:"cached_tokens,omitempty"`
	// Warnings contains any non-fatal warnings generated while processing the request.
	Warnings []string `json:"warnings,omitempty"`
}

// RerankV2APIVersion describes the API version details in the response meta.
type RerankV2APIVersion struct {
	// Version is the API version string (e.g., "2").
	Version string `json:"version"`
	// IsDeprecated indicates whether this API version is deprecated (nullable).
	IsDeprecated *bool `json:"is_deprecated,omitempty"`
	// IsExperimental indicates whether this API version is experimental (nullable).
	IsExperimental *bool `json:"is_experimental,omitempty"`
}

// RerankV2BilledUnits contains usage metrics related to the request.
type RerankV2BilledUnits struct {
	// Images is the number of billed images (nullable).
	Images *float64 `json:"images,omitempty"`
	// InputTokens is the number of billed input tokens (nullable).
	InputTokens *float64 `json:"input_tokens,omitempty"`
	// OutputTokens is the number of billed output tokens (nullable).
	OutputTokens *float64 `json:"output_tokens,omitempty"`
	// SearchUnits is the number of billed search units (nullable).
	SearchUnits *float64 `json:"search_units,omitempty"`
	// Classifications is the number of billed classification units (nullable).
	Classifications *float64 `json:"classifications,omitempty"`
}

// RerankV2Tokens captures token accounting for the request.
// Docs: https://docs.cohere.com/reference/rerank#response.body.meta.tokens
type RerankV2Tokens struct {
	// InputTokens is the number of tokens used as input to the model (nullable).
	InputTokens *float64 `json:"input_tokens,omitempty"`
	// OutputTokens is the number of tokens produced by the model (nullable).
	OutputTokens *float64 `json:"output_tokens,omitempty"`
}

// RerankV2Error describes a Cohere v2 error.
type RerankV2Error struct {
	// ID is a unique identifier for the error (nullable).
	ID *string `json:"id,omitempty"`
	// Message is a human-readable description of the error (nullable).
	Message *string `json:"message,omitempty"`
}
