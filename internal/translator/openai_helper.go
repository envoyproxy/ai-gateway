// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"cmp"
	"fmt"
	"strings"

	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

// The following are helper functions for creating an OpenAI ChatCompletionRequest from an Anthropic MessagesRequest

// buildOpenAIChatCompletionRequest converts an Anthropic MessagesRequest into an OpenAI ChatCompletionRequest.
// It handles model override, system prompts, message conversion, tools, and tool choice.
func buildOpenAIChatCompletionRequest(body *anthropic.MessagesRequest, modelNameOverride internalapi.ModelNameOverride) *openai.ChatCompletionRequest {
	model := cmp.Or(modelNameOverride, body.Model)
	messages := anthropicMessagesToOpenAI(body)
	tools := anthropicToolsToOpenAI(body.Tools)
	// body.ToolChoice is *anthropic.ToolChoice (pointer-to-interface). Dereference it before
	// passing so the type assertion inside anthropicToolChoiceToOpenAI works correctly.
	var toolChoiceVal anthropic.ToolChoice
	if body.ToolChoice != nil {
		toolChoiceVal = *body.ToolChoice
	}
	toolChoice := anthropicToolChoiceToOpenAI(toolChoiceVal, len(tools) > 0)

	maxTokens := int64(body.MaxTokens)
	req := &openai.ChatCompletionRequest{
		Model:               model,
		Messages:            messages,
		MaxCompletionTokens: &maxTokens,
		Temperature:         body.Temperature,
		TopP:                body.TopP,
		Stream:              body.Stream,
	}

	if len(tools) > 0 {
		req.Tools = tools
		req.ToolChoice = toolChoice
	}

	if body.Stream {
		req.StreamOptions = &openai.StreamOptions{IncludeUsage: true}
	}

	return req
}

// anthropicMessagesToOpenAI converts Anthropic messages (including the system prompt) to OpenAI message format.
func anthropicMessagesToOpenAI(body *anthropic.MessagesRequest) []openai.ChatCompletionMessageParamUnion {
	var messages []openai.ChatCompletionMessageParamUnion

	// Prepend the system prompt as an OpenAI system message.
	if body.System != nil {
		systemText := anthropicSystemPromptToText(body.System)
		if systemText != "" {
			messages = append(messages, openai.ChatCompletionMessageParamUnion{
				OfSystem: &openai.ChatCompletionSystemMessageParam{
					Content: openai.ContentUnion{Value: systemText},
					Role:    openai.ChatMessageRoleSystem,
				},
			})
		}
	}

	for _, msg := range body.Messages {
		switch msg.Role {
		case anthropic.MessageRoleUser:
			messages = append(messages, openai.ChatCompletionMessageParamUnion{
				OfUser: &openai.ChatCompletionUserMessageParam{
					Content: openai.StringOrUserRoleContentUnion{Value: anthropicContentToText(msg.Content)},
					Role:    openai.ChatMessageRoleUser,
				},
			})
		case anthropic.MessageRoleAssistant:
			messages = append(messages, openai.ChatCompletionMessageParamUnion{
				OfAssistant: &openai.ChatCompletionAssistantMessageParam{
					Content: openai.StringOrAssistantRoleContentUnion{Value: anthropicContentToText(msg.Content)},
					Role:    openai.ChatMessageRoleAssistant,
				},
			})
		}
	}

	return messages
}

// anthropicSystemPromptToText extracts a plain string from an Anthropic system prompt,
// concatenating text blocks if the prompt is in array form.
func anthropicSystemPromptToText(s *anthropic.SystemPrompt) string {
	if s.Text != "" {
		return s.Text
	}
	var sb strings.Builder
	for _, t := range s.Texts {
		sb.WriteString(t.Text)
	}
	return sb.String()
}

