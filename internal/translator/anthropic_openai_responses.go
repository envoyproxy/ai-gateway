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
	"log/slog"
	"path"
	"strconv"
	"strings"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// NewAnthropicToResponsesOpenAITranslator translates Anthropic Messages
// requests and OpenAI Responses output in both streaming and non-streaming
// modes.
func NewAnthropicToResponsesOpenAITranslator(prefix string, modelNameOverride internalapi.ModelNameOverride) AnthropicMessagesTranslator {
	return &anthropicToOpenAIResponsesTranslator{
		modelNameOverride: modelNameOverride,
		path:              path.Join("/", prefix, "responses"),
		errorTranslator:   NewAnthropicToChatCompletionOpenAITranslator(prefix, modelNameOverride),
	}
}

type anthropicToOpenAIResponsesTranslator struct {
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
	path              string
	stream            bool
	streamBuffer      bytes.Buffer
	streamState       *openAIStreamToAnthropicState
	streamedArguments map[string]bool
	streamedReasoning map[string]bool
	errorTranslator   AnthropicMessagesTranslator
	debugLogEnabled   bool
	enableRedaction   bool
	logger            *slog.Logger
}

func (a *anthropicToOpenAIResponsesTranslator) RequestBody(_ []byte, body *anthropic.MessagesRequest, _ bool) ([]internalapi.Header, []byte, error) {
	a.requestModel = cmp.Or(a.modelNameOverride, body.Model)
	a.stream = body.Stream

	requestBody, err := buildOpenAIResponsesRequest(body, a.modelNameOverride)
	if err != nil {
		return nil, nil, err
	}
	if a.stream {
		a.streamState = &openAIStreamToAnthropicState{
			activeTools:  make(map[int64]*streamToolCall),
			requestModel: a.requestModel,
		}
		a.streamedArguments = make(map[string]bool)
		a.streamedReasoning = make(map[string]bool)
	}
	return []internalapi.Header{
		{pathHeaderName, a.path},
		{contentLengthHeaderName, strconv.Itoa(len(requestBody))},
	}, requestBody, nil
}

func buildOpenAIResponsesRequest(body *anthropic.MessagesRequest, modelNameOverride internalapi.ModelNameOverride) ([]byte, error) {
	chatRequest := buildOpenAIChatCompletionRequest(body, modelNameOverride)
	chatJSON, err := json.Marshal(chatRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal intermediate OpenAI request: %w", err)
	}
	var chat map[string]any
	if err = json.Unmarshal(chatJSON, &chat); err != nil {
		return nil, fmt.Errorf("failed to decode intermediate OpenAI request: %w", err)
	}

	input, err := chatMessagesToResponsesInput(chat["messages"])
	if err != nil {
		return nil, err
	}
	request := map[string]any{
		"model":             cmp.Or(modelNameOverride, body.Model),
		"input":             input,
		"include":           []string{"reasoning.encrypted_content"},
		"max_output_tokens": body.MaxTokens,
		"store":             false,
	}
	if body.Stream {
		request["stream"] = true
	}
	if body.Temperature != nil {
		request["temperature"] = *body.Temperature
	}
	if body.TopP != nil {
		request["top_p"] = *body.TopP
	}
	if len(body.StopSequences) > 0 {
		return nil, fmt.Errorf("OpenAI Responses API does not support Anthropic stop_sequences")
	}
	if tools, ok := chat["tools"].([]any); ok && len(tools) > 0 {
		request["tools"] = chatToolsToResponsesTools(tools)
	}
	if choice, ok := chat["tool_choice"]; ok {
		request["tool_choice"] = chatToolChoiceToResponsesToolChoice(choice)
	}
	if parallel := anthropicParallelToolCalls(body.ToolChoice); parallel != nil {
		request["parallel_tool_calls"] = *parallel
	}
	if body.Thinking != nil {
		switch {
		case body.Thinking.Disabled != nil:
			request["reasoning"] = map[string]any{"effort": "none"}
		case body.Thinking.Enabled != nil:
			request["reasoning"] = map[string]any{"effort": "high"}
		case body.Thinking.Adaptive != nil:
			request["reasoning"] = map[string]any{"effort": "medium"}
		}
	}
	result, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal OpenAI Responses request: %w", err)
	}
	return result, nil
}

