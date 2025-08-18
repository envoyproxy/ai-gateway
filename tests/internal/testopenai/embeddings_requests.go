// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"bytes"

	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// EmbeddingsCassettes returns a slice of all cassettes for embeddings.
func EmbeddingsCassettes() []Cassette {
	return cassettes(embeddingsRequests)
}

// embeddingsRequests contains the actual request body for each embeddings cassette.
var embeddingsRequests = map[Cassette]*openai.EmbeddingRequest{
	CassetteEmbeddingsBasic: {
		Model: openai.ModelTextEmbedding3Small,
		Input: openai.StringOrArray{
			Value: "hello world",
		},
	},
	CassetteEmbeddingsBase64: {
		Model: openai.ModelTextEmbedding3Small,
		Input: openai.StringOrArray{
			Value: "Convert this text to base64 encoded embeddings",
		},
		EncodingFormat: ptr.To("base64"),
	},
	CassetteEmbeddingsTokens: {
		Model: openai.ModelTextEmbedding3Small,
		Input: openai.StringOrArray{
			// Between 0 and maximum value 100257 (inclusive).
			Value: []int{1, 5678, 91011, 100257},
		},
	},
	CassetteEmbeddingsLargeText: {
		Model: openai.ModelTextEmbedding3Small,
		Input: openai.StringOrArray{
			Value: "The quick brown fox jumps over the lazy dog. This pangram sentence contains every letter of the English alphabet at least once. It has been used since at least the late 19th century to test typewriters and computer keyboards, display examples of fonts, and other applications involving text where the use of all letters in the alphabet is desired. The phrase is commonly used for touch-typing practice, testing typewriters and computer keyboards, and displaying examples of fonts. It is also used in other applications involving all the letters in the English alphabet.",
		},
	},
	CassetteEmbeddingsUnknownModel: {
		Model: "text-embedding-4-ultra", // Non-existent model.
		Input: openai.StringOrArray{
			Value: "Test with unknown model",
		},
	},
	CassetteEmbeddingsDimensions: {
		Model: openai.ModelTextEmbedding3Small,
		Input: openai.StringOrArray{
			Value: "Generate embeddings with specific dimensions",
		},
		Dimensions: ptr.To(256), // Reduced dimensionality.
	},
	CassetteEmbeddingsMaxTokens: {
		Model: openai.ModelTextEmbedding3Small,
		Input: openai.StringOrArray{
			// Near 8191 token limit for openai embeddings models.
			Value: generateLongText(7500),
		},
	},
	CassetteEmbeddingsMixedBatch: {
		Model: openai.ModelTextEmbedding3Small,
		Input: openai.StringOrArray{
			Value: []string{
				"Hello 世界! 🌍",    // Mixed scripts and emoji.
				"Здравствуй мир", // Cyrillic.
				"مرحبا بالعالم",  // Arabic.
				"This is a much longer piece of text that contains multiple sentences. It tests how the embedding model handles varying input lengths within the same batch. The embeddings should capture the semantic meaning despite the length differences.",
				"🚀 Space emoji and symbols ✨ § ¶ †", // Special characters.
			},
		},
	},
	CassetteEmbeddingsWhitespace: {
		Model: openai.ModelTextEmbedding3Small,
		Input: openai.StringOrArray{
			Value: []string{
				"   Leading spaces",
				"Trailing spaces   ",
				"Multiple   spaces   between   words",
				"\tTabs\tand\nnewlines\r\neverywhere",
				"  \n  \t  ", // Only whitespace.
			},
		},
	},
	CassetteEmbeddingsBadRequest: {
		Model: openai.ModelTextEmbedding3Small,
		Input: openai.StringOrArray{
			// Above maximum value 100257 (inclusive).
			Value: []int{102257},
		},
		EncodingFormat: ptr.To("invalid_format"), // Invalid encoding format.
		Dimensions:     ptr.To(-1),               // Invalid negative dimensions.
	},
}

// generateLongText creates a long text string for testing token limits.
func generateLongText(approxChars int) string {
	// This simulates a realistic document that might be embedded.
	base := `In the field of natural language processing and machine learning, embeddings have become a fundamental representation technique. They transform discrete tokens, whether words, subwords, or characters, into dense vector representations in a continuous vector space. This transformation enables mathematical operations on text and captures semantic relationships between different pieces of text. `

	var result bytes.Buffer
	for result.Len() < approxChars {
		result.WriteString(base)
	}
	return result.String()[:approxChars]
}