// anthropicContentToText extracts a plain text string from Anthropic message content.
// For array content, text blocks are concatenated in order.
func anthropicContentToText(content anthropic.MessageContent) string {
	if content.Text != "" {
		return content.Text
	}
	var sb strings.Builder
	for _, block := range content.Array {
		if block.Text != nil {
			sb.WriteString(block.Text.Text)
		}
	}
	return sb.String()
}

// anthropicToolsToOpenAI converts Anthropic custom tools to OpenAI function tools.
func anthropicToolsToOpenAI(tools []anthropic.Tool) []openai.Tool {
	if len(tools) == 0 {
		return nil
	}
	result := make([]openai.Tool, 0, len(tools))
	for _, t := range tools {
		result = append(result, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}
	return result
}

// anthropicToolChoiceToOpenAI converts an Anthropic tool_choice value to an OpenAI ChatCompletionToolChoiceUnion.
// Returns nil if no tools are present or the tool choice type is unrecognized.
func anthropicToolChoiceToOpenAI(tc anthropic.ToolChoice, hasTools bool) *openai.ChatCompletionToolChoiceUnion {
	if tc == nil || !hasTools {
		return nil
	}
	// TODO: Change this, typ, and name (type assertion) when ToolChoice is further defined
	m, ok := tc.(map[string]any)
	if !ok {
		return nil
	}
	typ, _ := m["type"].(string)
	switch typ {
	case "auto":
		return &openai.ChatCompletionToolChoiceUnion{Value: string(openai.ToolChoiceTypeAuto)}
	case "none":
		return &openai.ChatCompletionToolChoiceUnion{Value: string(openai.ToolChoiceTypeNone)}
	case "any":
		// Anthropic "any" maps to OpenAI "required" (model must call a tool).
		return &openai.ChatCompletionToolChoiceUnion{Value: string(openai.ToolChoiceTypeRequired)}
	case "tool":
		name, _ := m["name"].(string)
		return &openai.ChatCompletionToolChoiceUnion{
			Value: openai.ChatCompletionNamedToolChoice{
				Type:     openai.ToolTypeFunction,
				Function: openai.ChatCompletionNamedToolChoiceFunction{Name: name},
			},
		}
	default:
		return nil
	}
}

// The following are helper functions that convert an OpenAI ChatCompletionResponse to an Anthropic MessagesRepsonse

// openAIResponseToAnthropic converts an OpenAI ChatCompletionResponse to an Anthropic MessagesResponse.
func openAIResponseToAnthropic(resp *openai.ChatCompletionResponse, model string) *anthropic.MessagesResponse {
	var content []anthropic.MessagesContentBlock
	var stopReason *anthropic.StopReason

	if len(resp.Choices) > 0 {
		choice := &resp.Choices[0]
		msg := &choice.Message

		// Convert text content.
		if msg.Content != nil && *msg.Content != "" {
			content = append(content, anthropic.MessagesContentBlock{
				Text: &anthropic.TextBlock{Type: "text", Text: *msg.Content},
			})
		}

		// Convert tool calls to tool_use content blocks.
		for _, tc := range msg.ToolCalls {
			var input map[string]any
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &input)
			}
			// If tool call json string is malformed (OpenAI allows this because of it being cut off mid-stream)
			// then we set input to an empty map
			if input == nil {
				input = map[string]any{}
			}
			id := ""
			if tc.ID != nil {
				id = *tc.ID
			}
			content = append(content, anthropic.MessagesContentBlock{
				Tool: &anthropic.ToolUseBlock{
					Type:  "tool_use",
					ID:    id,
					Name:  tc.Function.Name,
					Input: input,
				},
			})
		}

		sr := openAIFinishReasonToAnthropic(choice.FinishReason)
		stopReason = &sr
	}

	usage := &anthropic.Usage{
		InputTokens:  float64(resp.Usage.PromptTokens),
		OutputTokens: float64(resp.Usage.CompletionTokens),
	}

	return &anthropic.MessagesResponse{
		ID:         resp.ID,
		Type:       anthropic.ConstantMessagesResponseTypeMessages("message"),
		Role:       anthropic.ConstantMessagesResponseRoleAssistant("assistant"),
		Content:    content,
		Model:      model,
		StopReason: stopReason,
		Usage:      usage,
	}
}

