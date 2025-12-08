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

var cassetteEmbeddingsBasic = &openai.EmbeddingRequest{
	OfCompletion: &openai.EmbeddingCompletionRequest{
		Model:          openai.ModelTextEmbedding3Small,
		Input:          openai.EmbeddingRequestInput{Value: "How do I reset my password?"},
		EncodingFormat: ptr.To("float"),
	},
}

// embeddingsRequests contains the actual request body for each embeddings cassette.
var embeddingsRequests = map[Cassette]*openai.EmbeddingRequest{
	CassetteEmbeddingsBasic: cassetteEmbeddingsBasic,
	CassetteEmbeddingsBase64: {
		OfCompletion: &openai.EmbeddingCompletionRequest{
			Model:          openai.ModelTextEmbedding3Small,
			Input:          openai.EmbeddingRequestInput{Value: "How do I reset my password?"},
			EncodingFormat: ptr.To("base64"),
		},
	},
	CassetteEmbeddingsTokens: {
		OfCompletion: &openai.EmbeddingCompletionRequest{
			Model:          openai.ModelTextEmbedding3Small,
			Input:          openai.EmbeddingRequestInput{Value: []int64{4438, 656, 358, 7738, 856, 3636, 30}},
			EncodingFormat: ptr.To("base64"),
		},
	},
	CassetteEmbeddingsLargeText: {
		OfCompletion: &openai.EmbeddingCompletionRequest{
			Model: openai.ModelTextEmbedding3Small,
			Input: openai.EmbeddingRequestInput{
				Value: "The quick brown fox jumps over the lazy dog. This pangram sentence contains every letter of the English alphabet at least once. It has been used since at least the late 19th century to test typewriters and computer keyboards, display examples of fonts, and other applications involving text where the use of all letters in the alphabet is desired. The phrase is commonly used for touch-typing practice, testing typewriters and computer keyboards, and displaying examples of fonts. It is also used in other applications involving all the letters in the English alphabet.",
			},
			EncodingFormat: ptr.To("base64"),
		},
	},
	CassetteEmbeddingsUnknownModel: {
		OfCompletion: &openai.EmbeddingCompletionRequest{
			Model: "text-embedding-4-ultra", // Non-existent model.
			Input: openai.EmbeddingRequestInput{
				Value: "Test with unknown model",
			},
			EncodingFormat: ptr.To("base64"),
		},
	},
	CassetteEmbeddingsDimensions: {
		OfCompletion: &openai.EmbeddingCompletionRequest{
			Model: openai.ModelTextEmbedding3Small,
			Input: openai.EmbeddingRequestInput{
				Value: "Generate embeddings with specific dimensions",
			},
			Dimensions:     ptr.To(256), // Reduced dimensionality.
			EncodingFormat: ptr.To("base64"),
		},
	},
	CassetteEmbeddingsMaxTokens: {
		OfCompletion: &openai.EmbeddingCompletionRequest{
			Model: openai.ModelTextEmbedding3Small,
			Input: openai.EmbeddingRequestInput{
				// Near 8191 token limit for openai embeddings models.
				Value: generateLongText(7500),
			},
			EncodingFormat: ptr.To("base64"),
		},
	},
	CassetteEmbeddingsMixedBatch: {
		OfCompletion: &openai.EmbeddingCompletionRequest{
			Model: openai.ModelTextEmbedding3Small,
			Input: openai.EmbeddingRequestInput{
				Value: []string{
					"Hello 世界! 🌍",    // Mixed scripts and emoji.
					"Здравствуй мир", // Cyrillic.
					"مرحبا بالعالم",  // Arabic.
					"This is a much longer piece of text that contains multiple sentences. It tests how the embedding model handles varying input lengths within the same batch. The embeddings should capture the semantic meaning despite the length differences.",
					"🚀 Space emoji and symbols ✨ § ¶ †", // Special characters.
				},
			},
			EncodingFormat: ptr.To("base64"),
		},
	},
	CassetteEmbeddingsWhitespace: {
		OfCompletion: &openai.EmbeddingCompletionRequest{
			Model: openai.ModelTextEmbedding3Small,
			Input: openai.EmbeddingRequestInput{
				Value: []string{
					"   Leading spaces",
					"Trailing spaces   ",
					"Multiple   spaces   between   words",
					"\tTabs\tand\nnewlines\r\neverywhere",
					"  \n  \t  ", // Only whitespace.
				},
			},
			EncodingFormat: ptr.To("base64"),
		},
	},
	CassetteEmbeddingsBadRequest: {
		OfCompletion: &openai.EmbeddingCompletionRequest{
			Model: openai.ModelTextEmbedding3Small,
			Input: openai.EmbeddingRequestInput{
				// Above maximum value 100257 (inclusive).
				Value: []int64{102257},
			},
			EncodingFormat: ptr.To("invalid_format"), // Invalid encoding format.
			Dimensions:     ptr.To(-1),               // Invalid negative dimensions.
		},
	},
	// NEW: Chat-style embedding request test cases
	CassetteEmbeddingsChatBasic: {
		OfChat: &openai.EmbeddingChatRequest{
			Model: openai.ModelTextEmbedding3Small,
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role: openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{
							Value: "What is machine learning?",
						},
					},
				},
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Role: openai.ChatMessageRoleAssistant,
						Content: openai.StringOrAssistantRoleContentUnion{
							Value: "Machine learning is a subset of artificial intelligence.",
						},
					},
				},
			},
			EncodingFormat: ptr.To("float"),
		},
	},
	CassetteEmbeddingsChatMultiTurn: {
		OfChat: &openai.EmbeddingChatRequest{
			Model: openai.ModelTextEmbedding3Small,
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfSystem: &openai.ChatCompletionSystemMessageParam{
						Role: openai.ChatMessageRoleSystem,
						Content: openai.ContentUnion{
							Value: "You are a helpful AI assistant.",
						},
					},
				},
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role: openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{
							Value: "How does neural network training work?",
						},
					},
				},
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Role: openai.ChatMessageRoleAssistant,
						Content: openai.StringOrAssistantRoleContentUnion{
							Value: "Neural networks learn through backpropagation.",
						},
					},
				},
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role: openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{
							Value: "Can you explain gradient descent?",
						},
					},
				},
				{
					OfAssistant: &openai.ChatCompletionAssistantMessageParam{
						Role: openai.ChatMessageRoleAssistant,
						Content: openai.StringOrAssistantRoleContentUnion{
							Value: "Gradient descent is an optimization algorithm.",
						},
					},
				},
			},
			EncodingFormat: ptr.To("base64"),
		},
	},
	CassetteEmbeddingsChatWithDimensions: {
		OfChat: &openai.EmbeddingChatRequest{
			Model: openai.ModelTextEmbedding3Small,
			Messages: []openai.ChatCompletionMessageParamUnion{
				{
					OfUser: &openai.ChatCompletionUserMessageParam{
						Role: openai.ChatMessageRoleUser,
						Content: openai.StringOrUserRoleContentUnion{
							Value: "Generate a reduced-dimension embedding for this conversation.",
						},
					},
				},
			},
			Dimensions:     ptr.To(512),
			EncodingFormat: ptr.To("float"),
		},
	},
	CassetteEmbeddingsChatEmpty: {
		OfChat: &openai.EmbeddingChatRequest{
			Model:          openai.ModelTextEmbedding3Small,
			Messages:       []openai.ChatCompletionMessageParamUnion{},
			EncodingFormat: ptr.To("float"),
		},
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
