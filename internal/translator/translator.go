// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"io"

	openaisdk "github.com/openai/openai-go/v2"
	"github.com/tidwall/sjson"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

const (
	pathHeaderName          = ":path"
	statusHeaderName        = ":status"
	contentTypeHeaderName   = "content-type"
	contentLengthHeaderName = "content-length"
	awsErrorTypeHeaderName  = "x-amzn-errortype"
	jsonContentType         = "application/json"
	eventStreamContentType  = "text/event-stream"
	openAIBackendError      = "OpenAIBackendError"
	awsBedrockBackendError  = "AWSBedrockBackendError"
)

// Translator translates the request and response messages between the client
// and the backend API schemas.
//
// ReqT represents the structured request body type.
// SpanT represents the tracing span type passed to ResponseBody. Use any if the implementation does not have tracing span yet.
//
// This is created per request and is not thread-safe.
type Translator[ReqT any, SpanT any] interface {
	// RequestBody translates the request body.
	//     - `raw` is the raw request body.
	//     - `body` is the parsed request body of type *ReqT.
	//     - `flag` is a boolean context flag. Depending on the specific implementation,
	//       this represents either `forceBodyMutation` or `onRetry`.
	RequestBody(raw []byte, body *ReqT, flag bool) (
		newHeaders []internalapi.Header,
		mutatedBody []byte,
		err error,
	)

	// ResponseHeaders translates the response headers.
	ResponseHeaders(headers map[string]string) (
		newHeaders []internalapi.Header,
		err error,
	)

	// ResponseBody translates the response body.
	//     - `span` is the tracing span of type SpanT. Implementations that do not
	//       require tracing in this step can ignore this argument.
	ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool, span SpanT) (
		newHeaders []internalapi.Header,
		mutatedBody []byte,
		tokenUsage metrics.TokenUsage,
		responseModel internalapi.ResponseModel,
		err error,
	)

	// ResponseError translates the response error (non-2xx status codes).
	ResponseError(respHeaders map[string]string, body io.Reader) (
		newHeaders []internalapi.Header,
		mutatedBody []byte,
		err error,
	)
}

type (
	// OpenAIChatCompletionTranslator translates the OpenAI's /chat/completions endpoint.
	OpenAIChatCompletionTranslator = Translator[openai.ChatCompletionRequest, tracing.ChatCompletionSpan]
	// OpenAIEmbeddingTranslator translates the OpenAI's /embeddings endpoint.
	OpenAIEmbeddingTranslator = Translator[openai.EmbeddingRequest, tracing.EmbeddingsSpan]
	// OpenAICompletionTranslator translates the OpenAI's /completions endpoint.
	OpenAICompletionTranslator = Translator[openai.CompletionRequest, tracing.CompletionSpan]
	// CohereRerankTranslator translates the Cohere's /v2/rerank endpoint.
	CohereRerankTranslator = Translator[cohereschema.RerankV2Request, tracing.RerankSpan]
	// AnthropicMessagesTranslator translates the Anthropic's /messages endpoint.
	AnthropicMessagesTranslator = Translator[anthropicschema.MessagesRequest, tracing.MessageSpan]
	// OpenAIImageGenerationTranslator translates the OpenAI's /images/generations endpoint.
	OpenAIImageGenerationTranslator = Translator[openaisdk.ImageGenerateParams, tracing.ImageGenerationSpan]
)

// sjsonOptions are the options used for sjson operations in the translator.
var sjsonOptions = &sjson.Options{
	Optimistic: true,
	// Note: DO NOT set ReplaceInPlace to true since at the translation layer, which might be called multiple times per retry,
	// it must be ensured that the original body is not modified, i.e. the operation must be idempotent.
	ReplaceInPlace: false,
}