func chatMessagesToResponsesInput(value any) ([]any, error) {
	messages, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("intermediate OpenAI messages must be an array")
	}
	input := make([]any, 0, len(messages))
	for _, raw := range messages {
		message, ok := raw.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("intermediate OpenAI message must be an object")
		}
		role, _ := message["role"].(string)
		if role == "tool" {
			callID, _ := message["tool_call_id"].(string)
			input = append(input, map[string]any{
				"type": "function_call_output", "call_id": callID,
				"output": responseToolOutput(message["content"]),
			})
			continue
		}
		if content, present := message["content"]; present && content != nil {
			var converted any
			if role == "assistant" {
				var reasoning []any
				reasoning, converted = chatAssistantContentToResponsesContent(content)
				input = append(input, reasoning...)
			} else {
				converted = chatContentToResponsesContent(content)
			}
			if !emptyResponseContent(converted) {
				input = append(input, map[string]any{"role": role, "content": converted})
			}
		}
		if role == "assistant" {
			if toolCalls, ok := message["tool_calls"].([]any); ok {
				for _, rawToolCall := range toolCalls {
					toolCall, _ := rawToolCall.(map[string]any)
					function, _ := toolCall["function"].(map[string]any)
					input = append(input, map[string]any{
						"type": "function_call", "call_id": toolCall["id"],
						"name": function["name"], "arguments": function["arguments"],
					})
				}
			}
		}
	}
	return input, nil
}

const openAIReasoningEnvelopePrefix = "openai-reasoning-v1:"

func encodeOpenAIReasoningItem(item *openai.ResponseReasoningItem) (string, error) {
	reasoning := map[string]any{
		"id": item.ID, "type": cmp.Or(item.Type, "reasoning"), "summary": item.Summary,
	}
	if item.EncryptedContent != "" {
		reasoning["encrypted_content"] = item.EncryptedContent
	}
	if item.Content != nil {
		reasoning["content"] = item.Content
	}
	if item.Status != "" {
		reasoning["status"] = item.Status
	}
	encoded, err := json.Marshal(reasoning)
	if err != nil {
		return "", fmt.Errorf("failed to marshal OpenAI reasoning item: %w", err)
	}
	payload := append([]byte(openAIReasoningEnvelopePrefix), encoded...)
	return base64.RawStdEncoding.EncodeToString(payload), nil
}

func decodeOpenAIReasoningItem(value any) (map[string]any, bool) {
	encoded, ok := value.(string)
	if !ok {
		return nil, false
	}
	raw, err := base64.RawStdEncoding.DecodeString(encoded)
	prefix := []byte(openAIReasoningEnvelopePrefix)
	if err != nil || !bytes.HasPrefix(raw, prefix) {
		return nil, false
	}
	var item map[string]any
	if err = json.Unmarshal(raw[len(prefix):], &item); err != nil || item["type"] != "reasoning" {
		return nil, false
	}
	return item, true
}

func chatAssistantContentToResponsesContent(content any) (reasoning []any, visible any) {
	parts, ok := content.([]any)
	if !ok {
		return nil, content
	}
	visibleParts := make([]any, 0, len(parts))
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		var envelope any
		switch part["type"] {
		case "thinking":
			envelope = part["signature"]
		case "redacted_thinking":
			envelope = part["redactedContent"]
		}
		if item, decoded := decodeOpenAIReasoningItem(envelope); decoded {
			reasoning = append(reasoning, item)
			continue
		}
		if part["type"] == "text" {
			visibleParts = append(visibleParts, map[string]any{"type": "input_text", "text": part["text"]})
		}
	}
	return reasoning, visibleParts
}

func chatContentToResponsesContent(content any) any {
	parts, ok := content.([]any)
	if !ok {
		return content
	}
	converted := make([]any, 0, len(parts))
	for _, raw := range parts {
		part, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch part["type"] {
		case "text":
			converted = append(converted, map[string]any{"type": "input_text", "text": part["text"]})
		case "image_url":
			image, _ := part["image_url"].(map[string]any)
			converted = append(converted, map[string]any{"type": "input_image", "image_url": image["url"]})
		}
	}
	return converted
}

func emptyResponseContent(content any) bool {
	switch value := content.(type) {
	case string:
		return value == ""
	case []any:
		return len(value) == 0
	default:
		return content == nil
	}
}

