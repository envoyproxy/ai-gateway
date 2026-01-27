// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package gcp

import (
	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

type GenerateContentRequest struct {
	// Contains the multipart content of a message.
	//
	// https://github.com/googleapis/go-genai/blob/6a8184fcaf8bf15f0c566616a7b356560309be9b/types.go#L858
	Contents []genai.Content `json:"contents"`
	// Tool details of a tool that the model may use to generate a response.
	//
	// https://github.com/googleapis/go-genai/blob/6a8184fcaf8bf15f0c566616a7b356560309be9b/types.go#L1406
	Tools []genai.Tool `json:"tools"`
	// Optional. Tool config.
	// This config is shared for all tools provided in the request.
	//
	// https://github.com/googleapis/go-genai/blob/6a8184fcaf8bf15f0c566616a7b356560309be9b/types.go#L1466
	ToolConfig *genai.ToolConfig `json:"tool_config,omitempty"`
	// Optional. Generation config.
	// You can find API default values and more details at https://cloud.google.com/vertex-ai/generative-ai/docs/model-reference/inference#generationconfig
	// and https://cloud.google.com/vertex-ai/generative-ai/docs/multimodal/content-generation-parameters.
	GenerationConfig *genai.GenerationConfig `json:"generation_config,omitempty"`
	// Optional. Instructions for the model to steer it toward better performance.
	// For example, "Answer as concisely as possible" or "Don't use technical
	// terms in your response".
	//
	// https://github.com/googleapis/go-genai/blob/6a8184fcaf8bf15f0c566616a7b356560309be9b/types.go#L858
	SystemInstruction *genai.Content `json:"system_instruction,omitempty"`
	// Optional: Safety settings in the request to block unsafe content in the response.
	//
	// https://github.com/googleapis/go-genai/blob/6a8184fcaf8bf15f0c566616a7b356560309be9b/types.go#L1057
	SafetySettings []*genai.SafetySetting `json:"safetySettings,omitempty"`
}

// https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/text-embeddings-api#syntax
type Instance struct {
	// The text that you want to generate embeddings for.
	Content string `json:"content"`

	// Used to convey intended downstream application to help the model produce better embeddings. If left blank, the default used is RETRIEVAL_QUERY.
	// For more information about task types, see https://docs.cloud.google.com/vertex-ai/generative-ai/docs/embeddings/task-types
	// https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/text-embeddings-api#task_type
	TaskType openai.EmbeddingTaskType `json:"task_type,omitempty"`

	// Used to help the model produce better embeddings. Only valid with task_type=RETRIEVAL_DOCUMENT.
	Title string `json:"title,omitempty"`
}

// https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/text-embeddings-api#parameter-list
type Parameters struct {
	// When set to true, input text will be truncated. When set to false, an error is returned if the input text is longer than the maximum length supported by the model. Defaults to true.
	AutoTruncate bool `json:"auto_truncate,omitempty"`

	// Used to specify output embedding size. If set, output embeddings will be truncated to the size specified.
	OutputDimensionality int `json:"outputDimensionality,omitempty"`
}

// https://github.com/googleapis/python-aiplatform/blob/30e41d01f3fd0ef08da6ad6eb7f83df34476105e/google/cloud/aiplatform_v1/types/prediction_service.py#L63
type PredictRequest struct {
	// A list of instances
	//
	Instances []*Instance `json:"instances"`

	// Optional configuration for the embedding request.
	// Uses the official genai library configuration structure.
	Parameters Parameters `json:"parameters,omitempty"`
}

// ContentEmbeddingStatistics contains statistics about the embedding.
// Note: We use custom struct instead of genai.ContentEmbeddingStatistics because
// the GCP API returns snake_case JSON fields (token_count), while the genai library
// uses camelCase (tokenCount).
// https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/text-embeddings-api#response_body
type ContentEmbeddingStatistics struct {
	// The number of tokens in the input text.
	TokenCount int `json:"token_count,omitempty"`
	// Whether the input text was truncated.
	Truncated bool `json:"truncated,omitempty"`
}

// ContentEmbedding represents the embedding result from GCP Vertex AI.
// Note: We use custom struct instead of genai.ContentEmbedding to ensure
// correct JSON field names for statistics (snake_case vs camelCase).
type ContentEmbedding struct {
	// The embedding values.
	Values []float32 `json:"values,omitempty"`
	// Statistics about the embedding.
	Statistics *ContentEmbeddingStatistics `json:"statistics,omitempty"`
}

// https://docs.cloud.google.com/vertex-ai/generative-ai/docs/model-reference/text-embeddings-api#response_body
type Prediction struct {
	// The result generated from input text.
	Embeddings ContentEmbedding `json:"embeddings"`
}

// https://github.com/googleapis/python-aiplatform/blob/30e41d01f3fd0ef08da6ad6eb7f83df34476105e/google/cloud/aiplatform_v1/types/prediction_service.py#L117
type PredictResponse struct {
	Predictions []*Prediction `json:"predictions"`
}
