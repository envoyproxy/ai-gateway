// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// NewChatCompletionOpenAIToAwsOpenAITranslator implements [Factory] for OpenAI to Aws OpenAI translations.
func NewChatCompletionOpenAIToAwsOpenAITranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) OpenAIChatCompletionTranslator {
	return &openAIToAwsOpenAITranslatorV1ChatCompletion{
		modelNameOverride: modelNameOverride,
	}
}

// openAIToAwsOpenAITranslatorV1ChatCompletion adapts OpenAI requests for AWS Bedrock InvokeModel API.
// This uses the InvokeModel API which accepts model-specific request/response formats.
// For OpenAI models, this preserves the OpenAI format but uses AWS Bedrock endpoints.
type openAIToAwsOpenAITranslatorV1ChatCompletion struct {
	openAIToOpenAITranslatorV1ChatCompletion
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
	responseID        string
	stream            bool
}

func (o *openAIToAwsOpenAITranslatorV1ChatCompletion) RequestBody(raw []byte, req *openai.ChatCompletionRequest, forceBodyMutation bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	// Store request model and streaming state
	o.requestModel = req.Model
	if o.modelNameOverride != "" {
		o.requestModel = o.modelNameOverride
	}

	if req.Stream {
		o.stream = true
	}

	// URL encode the model name for the path to handle special characters (e.g., ARNs)
	encodedModelName := url.PathEscape(o.requestModel)

	// Set the path for AWS Bedrock InvokeModel API
	pathTemplate := "/model/%s/invoke"
	if req.Stream {
		pathTemplate = "/model/%s/invoke-with-response-stream"
	}

	// For InvokeModel API, the request body should be the OpenAI format
	// since we're invoking OpenAI models through Bedrock
	if o.modelNameOverride != "" {
		// If we need to override the model in the request body
		var openAIReq openai.ChatCompletionRequest
		if err := json.Unmarshal(raw, &openAIReq); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal request: %w", err)
		}
		openAIReq.Model = o.modelNameOverride
		newBody, err = json.Marshal(openAIReq)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal request: %w", err)
		}
	} else {
		newBody = raw
	}

	newHeaders = []internalapi.Header{
		{pathHeaderName, fmt.Sprintf(pathTemplate, encodedModelName)},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}

	return
}

// ResponseHeaders implements [OpenAIChatCompletionTranslator.ResponseHeaders].
func (o *openAIToAwsOpenAITranslatorV1ChatCompletion) ResponseHeaders(headers map[string]string) (
	newHeaders []internalapi.Header, err error,
) {
	// Store the response ID for tracking
	o.responseID = headers["x-amzn-requestid"]

	// For streaming responses, ensure content-type is correctly set
	if o.stream {
		contentType := headers["content-type"]
		// AWS Bedrock might return different content-type for streaming
		if contentType == "application/vnd.amazon.eventstream" {
			// Convert to the expected streaming content-type
			newHeaders = []internalapi.Header{{contentTypeHeaderName, "text/event-stream"}}
		}
	}
	return
}