func responseToolOutput(content any) string {
	if text, ok := content.(string); ok {
		return text
	}
	encoded, err := json.Marshal(content)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func chatToolsToResponsesTools(tools []any) []any {
	result := make([]any, 0, len(tools))
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		function, _ := tool["function"].(map[string]any)
		flattened := map[string]any{"type": "function", "name": function["name"]}
		for _, key := range []string{"description", "parameters", "strict"} {
			if value, ok := function[key]; ok {
				flattened[key] = value
			}
		}
		result = append(result, flattened)
	}
	return result
}

func chatToolChoiceToResponsesToolChoice(choice any) any {
	object, ok := choice.(map[string]any)
	if !ok {
		return choice
	}
	function, _ := object["function"].(map[string]any)
	return map[string]any{"type": "function", "name": function["name"]}
}

func anthropicParallelToolCalls(choice *anthropic.ToolChoice) *bool {
	if choice == nil {
		return nil
	}
	var disabled *bool
	switch {
	case choice.Auto != nil:
		disabled = choice.Auto.DisableParallelToolUse
	case choice.Any != nil:
		disabled = choice.Any.DisableParallelToolUse
	case choice.Tool != nil:
		disabled = choice.Tool.DisableParallelToolUse
	}
	if disabled == nil {
		return nil
	}
	parallel := !*disabled
	return &parallel
}

func (a *anthropicToOpenAIResponsesTranslator) ResponseHeaders(map[string]string) ([]internalapi.Header, error) {
	return nil, nil
}

func (a *anthropicToOpenAIResponsesTranslator) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool, span tracingapi.MessageSpan) ([]internalapi.Header, []byte, metrics.TokenUsage, string, error) {
	if a.stream {
		return a.responseBodyStreaming(body, endOfStream)
	}
	return a.responseBodyNonStreaming(body, span)
}

func (a *anthropicToOpenAIResponsesTranslator) responseBodyNonStreaming(body io.Reader, span tracingapi.MessageSpan) ([]internalapi.Header, []byte, metrics.TokenUsage, string, error) {
	var response openai.Response
	var tokenUsage metrics.TokenUsage
	responseModel := a.requestModel
	if err := json.NewDecoder(body).Decode(&response); err != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to unmarshal OpenAI Responses response: %w", err)
	}
	responseModel = cmp.Or(response.Model, a.requestModel)
	chatResponse, err := openAIResponsesToChatCompletion(&response, responseModel)
	if err != nil {
		return nil, nil, tokenUsage, responseModel, err
	}
	anthropicResponse := openAIResponseToAnthropic(chatResponse, responseModel)
	setAnthropicUsageFromOpenAIResponse(anthropicResponse, &response, &tokenUsage)

	if a.debugLogEnabled && a.enableRedaction && a.logger != nil {
		if encoded, marshalErr := json.Marshal(a.RedactAnthropicBody(anthropicResponse)); marshalErr == nil {
			a.logger.Debug("response body processing", slog.Any("response", string(encoded)))
		}
	}
	if span != nil {
		span.RecordResponse(anthropicResponse)
	}
	newBody, err := json.Marshal(anthropicResponse)
	if err != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to marshal Anthropic response: %w", err)
	}
	return []internalapi.Header{{contentLengthHeaderName, strconv.Itoa(len(newBody))}}, newBody, tokenUsage, responseModel, nil
}

