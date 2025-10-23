// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
	"io"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	openaisdk "github.com/openai/openai-go/v2"
	"github.com/tidwall/sjson"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

const (
	statusHeaderName       = ":status"
	contentTypeHeaderName  = "content-type"
	awsErrorTypeHeaderName = "x-amzn-errortype"
	jsonContentType        = "application/json"
	eventStreamContentType = "text/event-stream"
	openAIBackendError     = "OpenAIBackendError"
	awsBedrockBackendError = "AWSBedrockBackendError"
)

// OpenAIChatCompletionTranslator translates the request and response messages between the client and the backend API schemas
// for /v1/chat/completion endpoint of OpenAI.
//
// This is created per request and is not thread-safe.
type OpenAIChatCompletionTranslator interface {
	// RequestBody translates the request body.
	// 	- `raw` is the raw request body.
	// 	- `body` is the request body parsed into the [openai.ChatCompletionRequest].
	//	- `forceBodyMutation` is true if the translator should always mutate the body, even if no changes are made.
	//	- This returns `headerMutation` and `bodyMutation` that can be nil to indicate no mutation.
	RequestBody(raw []byte, body *openai.ChatCompletionRequest, forceBodyMutation bool) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		err error,
	)

	// ResponseHeaders translates the response headers.
	// 	- `headers` is the response headers.
	//	- This returns `headerMutation` that can be nil to indicate no mutation.
	ResponseHeaders(headers map[string]string) (
		headerMutation *extprocv3.HeaderMutation,
		err error,
	)

	// ResponseBody translates the response body. When stream=true, this is called for each chunk of the response body.
	// 	- `body` is the response body either chunk or the entire body, depending on the context.
	//	- This returns `headerMutation` and `bodyMutation` that can be nil to indicate no mutation.
	//  - This returns `tokenUsage` that is extracted from the body and will be used to do token rate limiting.
	//  - This returns `responseModel` that is the model name from the response (may differ from request model).
	ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool, span tracing.ChatCompletionSpan) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		tokenUsage LLMTokenUsage,
		responseModel internalapi.ResponseModel,
		err error,
	)

	// ResponseError translates the response error. This is called when the upstream response status code is not successful (2xx).
	// 	- `respHeaders` is the response headers.
	// 	- `body` is the response body that contains the error message.
	ResponseError(respHeaders map[string]string, body io.Reader) (headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error)
}

func setContentLength(headers *extprocv3.HeaderMutation, body []byte) {
	headers.SetHeaders = append(headers.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:      "content-length",
			RawValue: fmt.Appendf(nil, "%d", len(body)),
		},
	})
}

// OpenAIEmbeddingTranslator translates the request and response messages between the client and the backend API schemas
// for /v1/embeddings endpoint of OpenAI.
//
// This is created per request and is not thread-safe.
type OpenAIEmbeddingTranslator interface {
	// RequestBody translates the request body.
	// 	- `raw` is the raw request body.
	// 	- `body` is the request body parsed into the [openai.EmbeddingRequest].
	//	- `onRetry` is true if this is a retry request.
	//	- This returns `headerMutation` and `bodyMutation` that can be nil to indicate no mutation.
	RequestBody(raw []byte, body *openai.EmbeddingRequest, onRetry bool) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		err error,
	)

	// ResponseHeaders translates the response headers.
	// 	- `headers` is the response headers.
	//	- This returns `headerMutation` that can be nil to indicate no mutation.
	ResponseHeaders(headers map[string]string) (
		headerMutation *extprocv3.HeaderMutation,
		err error,
	)

	// ResponseBody translates the response body.
	// 	- `body` is the response body.
	//	- This returns `headerMutation` and `bodyMutation` that can be nil to indicate no mutation.
	//  - This returns `tokenUsage` that is extracted from the body and will be used to do token rate limiting.
	//  - This returns `responseModel` that is the model name from the response (may differ from request model).
	ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		tokenUsage LLMTokenUsage,
		responseModel internalapi.ResponseModel,
		err error,
	)

	// ResponseError translates the response error. This is called when the upstream response status code is not successful (2xx).
	// 	- `respHeaders` is the response headers.
	// 	- `body` is the response body that contains the error message.
	ResponseError(respHeaders map[string]string, body io.Reader) (headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error)
}

