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

	"github.com/anthropics/anthropic-sdk-go"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/tidwall/sjson"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
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
func (a *anthropicToAnthropicTranslator) RequestBody(original []byte, body *anthropicschema.MessagesRequest, forceBodyMutation bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	a.stream = body.Stream
	// Store the request model to use as fallback for response model
	a.requestModel = body.Model
	var newBody []byte
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

	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{
				Key:      ":path",
				RawValue: []byte("/v1/messages"),
			}},
		},
	}

	if len(newBody) > 0 {
		bodyMutation = &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: newBody},
		}
		headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{Header: &corev3.HeaderValue{
			Key:      "content-length",
			RawValue: []byte(strconv.Itoa(len(newBody))),
		}})
	}
	return
}

// ResponseHeaders implements [AnthropicMessagesTranslator.ResponseHeaders].
func (a *anthropicToAnthropicTranslator) ResponseHeaders(_ map[string]string) (
	headerMutation *extprocv3.HeaderMutation, err error,
) {
	return nil, nil
}

// ResponseBody implements [AnthropicMessagesTranslator.ResponseBody].
func (a *anthropicToAnthropicTranslator) ResponseBody(_ map[string]string, body io.Reader, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, responseModel string, err error,
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
	anthropicResp := &anthropic.Message{}
	if err := json.NewDecoder(body).Decode(anthropicResp); err != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	tokenUsage = LLMTokenUsage{
		InputTokens:       uint32(anthropicResp.Usage.InputTokens),                                    //nolint:gosec
		OutputTokens:      uint32(anthropicResp.Usage.OutputTokens),                                   //nolint:gosec
		TotalTokens:       uint32(anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens), //nolint:gosec
		CachedInputTokens: uint32(anthropicResp.Usage.CacheReadInputTokens),                           //nolint:gosec
	}
	responseModel = cmp.Or(internalapi.ResponseModel(anthropicResp.Model), a.requestModel)
	return nil, nil, tokenUsage, responseModel, nil
}

// extractUsageFromBufferEvent extracts the token usage from the buffered event.
// It scans complete lines and returns the latest usage found in this batch.
func (a *anthropicToAnthropicTranslator) extractUsageFromBufferEvent() (tokenUsage LLMTokenUsage) {
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
		eventUnion := &anthropic.MessageStreamEventUnion{}
		if err := json.Unmarshal(bytes.TrimPrefix(line, dataPrefix), eventUnion); err != nil {
			continue
		}

		// See the code in MessageStreamEventUnion.AsAny for reference.
		switch eventUnion.Type {
		case "message_start":
			// Message only valid in message_start events.
			if eventUnion.Message.Model != "" {
				// Store the response model for future batches
				a.streamingResponseModel = internalapi.ResponseModel(eventUnion.Message.Model)
			}
		case "message_delta":
			// Usage only valid in message_delta events.
			usage := &eventUnion.Usage
			tokenUsage.InputTokens = uint32(usage.InputTokens)                      //nolint:gosec
			tokenUsage.OutputTokens = uint32(usage.OutputTokens)                    //nolint:gosec
			tokenUsage.TotalTokens = uint32(usage.InputTokens + usage.OutputTokens) //nolint:gosec
			tokenUsage.CachedInputTokens = uint32(usage.CacheReadInputTokens)       //nolint:gosec
		}
	}
}