func openAIResponsesToChatCompletion(response *openai.Response, model string) (*openai.ChatCompletionResponse, error) {
	message := openai.ChatCompletionResponseChoiceMessage{Role: "assistant"}
	var text strings.Builder
	finishReason := openai.ChatCompletionChoicesFinishReasonStop
	for i := range response.Output {
		item := &response.Output[i]
		switch {
		case item.OfReasoning != nil:
			envelope, err := encodeOpenAIReasoningItem(item.OfReasoning)
			if err != nil {
				return nil, err
			}
			message.ThinkingBlocks = append(message.ThinkingBlocks, openai.ThinkingBlock{
				Type: "redacted_thinking", Data: envelope,
			})
		case item.OfOutputMessage != nil:
			content := item.OfOutputMessage.Content
			if content.OfString != nil {
				text.WriteString(*content.OfString)
			}
			for _, part := range content.OfContentArray {
				if part.OfOutputText != nil {
					text.WriteString(part.OfOutputText.Text)
				} else if part.OfRefusal != nil {
					text.WriteString(part.OfRefusal.Refusal)
					finishReason = openai.ChatCompletionChoicesFinishReasonContentFilter
				}
			}
		case item.OfFunctionCall != nil:
			callID := item.OfFunctionCall.CallID
			message.ToolCalls = append(message.ToolCalls, openai.ChatCompletionMessageToolCallParam{
				ID: &callID,
				Function: openai.ChatCompletionMessageToolCallFunctionParam{
					Name: item.OfFunctionCall.Name, Arguments: item.OfFunctionCall.Arguments,
				},
			})
			finishReason = openai.ChatCompletionChoicesFinishReasonToolCalls
		}
	}
	if text.Len() > 0 {
		value := text.String()
		message.Content = &value
	}
	switch response.IncompleteDetails.Reason {
	case "max_output_tokens":
		finishReason = openai.ChatCompletionChoicesFinishReasonLength
	case "content_filter":
		finishReason = openai.ChatCompletionChoicesFinishReasonContentFilter
	}
	usage := openai.Usage{}
	if response.Usage != nil {
		usage.PromptTokens = int(response.Usage.InputTokens)
		usage.CompletionTokens = int(response.Usage.OutputTokens)
		usage.TotalTokens = int(response.Usage.TotalTokens)
	}
	return &openai.ChatCompletionResponse{
		ID: response.ID, Model: model, Usage: usage,
		Choices: []openai.ChatCompletionResponseChoice{{Message: message, FinishReason: finishReason}},
	}, nil
}

func setAnthropicUsageFromOpenAIResponse(anthropicResponse *anthropic.MessagesResponse, response *openai.Response, tokenUsage *metrics.TokenUsage) {
	if response.Usage == nil {
		return
	}
	usage := response.Usage
	cacheCreationTokens := responseCacheCreationTokens(usage)
	uncachedInputTokens := usage.InputTokens - usage.InputTokensDetails.CachedTokens - cacheCreationTokens
	if uncachedInputTokens < 0 {
		uncachedInputTokens = 0
	}
	anthropicResponse.Usage = &anthropic.Usage{
		InputTokens:              float64(uncachedInputTokens),
		OutputTokens:             float64(usage.OutputTokens),
		CacheReadInputTokens:     float64(usage.InputTokensDetails.CachedTokens),
		CacheCreationInputTokens: float64(cacheCreationTokens),
	}
	setTokenUsageFromResponse(tokenUsage, response)
	if cacheCreationTokens > 0 {
		tokenUsage.SetCacheCreationInputTokens(uint32(cacheCreationTokens)) // #nosec G115
	}
}

func responseCacheCreationTokens(usage *openai.ResponseUsage) int64 {
	if usage.InputTokensDetails.CacheCreationTokens != 0 {
		return usage.InputTokensDetails.CacheCreationTokens
	}
	return usage.InputTokensDetails.CacheWriteTokens
}

func (a *anthropicToOpenAIResponsesTranslator) responseBodyStreaming(body io.Reader, endOfStream bool) ([]internalapi.Header, []byte, metrics.TokenUsage, string, error) {
	var tokenUsage metrics.TokenUsage
	responseModel := a.requestModel
	if a.streamState == nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("stream state not initialized")
	}
	if _, err := a.streamBuffer.ReadFrom(body); err != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to read Responses stream: %w", err)
	}
	out := make([]byte, 0)
	if err := a.processResponsesStream(&out, endOfStream); err != nil {
		return nil, nil, tokenUsage, responseModel, err
	}
	responseModel = cmp.Or(a.streamState.model, a.requestModel)
	return nil, out, a.streamState.tokenUsage, responseModel, nil
}

