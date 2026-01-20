// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dynamicmodule

import (
	"log/slog"
	"sync"

	"github.com/envoyproxy/ai-gateway/internal/dynamicmodule/sdk"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// endpoint represents the type of the endpoint that the request is targeting.
type endpoint int

const (
	// chatCompletionsEndpoint represents the /v1/chat/completions endpoint.
	chatCompletionsEndpoint endpoint = iota
	// completionsEndpoint represents the /v1/completions endpoint.
	completionsEndpoint
	// embeddingsEndpoint represents the /v1/embeddings endpoint.
	embeddingsEndpoint
	// imagesGenerationsEndpoint represents the /v1/images/generations endpoint.
	imagesGenerationsEndpoint
	// rerankEndpoint represents the /v2/rerank endpoint of cohere.
	rerankEndpoint
	// messagesEndpoint represents the /v1/messages endpoint of anthropic.
	messagesEndpoint
	// responsesEndpoint represents the /v1/responses endpoint of anthropic.
	responsesEndpoint
	// modelsEndpoint represents the /v1/models endpoint.
	modelsEndpoint
)

// String implements fmt.Stringer.
func (e endpoint) String() string {
	return [...]string{
		"chat_completions",
		"completions",
		"embeddings",
		"image_generations",
		"rerank",
		"messages",
		"responses",
		"models",
	}[e]
}

// Env holds the environment configuration for the dynamic module that is process-wide.
type Env struct {
	RootPrefix       string
	EndpointPrefixes internalapi.EndpointPrefixes
	ChatCompletionMetricsFactory,
	MessagesMetricsFactory,
	CompletionMetricsFactory,
	EmbeddingsMetricsFactory,
	ImageGenerationMetricsFactory,
	RerankMetricsFactory metrics.Factory
	Tracing         tracingapi.Tracing
	RouterFilters   *RouterFilters
	Logger          *slog.Logger
	DebugLogEnabled bool
}

// RouterFilters holds the instantiated router filters keyed on the request-id.
type RouterFilters struct {
	Filters map[string]RouterFilterItem
	Lock    sync.RWMutex
}

type RouterFilterItem interface {
	Endpoint() endpoint
	sdk.HTTPFilter
}