// openAIFinishReasonToAnthropic maps an OpenAI finish_reason to an Anthropic StopReason.
func openAIFinishReasonToAnthropic(reason openai.ChatCompletionChoicesFinishReason) anthropic.StopReason {
	switch reason {
	case openai.ChatCompletionChoicesFinishReasonStop:
		return anthropic.StopReasonEndTurn
	case openai.ChatCompletionChoicesFinishReasonLength:
		return anthropic.StopReasonMaxTokens
	case openai.ChatCompletionChoicesFinishReasonToolCalls:
		return anthropic.StopReasonToolUse
	case openai.ChatCompletionChoicesFinishReasonContentFilter:
		return anthropic.StopReasonRefusal
	default:
		return anthropic.StopReasonEndTurn
	}
}

// The following are helpers that convert an OpenAI Stream to an Anthropic Stream (SSE conversion)

// openAIStreamToAnthropicState tracks the state for converting OpenAI SSE chunks to Anthropic SSE events.
type openAIStreamToAnthropicState struct {
	buffer         bytes.Buffer
	messageStarted bool // flag indicating emitted message_start
	hasOpenBlock   bool // flag indicating emitted content_block_start but not content_block_stop
	closingEmitted bool // flag indicating emitted content_block_stop + message_delta + message_stop
	messageID      string
	model          string
	stopReason     string // Anthropic stop_reason, mapped from OpenAI finish_reason
	inputTokens    int
	outputTokens   int
	tokenUsage     metrics.TokenUsage
	blockIndex     int                       // current Anthropic content block index
	activeTools    map[int64]*streamToolCall // keyed by OpenAI tool_call index
	requestModel   string
}

type streamToolCall struct {
	blockIdx int
	id       string
	name     string
}

// processBuffer processes the buffered OpenAI SSE data and emits Anthropic SSE events.
func (s *openAIStreamToAnthropicState) processBuffer(out *[]byte, endOfStream bool) error {
	// Loop through all event blocks that are separated by a blank line
	for {
		eventBlock, remaining, found := bytes.Cut(s.buffer.Bytes(), []byte("\n\n"))
		if !found {
			break
		}
		if err := s.processEventBlock(eventBlock, out); err != nil {
			return err
		}
		// Clear buffer and add back remaining SSE data
		s.buffer.Reset()
		s.buffer.Write(remaining)
	}

	// Handle any remaining data at end of stream.
	if endOfStream {
		if s.buffer.Len() > 0 {
			remaining := s.buffer.Bytes()
			s.buffer.Reset()
			if err := s.processEventBlock(remaining, out); err != nil {
				return err
			}
		}
		if !s.closingEmitted {
			return s.emitClosingEvents(out)
		}
	}
	return nil
}

// processEventBlock processes a single SSE event block (data between consecutive \n\n separators).
func (s *openAIStreamToAnthropicState) processEventBlock(block []byte, out *[]byte) error {
	var eventData []byte
	for line := range bytes.SplitSeq(block, []byte("\n")) {
		if after, ok := bytes.CutPrefix(line, sseDataPrefix); ok {
			data := bytes.TrimSpace(after)
			if len(data) > 0 {
				eventData = data
			}
		}
	}

	if len(eventData) == 0 {
		return nil
	}

	// Skip the [DONE] marker; closing events are emitted on the usage chunk or endOfStream.
	if bytes.Equal(eventData, sseDoneMessage) {
		return nil
	}

	var chunk openai.ChatCompletionResponseChunk
	if err := json.Unmarshal(eventData, &chunk); err != nil {
		// Skip malformed chunks silently.
		return nil
	}

	return s.handleChunk(&chunk, out)
}

