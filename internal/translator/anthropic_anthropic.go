// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
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
func (a *anthropicToAnthropicTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool, _ tracing.MessageSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	if a.stream {
		var buf []byte
		buf, err = io.ReadAll(body)
		if err != nil {
			return nil, nil, tokenUsage, a.requestModel, fmt.Errorf("failed to read body: %w", err)
		}
		a.buffered = append(a.buffered, buf...)
		tokenUsage = a.extractUsageFromBufferEvent()
		// Use stored streaming response model, fallback to request model for non-compliant backends
		responseModel = cmp.Or(a.streamingResponseModel, a.requestModel)
		return
	}

	// Parse the Anthropic response to extract token usage.
	anthropicResp := &anthropic.MessagesResponse{}
	if err := json.NewDecoder(body).Decode(anthropicResp); err != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	usage := anthropicResp.Usage
	tokenUsage = extractTokenUsageFromAnthropic(
		int64(usage.InputTokens),
		int64(usage.OutputTokens),
		int64(usage.CacheReadInputTokens),
		int64(usage.CacheCreationInputTokens),
	)

	responseModel = cmp.Or(internalapi.ResponseModel(anthropicResp.Model), a.requestModel)
	return nil, nil, tokenUsage, responseModel, nil
}

// extractUsageFromBufferEvent extracts the token usage from the buffered event.
// It scans complete lines and returns the latest usage found in this batch.
func (a *anthropicToAnthropicTranslator) extractUsageFromBufferEvent() (tokenUsage metrics.TokenUsage) {
	for {
		i := bytes.IndexByte(a.buffered, '\n')
		if i == -1 {
			return
		}
		line := a.buffered[:i]
		a.buffered = a.buffered[i+1:]
		if !bytes.HasPrefix(line, dataPrefix) {
			continue
		}
		eventUnion := &anthropic.MessagesStreamEvent{}
		if err := json.Unmarshal(bytes.TrimPrefix(line, dataPrefix), eventUnion); err != nil {
			continue
		}

		// See the code in MessageStreamEventUnion.AsAny for reference.
		switch eventUnion.Type {
		case "message_start":
			message := eventUnion.MessageStart
			// Message only valid in message_start events.
			if message.Model != "" {
				// Store the response model for future batches
				a.streamingResponseModel = internalapi.ResponseModel(message.Model)
			}
			// Extract usage from message_start event
			u := message.Usage
			tokenUsage = extractTokenUsageFromAnthropic(
				int64(u.InputTokens),
				int64(u.OutputTokens),
				int64(u.CacheReadInputTokens),
				int64(u.CacheCreationInputTokens),
			)
		case "message_delta":
			u := eventUnion.MessageDelta.Usage
			tokenUsage = extractTokenUsageFromAnthropic(
				int64(u.InputTokens),
				int64(u.OutputTokens),
				int64(u.CacheReadInputTokens),
				int64(u.CacheCreationInputTokens),
			)
		}
	}
}

// ResponseError implements [AnthropicMessagesTranslator] for Anthropic to AWS Bedrock Anthropic translation.
func (a *anthropicToAnthropicTranslator) ResponseError(map[string]string, io.Reader) (
	newHeaders []internalapi.Header,
	mutatedBody []byte,
	err error,
) {
	// TODO: implement the non-anthropic error conversion logic here. For now, we just return the original error
	// 	from the upstream as-is.
	return
}
