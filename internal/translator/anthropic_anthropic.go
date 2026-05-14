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
	"log/slog"
	"strconv"
	"strings"

	"github.com/tidwall/sjson"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/redaction"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
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
	// Redaction configuration for debug logging
	debugLogEnabled bool
	enableRedaction bool
	logger          *slog.Logger
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
func (a *anthropicToAnthropicTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracingapi.MessageSpan) (
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

	// Redact and log response when enabled
	if a.debugLogEnabled && a.enableRedaction && a.logger != nil {
		redactedResp := a.RedactAnthropicBody(anthropicResp)
		if jsonBody, marshalErr := json.Marshal(redactedResp); marshalErr == nil {
			a.logger.Debug("response body processing", slog.Any("response", string(jsonBody)))
		}
	}

	usage := anthropicResp.Usage
	tokenUsage = metrics.ExtractTokenUsageFromExplicitCaching(
		int64(usage.InputTokens),
		int64(usage.OutputTokens),
		ptr.To(int64(usage.CacheReadInputTokens)),
		ptr.To(int64(usage.CacheCreationInputTokens)),
	)
	if span != nil {
		span.RecordResponse(anthropicResp)
	}
	responseModel = cmp.Or(anthropicResp.Model, a.requestModel)
	return nil, nil, tokenUsage, responseModel, nil
}

// extractUsageFromBufferEvent extracts the token usage from the buffered event.
// It scans complete lines and accumulates usage from all events in this batch.
func (a *anthropicToAnthropicTranslator) extractUsageFromBufferEvent(s tracingapi.MessageSpan) {
	for {
		i := bytes.IndexByte(a.buffered, '\n')
		if i == -1 {
			// Recalculate total tokens before returning
			a.updateTotalTokens()
			return
		}
		line := a.buffered[:i]
		a.buffered = a.buffered[i+1:]
		if !bytes.HasPrefix(line, sseDataPrefix) {
			continue
		}
		eventUnion := &anthropic.MessagesStreamChunk{}
		if err := json.Unmarshal(bytes.TrimPrefix(line, sseDataPrefix), eventUnion); err != nil {
			continue
		}
		if s != nil {
			s.RecordResponseChunk(eventUnion)
		}
		a.reflectStreamingEvent(eventUnion)
	}
}