func (a *anthropicToOpenAIResponsesTranslator) processResponsesStream(out *[]byte, endOfStream bool) error {
	for {
		data := a.streamBuffer.Bytes()
		boundary, width := bytes.Index(data, []byte("\n\n")), 2
		if crlf := bytes.Index(data, []byte("\r\n\r\n")); crlf >= 0 && (boundary < 0 || crlf < boundary) {
			boundary, width = crlf, 4
		}
		if boundary < 0 {
			break
		}
		block := append([]byte(nil), data[:boundary]...)
		remaining := append([]byte(nil), data[boundary+width:]...)
		a.streamBuffer.Reset()
		a.streamBuffer.Write(remaining)
		if err := a.processResponsesEventBlock(block, out); err != nil {
			return err
		}
	}
	if endOfStream && a.streamBuffer.Len() > 0 {
		block := append([]byte(nil), a.streamBuffer.Bytes()...)
		a.streamBuffer.Reset()
		if err := a.processResponsesEventBlock(block, out); err != nil {
			return err
		}
	}
	if endOfStream && !a.streamState.closingEmitted {
		return a.streamState.emitClosingEvents(out)
	}
	return nil
}

func (a *anthropicToOpenAIResponsesTranslator) processResponsesEventBlock(block []byte, out *[]byte) error {
	for line := range bytes.SplitSeq(block, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		data, ok := cutSSEDataPrefix(line)
		if !ok || len(data) == 0 || bytes.Equal(data, sseDoneMessage) {
			continue
		}
		var event openai.ResponseStreamEventUnion
		if err := json.Unmarshal(data, &event); err != nil {
			continue
		}
		if err := a.handleResponsesEvent(&event, out); err != nil {
			return err
		}
	}
	return nil
}

func (a *anthropicToOpenAIResponsesTranslator) handleResponsesEvent(event *openai.ResponseStreamEventUnion, out *[]byte) error {
	switch {
	case event.OfResponseCreated != nil:
		response := &event.OfResponseCreated.Response
		return a.streamState.handleChunk(&openai.ChatCompletionResponseChunk{
			ID: response.ID, Model: response.Model,
			Choices: []openai.ChatCompletionResponseChunkChoice{{
				Delta: &openai.ChatCompletionResponseChunkChoiceDelta{Role: "assistant"},
			}},
		}, out)
	case event.OfResponseTextDelta != nil:
		delta := event.OfResponseTextDelta.Delta
		return a.streamState.handleChunk(&openai.ChatCompletionResponseChunk{
			Choices: []openai.ChatCompletionResponseChunkChoice{{Delta: &openai.ChatCompletionResponseChunkChoiceDelta{Content: &delta}}},
		}, out)
	case event.OfResponseOutputItemAdded != nil && event.OfResponseOutputItemAdded.Item.OfFunctionCall != nil:
		call := event.OfResponseOutputItemAdded.Item.OfFunctionCall
		callID := call.CallID
		return a.streamState.handleChunk(responsesToolCallChunk(event.OfResponseOutputItemAdded.OutputIndex, &callID, call.Name, call.Arguments), out)
	case event.OfResponseFunctionCallArgumentsDelta != nil:
		delta := event.OfResponseFunctionCallArgumentsDelta
		a.streamedArguments[delta.ItemID] = true
		return a.streamState.handleChunk(responsesToolCallChunk(delta.OutputIndex, nil, "", delta.Delta), out)
	case event.OfResponseFunctionCallArgumentsDone != nil:
		done := event.OfResponseFunctionCallArgumentsDone
		if a.streamedArguments[done.ItemID] {
			return nil
		}
		return a.streamState.handleChunk(responsesToolCallChunk(done.OutputIndex, nil, done.Name, done.Arguments), out)
	case event.OfResponseOutputItemDone != nil && event.OfResponseOutputItemDone.Item.OfReasoning != nil:
		return a.emitResponsesReasoningItem(event.OfResponseOutputItemDone.Item.OfReasoning, out)
	case event.OfResponseCompleted != nil:
		return a.completeResponsesStream(&event.OfResponseCompleted.Response, out)
	case event.OfResponseIncomplete != nil:
		return a.completeResponsesStream(&event.OfResponseIncomplete.Response, out)
	case event.OfResponseFailed != nil:
		return a.completeResponsesStream(&event.OfResponseFailed.Response, out)
	case event.OfError != nil:
		encoded, err := json.Marshal(anthropic.ErrorResponse{
			Type: "error", Error: anthropic.ErrorResponseMessage{Type: event.OfError.Code, Message: event.OfError.Message},
		})
		if err != nil {
			return err
		}
		appendAnthropicSSEEvent(out, "error", encoded)
	}
	return nil
}

