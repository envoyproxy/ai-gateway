// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// NewAnthropicToAnthropicTranslator creates a passthrough translator for Anthropic.
func NewAnthropicToAnthropicTranslator(version string, modelNameOverride internalapi.ModelNameOverride) AnthropicMessagesTranslator {
	// TODO: use "version" in APISchema struct to set the specific prefix if needed like OpenAI does. However, two questions:
	// 	* Is there any "Anthropic compatible" API that uses a different prefix like OpenAI does?
	// 	* Even if there is, we should refactor the APISchema struct to have "prefix" field instead of abusing "version" field.
	_ = version
	return &anthropicToAnthropicTranslator{modelNameOverride: modelNameOverride}
}

type anthropicToAnthropicTranslator struct {
	modelNameOverride      internalapi.ModelNameOverride
	requestModel           internalapi.RequestModel
	stream                 bool
	buffered               []byte
	streamingResponseModel internalapi.ResponseModel
	streamingTokenUsage    metrics.TokenUsage
}

// RequestBody implements [AnthropicMessagesTranslator.RequestBody].
func (a *anthropicToAnthropicTranslator) RequestBody(original []byte, body *anthropic.MessagesRequest, forceBodyMutation bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	a.stream = body.Stream
	// Store the request model to use as fallback for response model
	a.requestModel = body.Model
	if a.modelNameOverride != "" {
		// If modelName is set we override the model to be used for the request.
		newBody, err = sjson.SetBytesOptions(original, "model", a.modelNameOverride, sjsonOptions)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set model name: %w", err)
		}
		// Make everything coherent.
		a.requestModel = a.modelNameOverride
	}

	if forceBodyMutation && len(newBody) == 0 {
		newBody = original
	}

	newHeaders = []internalapi.Header{{pathHeaderName, "/v1/messages"}}
	if len(newBody) > 0 {
		newHeaders = append(newHeaders, internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(newBody))})
	}
	return
}

// ResponseHeaders implements [AnthropicMessagesTranslator.ResponseHeaders].
func (a *anthropicToAnthropicTranslator) ResponseHeaders(_ map[string]string) (
	newHeaders []internalapi.Header, err error,
) {
	return nil, nil
}

// ResponseBody implements [AnthropicMessagesTranslator.ResponseBody].
func (a *anthropicToAnthropicTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracing.MessageSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	if a.stream {
		var buf []byte
		buf, err = io.ReadAll(body)
		if err != nil {
			return nil, nil, tokenUsage, a.requestModel, fmt.Errorf("failed to read body: %w", err)
		}

		a.buffered = append(a.buffered, buf...)
		a.extractUsageFromBufferEvent(span)
		// Use stored streaming response model, fallback to request model for non-compliant backends
		responseModel = cmp.Or(a.streamingResponseModel, a.requestModel)
		return nil, nil, a.streamingTokenUsage, responseModel, nil
	}

	// Parse the Anthropic response to extract token usage.
	anthropicResp := &anthropic.MessagesResponse{}
	if err := json.NewDecoder(body).Decode(anthropicResp); err != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	usage := anthropicResp.Usage
	tokenUsage = metrics.ExtractTokenUsageFromAnthropic(
		int64(usage.InputTokens),
		int64(usage.OutputTokens),
		int64(usage.CacheReadInputTokens),
		int64(usage.CacheCreationInputTokens),
	)
	if span != nil {
		span.RecordResponse(anthropicResp)
	}
	responseModel = cmp.Or(anthropicResp.Model, a.requestModel)
	return nil, nil, tokenUsage, responseModel, nil
}

