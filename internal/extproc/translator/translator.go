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

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

const (
	statusHeaderName       = ":status"
	contentTypeHeaderName  = "content-type"
	awsErrorTypeHeaderName = "x-amzn-errortype"
	jsonContentType        = "application/json"
	openAIBackendError     = "OpenAIBackendError"
	awsBedrockBackendError = "AWSBedrockBackendError"
)

// isGoodStatusCode checks if the HTTP status code of the upstream response is successful.
// The 2xx - Successful: The request is received by upstream and processed successfully.
// https://developer.mozilla.org/en-US/docs/Web/HTTP/Status#successful_responses
func isGoodStatusCode(code int) bool {
	return code >= 200 && code < 300
}

// OpenAIChatCompletionTranslator translates the request and response messages between the client and the backend API schemas
// for /v1/chat/completion endpoint of OpenAI.
//
// This is created per request and is not thread-safe.
type OpenAIChatCompletionTranslator interface {
	// RequestBody translates the request body.
	// 	- `raw` is the raw request body.
	// 	- `body` is the request body parsed into the [openai.ChatCompletionRequest].
	//	- `onRetry` is true if this is a retry request.
	//	- This returns `headerMutation` and `bodyMutation` that can be nil to indicate no mutation.
	RequestBody(raw []byte, body *openai.ChatCompletionRequest, onRetry bool) (
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
	ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		tokenUsage LLMTokenUsage,
		err error,
	)
}

func setContentLength(headers *extprocv3.HeaderMutation, body []byte) {
	headers.SetHeaders = append(headers.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:      "content-length",
			RawValue: []byte(fmt.Sprintf("%d", len(body))),
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
	ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool) (
		headerMutation *extprocv3.HeaderMutation,
		bodyMutation *extprocv3.BodyMutation,
		tokenUsage LLMTokenUsage,
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
}