func (a *anthropicToOpenAIResponsesTranslator) emitResponsesReasoningItem(item *openai.ResponseReasoningItem, out *[]byte) error {
	envelope, err := encodeOpenAIReasoningItem(item)
	if err != nil {
		return err
	}
	key := cmp.Or(item.ID, envelope)
	if a.streamedReasoning[key] {
		return nil
	}
	a.streamedReasoning[key] = true
	return a.streamState.handleChunk(&openai.ChatCompletionResponseChunk{
		Choices: []openai.ChatCompletionResponseChunkChoice{{
			Delta: &openai.ChatCompletionResponseChunkChoiceDelta{
				ThinkingBlocks: []openai.ThinkingBlock{{Type: "redacted_thinking", Data: envelope}},
			},
		}},
	}, out)
}

func responsesToolCallChunk(index int64, id *string, name, arguments string) *openai.ChatCompletionResponseChunk {
	return &openai.ChatCompletionResponseChunk{Choices: []openai.ChatCompletionResponseChunkChoice{{
		Delta: &openai.ChatCompletionResponseChunkChoiceDelta{ToolCalls: []openai.ChatCompletionChunkChoiceDeltaToolCall{{
			Index: index, ID: id,
			Function: openai.ChatCompletionMessageToolCallFunctionParam{Name: name, Arguments: arguments},
		}}},
	}}}
}

func (a *anthropicToOpenAIResponsesTranslator) completeResponsesStream(response *openai.Response, out *[]byte) error {
	for i := range response.Output {
		if response.Output[i].OfReasoning != nil {
			if err := a.emitResponsesReasoningItem(response.Output[i].OfReasoning, out); err != nil {
				return err
			}
		}
	}
	finishReason := openai.ChatCompletionChoicesFinishReasonStop
	switch response.IncompleteDetails.Reason {
	case "max_output_tokens":
		finishReason = openai.ChatCompletionChoicesFinishReasonLength
	case "content_filter":
		finishReason = openai.ChatCompletionChoicesFinishReasonContentFilter
	default:
		if len(a.streamState.activeTools) > 0 {
			finishReason = openai.ChatCompletionChoicesFinishReasonToolCalls
		}
	}
	if err := a.streamState.handleChunk(&openai.ChatCompletionResponseChunk{
		ID: response.ID, Model: response.Model,
		Choices: []openai.ChatCompletionResponseChunkChoice{{FinishReason: finishReason}},
	}, out); err != nil {
		return err
	}
	usage := &openai.Usage{}
	if response.Usage != nil {
		usage.PromptTokens = int(response.Usage.InputTokens)
		usage.CompletionTokens = int(response.Usage.OutputTokens)
		usage.TotalTokens = int(response.Usage.TotalTokens)
	}
	if err := a.streamState.handleChunk(&openai.ChatCompletionResponseChunk{Usage: usage}, out); err != nil {
		return err
	}
	setTokenUsageFromResponse(&a.streamState.tokenUsage, response)
	if response.Usage != nil {
		if cacheCreationTokens := responseCacheCreationTokens(response.Usage); cacheCreationTokens > 0 {
			a.streamState.tokenUsage.SetCacheCreationInputTokens(uint32(cacheCreationTokens)) // #nosec G115
		}
	}
	return nil
}

func (a *anthropicToOpenAIResponsesTranslator) ResponseError(headers map[string]string, body io.Reader) ([]internalapi.Header, []byte, error) {
	return a.errorTranslator.ResponseError(headers, body)
}

func (a *anthropicToOpenAIResponsesTranslator) SetRedactionConfig(debugLogEnabled, enableRedaction bool, logger *slog.Logger) {
	a.debugLogEnabled = debugLogEnabled
	a.enableRedaction = enableRedaction
	a.logger = logger
}

func (a *anthropicToOpenAIResponsesTranslator) RedactAnthropicBody(resp *anthropic.MessagesResponse) *anthropic.MessagesResponse {
	return redactAnthropicMessagesResponse(resp)
}

func redactAnthropicMessagesResponse(resp *anthropic.MessagesResponse) *anthropic.MessagesResponse {
	if resp == nil {
		return nil
	}
	redacted := *resp
	if len(resp.Content) > 0 {
		redacted.Content = make([]anthropic.MessagesContentBlock, len(resp.Content))
		for i := range resp.Content {
			redacted.Content[i] = redactAnthropicContent(&resp.Content[i])
		}
	}
	return &redacted
}