// extractUsageFromBufferEvent extracts the token usage from the buffered event.
// It scans complete lines and accumulates usage from all events in this batch.
func (a *anthropicToAnthropicTranslator) extractUsageFromBufferEvent(s tracing.MessageSpan) {
	for {
		i := bytes.IndexByte(a.buffered, '\n')
		if i == -1 {
			// Recalculate total tokens before returning
			a.updateTotalTokens()
			return
		}
		line := a.buffered[:i]
		a.buffered = a.buffered[i+1:]
		if !bytes.HasPrefix(line, dataPrefix) {
			continue
		}
		eventUnion := &anthropic.MessagesStreamChunk{}
		if err := json.Unmarshal(bytes.TrimPrefix(line, dataPrefix), eventUnion); err != nil {
			continue
		}
		if s != nil {
			s.RecordResponseChunk(eventUnion)
		}

		switch {
		case eventUnion.MessageStart != nil:
			message := eventUnion.MessageStart
			// Store the response model for future batches
			if message.Model != "" {
				a.streamingResponseModel = message.Model
			}
			// Extract usage from message_start event - this sets the baseline input tokens
			if u := message.Usage; u != nil {
				messageStartUsage := metrics.ExtractTokenUsageFromAnthropic(
					int64(u.InputTokens),
					int64(u.OutputTokens),
					int64(u.CacheReadInputTokens),
					int64(u.CacheCreationInputTokens),
				)
				// Override with message_start usage (contains input tokens and initial state)
				a.streamingTokenUsage.Override(messageStartUsage)
			}
		case eventUnion.MessageDelta != nil:
			u := eventUnion.MessageDelta.Usage
			// message_delta events provide final counts for specific token types
			// Update output tokens from message_delta (final count)
			if u.OutputTokens >= 0 {
				a.streamingTokenUsage.SetOutputTokens(uint32(u.OutputTokens)) //nolint:gosec
			}
		}
	}
}

// updateTotalTokens recalculates and sets the total token count
func (a *anthropicToAnthropicTranslator) updateTotalTokens() {
	inputTokens, inputSet := a.streamingTokenUsage.InputTokens()
	outputTokens, outputSet := a.streamingTokenUsage.OutputTokens()

	// Initialize missing values to 0 if we have any token data
	if outputSet && !inputSet {
		a.streamingTokenUsage.SetInputTokens(0)
		inputTokens = 0
		inputSet = true
	}

	// Set cached tokens to 0 if not set but we have other token data
	if outputSet {
		if _, cachedSet := a.streamingTokenUsage.CachedInputTokens(); !cachedSet {
			a.streamingTokenUsage.SetCachedInputTokens(0)
		}
		if _, cachedSet := a.streamingTokenUsage.CacheCreationInputTokens(); !cachedSet {
			a.streamingTokenUsage.SetCacheCreationInputTokens(0)
		}
	}

	if inputSet && outputSet {
		a.streamingTokenUsage.SetTotalTokens(inputTokens + outputTokens)
	}
}

// ResponseError implements [AnthropicMessagesTranslator] for Anthropic to AWS Bedrock Anthropic translation.
func (a *anthropicToAnthropicTranslator) ResponseError(respHeaders map[string]string, r io.Reader) (
	newHeaders []internalapi.Header,
	mutatedBody []byte,
	err error,
) {
	statusCode := respHeaders[statusHeaderName]
	if !strings.Contains(respHeaders[contentTypeHeaderName], jsonContentType) {
		buf, err := io.ReadAll(r)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read error body: %w", err)
		}
		var typ string
		switch statusCode {
		case "400":
			typ = "invalid_request_error"
		case "401":
			typ = "authentication_error"
		case "403":
			typ = "permission_error"
		case "404":
			typ = "not_found_error"
		case "413":
			typ = "request_too_large"
		case "429":
			typ = "rate_limit_error"
		case "500":
			typ = "internal_server_error"
		case "503":
			typ = "service_unavailable_error"
		case "529":
			typ = "overloaded_error"
		default:
			typ = "internal_server_error"
		}
		anthropicError := anthropic.ErrorResponse{
			Type:  "error", // Always "error" at the top level.
			Error: anthropic.ErrorResponseMessage{Type: typ, Message: string(buf)},
		}
		mutatedBody, err = json.Marshal(anthropicError)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to marshal error body: %w", err)
		}
		newHeaders = append(newHeaders,
			internalapi.Header{contentTypeHeaderName, jsonContentType},
			internalapi.Header{contentLengthHeaderName, strconv.Itoa(len(mutatedBody))},
		)
	}
	return
}
