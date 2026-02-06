// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"

	"github.com/envoyproxy/ai-gateway/internal/apischema/awsbedrock"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// NewChatCompletionOpenAIToAWSInvokeOpenAITranslator implements [Factory] for OpenAI to AWS InvokeModel OpenAI translations.
func NewChatCompletionOpenAIToAWSInvokeOpenAITranslator(modelNameOverride internalapi.ModelNameOverride) OpenAIChatCompletionTranslator {
	return &openAIToAWSInvokeOpenAITranslatorV1ChatCompletion{
		openAIToOpenAITranslatorV1ChatCompletion: openAIToOpenAITranslatorV1ChatCompletion{
			modelNameOverride: modelNameOverride,
		},
	}
}

// openAIToAWSInvokeOpenAITranslatorV1ChatCompletion adapts OpenAI requests for AWS Bedrock InvokeModel API.
// This uses the InvokeModel API which accepts model-specific request/response formats.
// For OpenAI models, this preserves the OpenAI format but uses AWS Bedrock endpoints.
type openAIToAWSInvokeOpenAITranslatorV1ChatCompletion struct {
	openAIToOpenAITranslatorV1ChatCompletion
	responseID string
}

func (o *openAIToAWSInvokeOpenAITranslatorV1ChatCompletion) RequestBody(raw []byte, req *openai.ChatCompletionRequest, _ bool) (
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
	// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModel.html#API_runtime_InvokeModel_RequestSyntax
	pathTemplate := "/model/%s/invoke"
	if req.Stream {
		// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_InvokeModelWithResponseStream.html#API_runtime_InvokeModelWithResponseStream_RequestSyntax
		pathTemplate = "/model/%s/invoke-with-response-stream"
	}

	// For InvokeModel API, the request body should be the OpenAI format
	if o.modelNameOverride != "" {
		// If we need to override the model in the request body
		var openAIReq openai.ChatCompletionRequest
		err = json.Unmarshal(raw, &openAIReq)
		if err != nil {
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
func (o *openAIToAWSInvokeOpenAITranslatorV1ChatCompletion) ResponseHeaders(headers map[string]string) (
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
// For streaming responses, AWS returns binary EventStream format with base64-encoded OpenAI JSON.
// This method decodes the EventStream and converts it to OpenAI SSE format to send to the client.
func (o *openAIToAWSInvokeOpenAITranslatorV1ChatCompletion) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool, span tracingapi.ChatCompletionSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	if o.stream {
		// Streaming: decode AWS EventStream format and convert to SSE
		var buf []byte
		buf, err = io.ReadAll(body)
		if err != nil {
			return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("failed to read streaming body: %w", err)
		}

		// Convert AWS EventStream to OpenAI SSE format and return it as newBody
		newBody, tokenUsage, err = o.convertEventStreamToSSE(buf, span)
		if err != nil {
			return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("failed to convert EventStream to SSE: %w", err)
		}

		if endOfStream && !strings.HasSuffix(string(newBody), "data: [DONE]\n\n") {
			newBody = append(newBody, []byte("data: [DONE]\n\n")...)
		}
		responseModel = o.requestModel
	} else {
		// Non-streaming: handle regular JSON response
		var openAIResp openai.ChatCompletionResponse
		if err = json.NewDecoder(body).Decode(&openAIResp); err != nil {
			return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("failed to decode response: %w", err)
		}

		// Extract token usage
		tokenUsage.SetInputTokens(uint32(openAIResp.Usage.PromptTokens))      //nolint:gosec
		tokenUsage.SetOutputTokens(uint32(openAIResp.Usage.CompletionTokens)) //nolint:gosec
		tokenUsage.SetTotalTokens(uint32(openAIResp.Usage.TotalTokens))       //nolint:gosec
		if openAIResp.Usage.PromptTokensDetails != nil {
			tokenUsage.SetCachedInputTokens(uint32(openAIResp.Usage.PromptTokensDetails.CachedTokens)) //nolint:gosec
		}

		// Fallback to request model for non-compliant backends
		responseModel = cmp.Or(openAIResp.Model, o.requestModel)

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

// convertEventStreamToSSE decodes AWS EventStream binary format and converts to OpenAI SSE format.
// AWS EventStream contains base64-encoded OpenAI JSON in a "bytes" field.
// Returns the SSE data and extracted token usage.
func (o *openAIToAWSInvokeOpenAITranslatorV1ChatCompletion) convertEventStreamToSSE(data []byte, span tracingapi.ChatCompletionSpan) ([]byte, metrics.TokenUsage, error) {
	var tokenUsage metrics.TokenUsage

	if len(data) == 0 {
		return nil, tokenUsage, nil
	}

	r := bytes.NewReader(data)
	dec := eventstream.NewDecoder()
	var result []byte
	var decodedBytes []byte

	for {
		msg, err := dec.Decode(r, nil)
		if err != nil {
			// End of stream or incomplete message
			break
		}

		// Parse the payload which contains {"bytes": "base64data", ...}
		var payload struct {
			Bytes string `json:"bytes"`
		}
		if json.Unmarshal(msg.Payload, &payload) != nil {
			continue
		}

		// Base64 decode the bytes field
		decodedBytes, err = base64.StdEncoding.DecodeString(payload.Bytes)
		if err != nil {
			continue
		}

		// Parse the chunk to extract usage information
		var chunk openai.ChatCompletionResponseChunk
		if json.Unmarshal(decodedBytes, &chunk) == nil {
			// Extract token usage if present
			if chunk.Usage != nil {
				tokenUsage.SetInputTokens(uint32(chunk.Usage.PromptTokens))      //nolint:gosec
				tokenUsage.SetOutputTokens(uint32(chunk.Usage.CompletionTokens)) //nolint:gosec
				tokenUsage.SetTotalTokens(uint32(chunk.Usage.TotalTokens))       //nolint:gosec
				if chunk.Usage.PromptTokensDetails != nil {
					tokenUsage.SetCachedInputTokens(uint32(chunk.Usage.PromptTokensDetails.CachedTokens)) //nolint:gosec
				}
			}

			// Record the chunk in the span if provided
			if span != nil {
				span.RecordResponseChunk(&chunk)
			}

			// Store the response model if present
			if chunk.Model != "" {
				o.streamingResponseModel = chunk.Model
			}
		}

		// Convert to SSE format: "data: <json>\n\n"
		result = append(result, []byte("data: ")...)
		result = append(result, decodedBytes...)
		result = append(result, []byte("\n\n")...)
	}

	return result, tokenUsage, nil
}

// ResponseError implements [OpenAIChatCompletionTranslator.ResponseError].
// Translates AWS Bedrock InvokeModel exceptions to OpenAI error format.
// The error type is typically stored in the "x-amzn-errortype" HTTP header for AWS error responses.
func (o *openAIToAWSInvokeOpenAITranslatorV1ChatCompletion) ResponseError(respHeaders map[string]string, body io.Reader) (
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
			var bedrockError awsbedrock.BedrockException
			if json.Unmarshal(buf, &bedrockError) == nil && bedrockError.Message != "" {
				openaiError = openai.Error{
					Type: "error",
					Error: openai.ErrorType{
						Type:    respHeaders[awsErrorTypeHeaderName],
						Message: bedrockError.Message,
						Code:    &statusCode,
					},
				}
			} else {
				// Generic AWS error format
				openaiError = openai.Error{
					Type: "error",
					Error: openai.ErrorType{
						Type:    awsBedrockBackendError,
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
				Type:    awsBedrockBackendError,
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