// handleChunk converts a single OpenAI ChatCompletionResponseChunk to Anthropic SSE events.
func (s *openAIStreamToAnthropicState) handleChunk(chunk *openai.ChatCompletionResponseChunk, out *[]byte) error {
	// Update StreamState's message ID and model with chunk ID and model
	if chunk.ID != "" && s.messageID == "" {
		s.messageID = chunk.ID
	}
	if chunk.Model != "" && s.model == "" {
		s.model = chunk.Model
	}

	// Usage-only chunk (emitted when stream_options.include_usage=true)
	// One of the two ways to indicate stream end (other is endOfStream)
	if len(chunk.Choices) == 0 && chunk.Usage != nil {
		s.inputTokens = chunk.Usage.PromptTokens
		s.outputTokens = chunk.Usage.CompletionTokens
		s.tokenUsage = metrics.ExtractTokenUsageFromExplicitCaching(
			int64(s.inputTokens),
			int64(s.outputTokens),
			ptr.To(int64(0)),
			ptr.To(int64(0)),
		)
		return s.emitClosingEvents(out)
	}

	if len(chunk.Choices) == 0 {
		return nil
	}

	// Choose first choice in chunk
	choice := &chunk.Choices[0]
	delta := choice.Delta

	// Emit message_start on the first meaningful delta.
	if !s.messageStarted && delta != nil {
		if err := s.emitMessageStart(out); err != nil {
			return err
		}
	}

	if delta != nil {
		// Handle text content.
		if delta.Content != nil && *delta.Content != "" {
			// Emit textblockstart if not started
			if !s.hasOpenBlock {
				if err := s.emitTextBlockStart(out); err != nil {
					return err
				}
			}
			if err := s.emitTextDelta(*delta.Content, out); err != nil {
				return err
			}
		}

		// Handle tool call deltas.
		for i := range delta.ToolCalls {
			if err := s.handleToolCallDelta(&delta.ToolCalls[i], out); err != nil {
				return err
			}
		}
	}

	// Store finish_reason for use in the closing events.
	if choice.FinishReason != "" {
		s.stopReason = string(openAIFinishReasonToAnthropic(choice.FinishReason))
	}

	return nil
}

// emitMessageStart emits the Anthropic message_start SSE event.
func (s *openAIStreamToAnthropicState) emitMessageStart(out *[]byte) error {
	s.messageStarted = true
	payload := map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            s.messageID,
			"type":          "message",
			"role":          "assistant",
			"content":       []any{},
			"model":         cmp.Or(s.model, s.requestModel),
			"stop_reason":   nil,
			"stop_sequence": nil,
			// Input tokens are not yet known; they will be reported in message_delta.usage.
			"usage": map[string]int{"input_tokens": 0, "output_tokens": 0},
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal message_start: %w", err)
	}
	appendAnthropicSSEEvent(out, "message_start", data)
	return nil
}