// ResponseBody implements [OpenAIChatCompletionTranslator.ResponseBody].
// AWS Bedrock InvokeModel API with OpenAI models returns responses in OpenAI format.
// This function handles both streaming and non-streaming responses.
func (o *openAIToAwsOpenAITranslatorV1ChatCompletion) ResponseBody(headers map[string]string, body io.Reader, endOfStream bool, span tracing.ChatCompletionSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	responseModel = o.requestModel

	if o.stream {
		// Handle streaming response
		var buf []byte
		buf, err = io.ReadAll(body)
		if err != nil {
			return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("failed to read streaming body: %w", err)
		}

		// For InvokeModel with OpenAI models, the streaming response should already be in
		// Server-Sent Events format with OpenAI chunks
		newBody = buf

		// Parse for token usage if available in the stream
		for _, line := range strings.Split(string(buf), "\n") {
			if dataStr, found := strings.CutPrefix(line, "data: "); found {
				if dataStr != "[DONE]" {
					var chunk openai.ChatCompletionResponseChunk
					if json.Unmarshal([]byte(dataStr), &chunk) == nil {
						if chunk.Usage != nil {
							tokenUsage.SetInputTokens(uint32(chunk.Usage.PromptTokens))
							tokenUsage.SetOutputTokens(uint32(chunk.Usage.CompletionTokens))
							tokenUsage.SetTotalTokens(uint32(chunk.Usage.TotalTokens))
						}
						if span != nil {
							span.RecordResponseChunk(&chunk)
						}
					}
				}
			}
		}

		if endOfStream && !strings.HasSuffix(string(newBody), "data: [DONE]\n") {
			newBody = append(newBody, []byte("data: [DONE]\n")...)
		}
	} else {
		// Handle non-streaming response
		var buf []byte
		buf, err = io.ReadAll(body)
		if err != nil {
			return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("failed to read body: %w", err)
		}

		// For InvokeModel with OpenAI models, response should already be in OpenAI format
		var openAIResp openai.ChatCompletionResponse
		if err = json.Unmarshal(buf, &openAIResp); err != nil {
			return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("failed to unmarshal response: %w", err)
		}

		// Use response model if available, otherwise use request model
		if openAIResp.Model != "" {
			responseModel = openAIResp.Model
		}

		// Extract token usage
		if openAIResp.Usage.TotalTokens > 0 {
			tokenUsage.SetInputTokens(uint32(openAIResp.Usage.PromptTokens))
			tokenUsage.SetOutputTokens(uint32(openAIResp.Usage.CompletionTokens))
			tokenUsage.SetTotalTokens(uint32(openAIResp.Usage.TotalTokens))
		}

		// Override the ID with AWS request ID if available
		if o.responseID != "" {
			openAIResp.ID = o.responseID
		}

		newBody, err = json.Marshal(openAIResp)
		if err != nil {
			return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("failed to marshal response: %w", err)
		}

		if span != nil {
			span.RecordResponse(&openAIResp)
		}
	}

	if len(newBody) > 0 {
		newHeaders = []internalapi.Header{{contentLengthHeaderName, strconv.Itoa(len(newBody))}}
	}
	return
}

// ResponseError implements [OpenAIChatCompletionTranslator.ResponseError].
// Translates AWS Bedrock InvokeModel exceptions to OpenAI error format.
// The error type is typically stored in the "x-amzn-errortype" HTTP header for AWS error responses.
func (o *openAIToAwsOpenAITranslatorV1ChatCompletion) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	var openaiError openai.Error

	// Check if we have a JSON error response
	if v, ok := respHeaders[contentTypeHeaderName]; ok && strings.Contains(v, jsonContentType) {
		// Try to parse as AWS Bedrock error
		var buf []byte
		buf, err = io.ReadAll(body)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read error body: %w", err)
		}

		// Check if it's already an OpenAI error format
		var existingOpenAIError openai.Error
		if json.Unmarshal(buf, &existingOpenAIError) == nil && existingOpenAIError.Error.Message != "" {
			// Already in OpenAI format, return as-is
			newBody = buf
		} else {
			// Try to parse as AWS error and convert to OpenAI format
			var awsError struct {
				Type    string `json:"__type,omitempty"`
				Message string `json:"message"`
				Code    string `json:"code,omitempty"`
			}
			if json.Unmarshal(buf, &awsError) == nil && awsError.Message != "" {
				openaiError = openai.Error{
					Type: "error",
					Error: openai.ErrorType{
						Type:    respHeaders[awsErrorTypeHeaderName],
						Message: awsError.Message,
						Code:    &statusCode,
					},
				}
			} else {
				// Generic AWS error format
				openaiError = openai.Error{
					Type: "error",
					Error: openai.ErrorType{
						Type:    awsInvokeModelBackendError,
						Message: string(buf),
						Code:    &statusCode,
					},
				}
			}
			newBody, err = json.Marshal(openaiError)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal error body: %w", err)
			}
		}
	} else {
		// Non-JSON error response
		var buf []byte
		buf, err = io.ReadAll(body)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read error body: %w", err)
		}
		openaiError = openai.Error{
			Type: "error",
			Error: openai.ErrorType{
				Type:    awsInvokeModelBackendError,
				Message: string(buf),
				Code:    &statusCode,
			},
		}
		newBody, err = json.Marshal(openaiError)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal error body: %w", err)
		}
	}

	newHeaders = []internalapi.Header{
		{contentTypeHeaderName, jsonContentType},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}