// OpenAICompletionTranslator translates the request and response messages between the client and the backend API schemas
// for /v1/completions endpoint of OpenAI.
//
// This is created per request and is not thread-safe.
type OpenAICompletionTranslator interface {
	// RequestBody translates the request body.
	// 	- `raw` is the raw request body.
	// 	- `body` is the request body parsed into the [openai.CompletionRequest].
	//	- `onRetry` is true if this is a retry request.
	//	- This returns `headerMutation` and `bodyMutation` that can be nil to indicate no mutation.
	RequestBody(raw []byte, body *openai.CompletionRequest, onRetry bool) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		err error,
	)

	// ResponseHeaders translates the response headers.
	// 	- `headers` is the response headers.
	//	- This returns `headerMutation` that can be nil to indicate no mutation.
	ResponseHeaders(headers map[string]string) (
		headerMutation *extprocv3.HeaderMutation,
		err error,
	)

	// ResponseBody translates the response body. When stream=true, this is called for each chunk of the response body.
	// 	- `body` is the response body either chunk or the entire body, depending on the context.
	//	- This returns `headerMutation` and `bodyMutation` that can be nil to indicate no mutation.
	//  - This returns `tokenUsage` that is extracted from the body and will be used to do token rate limiting.
	//  - This returns `responseModel` that is the model name from the response (may differ from request model).
	ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool, span tracing.CompletionSpan) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		tokenUsage LLMTokenUsage,
		responseModel internalapi.ResponseModel,
		err error,
	)

	// ResponseError translates the response error. This is called when the upstream response status code is not successful (2xx).
	// 	- `respHeaders` is the response headers.
	// 	- `body` is the response body that contains the error message.
	ResponseError(respHeaders map[string]string, body io.Reader) (headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error)
}

// AnthropicMessagesTranslator translates the request and response messages between the client and the backend API schemas
// for /v1/messages endpoint of Anthropic.
//
// This is created per request and is not thread-safe.
type AnthropicMessagesTranslator interface {
	// RequestBody translates the request body.
	// 	- `raw` is the raw request body.
	// 	- `body` is the request body parsed into the [anthropicschema.MessagesRequest].
	//	- `forceBodyMutation` is true if the translator should always mutate the body, even if no changes are made.
	//	- This returns `headerMutation` and `bodyMutation` that can be nil to indicate no mutation.
	RequestBody(raw []byte, body *anthropicschema.MessagesRequest, forceBodyMutation bool) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		err error,
	)

	// ResponseHeaders translates the response headers.
	// 	- `headers` is the response headers.
	//	- This returns `headerMutation` that can be nil to indicate no mutation.
	ResponseHeaders(headers map[string]string) (
		headerMutation *extprocv3.HeaderMutation,
		err error,
	)

	// ResponseBody translates the response body. When stream=true, this is called for each chunk of the response body.
	// 	- `body` is the response body either chunk or the entire body, depending on the context.
	//	- This returns `headerMutation` and `bodyMutation` that can be nil to indicate no mutation.
	//  - This returns `tokenUsage` that is extracted from the body and will be used to do token rate limiting.
	//  - This returns `responseModel` that is the model name from the response (may differ from request model).
	ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		tokenUsage LLMTokenUsage,
		responseModel internalapi.ResponseModel,
		err error,
	)
}

// LLMTokenUsage represents the token usage reported usually by the backend API in the response body.
type LLMTokenUsage struct {
	// InputTokens is the number of tokens consumed from the input.
	InputTokens uint32
	// OutputTokens is the number of tokens consumed from the output.
	OutputTokens uint32
	// TotalTokens is the total number of tokens consumed.
	TotalTokens uint32
	// CachedInputTokens is the total number of tokens read from cache.
	CachedInputTokens uint32
}

// sjsonOptions are the options used for sjson operations in the translator.
var sjsonOptions = &sjson.Options{
	Optimistic: true,
	// Note: DO NOT set ReplaceInPlace to true since at the translation layer, which might be called multiple times per retry,
	// it must be ensured that the original body is not modified, i.e. the operation must be idempotent.
	ReplaceInPlace: false,
}

// ImageGenerationTranslator translates the request and response messages between the client and the backend API schemas
// for /v1/images/generations endpoint of OpenAI.
//
// This is created per request and is not thread-safe.
type ImageGenerationTranslator interface {
	// RequestBody translates the request body.
	// 	- raw is the raw request body.
	// 	- body is the request body parsed into the OpenAI SDK [openaisdk.ImageGenerateParams].
	//	- forceBodyMutation is true if the translator should always mutate the body, even if no changes are made.
	//	- This returns headerMutation and bodyMutation that can be nil to indicate no mutation.
	RequestBody(raw []byte, body *openaisdk.ImageGenerateParams, forceBodyMutation bool) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		err error,
	)

	// ResponseHeaders translates the response headers.
	// 	- headers is the response headers.
	//	- This returns headerMutation that can be nil to indicate no mutation.
	ResponseHeaders(headers map[string]string) (
		headerMutation *extprocv3.HeaderMutation,
		err error,
	)

	// ResponseBody translates the response body.
	// 	- body is the response body.
	//	- This returns headerMutation and bodyMutation that can be nil to indicate no mutation.
	//  - This returns responseModel that is the model name from the response (may differ from request model).
	ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		tokenUsage LLMTokenUsage,
		responseModel internalapi.ResponseModel,
		err error,
	)

	// ResponseError translates the response error. This is called when the upstream response status code is not successful (2xx).
	// 	- respHeaders is the response headers.
	// 	- body is the response body that contains the error message.
	ResponseError(respHeaders map[string]string, body io.Reader) (headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error)
}