func (a *anthropicToAnthropicTranslator) reflectStreamingEvent(eventUnion *anthropic.MessagesStreamChunk) {
	switch {
	case eventUnion.MessageStart != nil:
		message := eventUnion.MessageStart
		// Store the response model for future batches
		if message.Model != "" {
			a.streamingResponseModel = message.Model
		}
		// Extract usage from message_start event - this sets the baseline input tokens
		if u := message.Usage; u != nil {
			messageStartUsage := metrics.ExtractTokenUsageFromExplicitCaching(
				int64(u.InputTokens),
				int64(u.OutputTokens),
				ptr.To(int64(u.CacheReadInputTokens)),
				ptr.To(int64(u.CacheCreationInputTokens)),
			)
			// Override with message_start usage (contains input tokens and initial state)
			a.streamingTokenUsage.Override(messageStartUsage)
		}
	case eventUnion.MessageDelta != nil:
		u := eventUnion.MessageDelta.Usage
		// message_delta.usage is CUMULATIVE per Anthropic documentation
		// (see the comment on MessagesStreamChunkMessageDelta in the local
		// apischema). It carries the running totals for any of input_tokens,
		// cache_creation_input_tokens, cache_read_input_tokens, and
		// output_tokens; cache writes that happen mid-generation only
		// appear here, never in message_start. The previous implementation
		// only absorbed output_tokens, which silently undercounted
		// cache_creation_input_tokens by ~85% on Anthropic streaming
		// (Haiku 4.5: 91k truth vs 463 reported; Sonnet 4.6: 73k vs 10k).
		//
		// Anthropic's SDK models the cache fields as Optional[int] and
		// omits unchanged fields from the JSON body; Go unmarshals the
		// omissions to 0. Treat 0 as "not reported in this delta" so
		// we preserve the message_start values rather than clobbering
		// them with a zero. (Cumulative counters never decrease, so a
		// real 0 in a later delta would only happen when the field had
		// always been 0, in which case preserving 0 is equivalent.)
		// output_tokens: keep >= 0 so a message_delta that omits the entire
		// "usage" object (all zeroes after unmarshal) still marks output as
		// set — see TestAnthropicToGCPAnthropicTranslator_ResponseBody_StreamingEdgeCases.
		if u.OutputTokens >= 0 {
			a.streamingTokenUsage.SetOutputTokens(uint32(u.OutputTokens)) //nolint:gosec
		}

		oldGross, grossSet := a.streamingTokenUsage.InputTokens()
		oldCC, ccOK := a.streamingTokenUsage.CacheCreationInputTokens()
		oldCR, crOK := a.streamingTokenUsage.CachedInputTokens()
		var oldCCv, oldCRv uint32
		if ccOK {
			oldCCv = oldCC
		}
		if crOK {
			oldCRv = oldCR
		}

		cacheCreate := oldCCv
		if u.CacheCreationInputTokens > 0 {
			cacheCreate = uint32(u.CacheCreationInputTokens) //nolint:gosec
			a.streamingTokenUsage.SetCacheCreationInputTokens(cacheCreate)
		}
		cacheRead := oldCRv
		if u.CacheReadInputTokens > 0 {
			cacheRead = uint32(u.CacheReadInputTokens) //nolint:gosec
			a.streamingTokenUsage.SetCachedInputTokens(cacheRead)
		}

		switch {
		case u.InputTokens > 0:
			// u.InputTokens is the regular (non-cache) portion; gross stored
			// on streamingTokenUsage is regular + cache_create + cache_read.
			a.streamingTokenUsage.SetInputTokens(uint32(u.InputTokens) + cacheCreate + cacheRead) //nolint:gosec
		case grossSet && (u.CacheCreationInputTokens > 0 || u.CacheReadInputTokens > 0):
			// Delta updated cache counts but omitted input_tokens (JSON → 0).
			// Recover regular input from prior gross so we do not leave gross stale.
			regular := oldGross
			if sub := oldCCv + oldCRv; regular >= sub {
				regular -= sub
			}
			a.streamingTokenUsage.SetInputTokens(regular + cacheCreate + cacheRead) //nolint:gosec
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

	// Set cache_* tokens to 0 if not set but we have other token data so the
	// downstream histogram observation always emits an observation per token
	// type per request (RecordTokenUsage skips fields whose *_set flag is
	// false). This safety net used to fire for every Anthropic streaming
	// request because reflectStreamingEvent ignored the cache fields in
	// message_delta — a silent 0-fill that hid that bug for months. After
	// the message_delta fix above the safety net should only fire for
	// streams that completed without ever yielding a usage payload, which
	// itself indicates an upstream problem; emit a WARN so it is visible.
	if outputSet {
		if _, cachedSet := a.streamingTokenUsage.CachedInputTokens(); !cachedSet {
			if a.logger != nil {
				a.logger.Warn("anthropic streaming: cached_input_tokens missing at end of stream; defaulting to 0",
					slog.String("response_model", a.streamingResponseModel))
			}
			a.streamingTokenUsage.SetCachedInputTokens(0)
		}
		if _, cachedSet := a.streamingTokenUsage.CacheCreationInputTokens(); !cachedSet {
			if a.logger != nil {
				a.logger.Warn("anthropic streaming: cache_creation_input_tokens missing at end of stream; defaulting to 0",
					slog.String("response_model", a.streamingResponseModel))
			}
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

// SetRedactionConfig implements [AnthropicResponseRedactor.SetRedactionConfig].
func (a *anthropicToAnthropicTranslator) SetRedactionConfig(debugLogEnabled, enableRedaction bool, logger *slog.Logger) {
	a.debugLogEnabled = debugLogEnabled
	a.enableRedaction = enableRedaction
	a.logger = logger
}

// RedactAnthropicBody implements [AnthropicResponseRedactor.RedactAnthropicBody].
// Creates a redacted copy of the Anthropic response for safe logging without modifying the original.
func (a *anthropicToAnthropicTranslator) RedactAnthropicBody(resp *anthropic.MessagesResponse) *anthropic.MessagesResponse {
	if resp == nil {
		return nil
	}

	// Create a shallow copy of the response
	redacted := *resp

	// Redact content blocks (contains AI-generated content)
	if len(resp.Content) > 0 {
		redacted.Content = make([]anthropic.MessagesContentBlock, len(resp.Content))
		for i := range resp.Content {
			redacted.Content[i] = redactAnthropicContent(&resp.Content[i])
		}
	}

	return &redacted
}

// redactAnthropicContent redacts sensitive content from an Anthropic content block.
func redactAnthropicContent(content *anthropic.MessagesContentBlock) anthropic.MessagesContentBlock {
	redactedContent := *content

	// Redact text content
	if content.Text != nil {
		textCopy := *content.Text
		textCopy.Text = redaction.RedactString(content.Text.Text)
		redactedContent.Text = &textCopy
	}

	// Redact thinking content
	if content.Thinking != nil {
		thinkingCopy := *content.Thinking
		thinkingCopy.Thinking = redaction.RedactString(content.Thinking.Thinking)
		redactedContent.Thinking = &thinkingCopy
	}

	// Redact tool use input (may contain sensitive data)
	if content.Tool != nil {
		toolCopy := *content.Tool
		// For tool use, we redact by replacing the input with a placeholder
		toolCopy.Input = map[string]any{
			"redacted": redaction.RedactString(fmt.Sprintf("%v", content.Tool.Input)),
		}
		redactedContent.Tool = &toolCopy
	}

	// Note: tool_use_id and function names are metadata, not sensitive content

	return redactedContent
}