// emitTextBlockStart emits a content_block_start SSE event for a text content block.
func (s *openAIStreamToAnthropicState) emitTextBlockStart(out *[]byte) error {
	s.hasOpenBlock = true
	payload := map[string]any{
		"type":  "content_block_start",
		"index": s.blockIndex,
		"content_block": map[string]any{
			"type": "text",
			"text": "",
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal content_block_start: %w", err)
	}
	appendAnthropicSSEEvent(out, "content_block_start", data)
	return nil
}

// emitTextDelta emits a content_block_delta SSE event with text content.
func (s *openAIStreamToAnthropicState) emitTextDelta(text string, out *[]byte) error {
	payload := map[string]any{
		"type":  "content_block_delta",
		"index": s.blockIndex,
		"delta": map[string]any{
			"type": "text_delta",
			"text": text,
		},
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal content_block_delta: %w", err)
	}
	appendAnthropicSSEEvent(out, "content_block_delta", data)
	return nil
}

// handleToolCallDelta handles an OpenAI tool call delta and emits Anthropic tool_use content block events.
func (s *openAIStreamToAnthropicState) handleToolCallDelta(tc *openai.ChatCompletionChunkChoiceDeltaToolCall, out *[]byte) error {
	tool, exists := s.activeTools[tc.Index]
	if !exists {
		// New tool call: close any open block (e.g., text block) and open a new tool_use block.
		if s.hasOpenBlock {
			if err := s.emitContentBlockStop(out); err != nil {
				return err
			}
			s.blockIndex++
			s.hasOpenBlock = false
		}

		id := ""
		if tc.ID != nil {
			id = *tc.ID
		}
		tool = &streamToolCall{
			blockIdx: s.blockIndex,
			id:       id,
			name:     tc.Function.Name,
		}
		s.activeTools[tc.Index] = tool
		s.hasOpenBlock = true

		// Emit content_block_start for the new tool_use block.
		payload := map[string]any{
			"type":  "content_block_start",
			"index": tool.blockIdx,
			"content_block": map[string]any{
				"type":  "tool_use",
				"id":    id,
				"name":  tc.Function.Name,
				"input": map[string]any{},
			},
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal tool content_block_start: %w", err)
		}
		appendAnthropicSSEEvent(out, "content_block_start", data)
	}

	// Emit input_json_delta for accumulated tool arguments.
	if tc.Function.Arguments != "" {
		payload := map[string]any{
			"type":  "content_block_delta",
			"index": tool.blockIdx,
			"delta": map[string]any{
				"type":         "input_json_delta",
				"partial_json": tc.Function.Arguments,
			},
		}
		data, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("failed to marshal input_json_delta: %w", err)
		}
		appendAnthropicSSEEvent(out, "content_block_delta", data)
	}

	return nil
}

// emitContentBlockStop emits a content_block_stop SSE event for the current block.
func (s *openAIStreamToAnthropicState) emitContentBlockStop(out *[]byte) error {
	payload := map[string]any{
		"type":  "content_block_stop",
		"index": s.blockIndex,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal content_block_stop: %w", err)
	}
	appendAnthropicSSEEvent(out, "content_block_stop", data)
	return nil
}

// emitClosingEvents emits content_block_stop (if a block is open), message_delta, and message_stop SSE events.
func (s *openAIStreamToAnthropicState) emitClosingEvents(out *[]byte) error {
	if s.closingEmitted {
		return nil
	}
	s.closingEmitted = true

	// Close the currently open content block.
	if s.hasOpenBlock {
		if err := s.emitContentBlockStop(out); err != nil {
			return err
		}
		s.hasOpenBlock = false
	}

	stopReason := s.stopReason
	if stopReason == "" {
		stopReason = string(anthropic.StopReasonEndTurn)
	}

	// Emit message_delta with stop_reason and final output token count.
	msgDeltaPayload := map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]int{
			"output_tokens": s.outputTokens,
		},
	}
	data, err := json.Marshal(msgDeltaPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal message_delta: %w", err)
	}
	appendAnthropicSSEEvent(out, "message_delta", data)

	// Emit message_stop.
	msgStopPayload := map[string]any{"type": "message_stop"}
	data, err = json.Marshal(msgStopPayload)
	if err != nil {
		return fmt.Errorf("failed to marshal message_stop: %w", err)
	}
	appendAnthropicSSEEvent(out, "message_stop", data)

	return nil
}

// appendAnthropicSSEEvent appends a formatted Anthropic SSE event to the output buffer.
func appendAnthropicSSEEvent(buf *[]byte, eventType string, data []byte) {
	*buf = append(*buf, "event: "...)
	*buf = append(*buf, eventType...)
	*buf = append(*buf, '\n')
	*buf = append(*buf, "data: "...)
	*buf = append(*buf, data...)
	*buf = append(*buf, '\n', '\n')
}
