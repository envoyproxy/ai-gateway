// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

const (
	sseEventPrefix = "event:"
	sseDataPrefix  = "data: "
	sseDoneMessage = "[DONE]"
)

// streamingToolCall holds the state for a single tool call that is being streamed.
type streamingToolCall struct {
	id        string
	name      string
	inputJSON string
}

// anthropicStreamParser manages the stateful translation of an Anthropic SSE stream
// to an OpenAI-compatible SSE stream.
type anthropicStreamParser struct {
	buffer          bytes.Buffer
	activeMessageID string
	activeToolCalls map[int]*streamingToolCall
	tokenUsage      LLMTokenUsage
	stopReason      anthropic.StopReason
	model           string
	sentFirstChunk  bool
}

// newAnthropicStreamParser creates a new parser for a streaming request.
func newAnthropicStreamParser(modelName string) *anthropicStreamParser {
	return &anthropicStreamParser{
		model:           modelName,
		activeToolCalls: make(map[int]*streamingToolCall),
	}
}

func (p *anthropicStreamParser) writeChunk(eventBlock string, builder *strings.Builder) error {
	chunk, err := p.parseAndHandleEvent(eventBlock)
	if err != nil {
		return err
	}
	if chunk != nil {
		var chunkBytes []byte
		chunkBytes, err = json.Marshal(chunk)
		if err != nil {
			return fmt.Errorf("failed to marshal stream chunk: %w", err)
		}
		builder.WriteString(sseDataPrefix)
		builder.Write(chunkBytes)
		builder.WriteString("\n\n")
	}
	return nil
}

// Process reads from the Anthropic SSE stream, translates events to OpenAI chunks,
// and returns the mutations for Envoy.
func (p *anthropicStreamParser) Process(body io.Reader, endOfStream bool) (
	*extprocv3.HeaderMutation, *extprocv3.BodyMutation, LLMTokenUsage, error,
) {
	if _, err := p.buffer.ReadFrom(body); err != nil {
		return nil, nil, LLMTokenUsage{}, fmt.Errorf("failed to read from stream body: %w", err)
	}

	var responseBodyBuilder strings.Builder
	for {
		eventBlock, remaining, found := bytes.Cut(p.buffer.Bytes(), []byte("\n\n"))
		if !found {
			break
		}

		if err := p.writeChunk(string(eventBlock), &responseBodyBuilder); err != nil {
			return nil, nil, LLMTokenUsage{}, err
		}

		p.buffer.Reset()
		p.buffer.Write(remaining)
	}

	if endOfStream && p.buffer.Len() > 0 {
		finalEventBlock := p.buffer.String()
		p.buffer.Reset()

		if err := p.writeChunk(finalEventBlock, &responseBodyBuilder); err != nil {
			return nil, nil, LLMTokenUsage{}, err
		}
	}

	// After the loop, if it's the end of the stream and the buffer still contains
	// the final event (which has no trailing \n\n), process it now.
	if endOfStream && p.buffer.Len() > 0 {
		finalEventBlock := p.buffer.String()
		p.buffer.Reset() // Clear the buffer as we are done with it.

		chunk, err := p.parseAndHandleEvent(finalEventBlock)
		if err != nil {
			return nil, nil, LLMTokenUsage{}, err
		}
		if chunk != nil {
			chunkBytes, err := json.Marshal(chunk)
			if err != nil {
				return nil, nil, LLMTokenUsage{}, fmt.Errorf("failed to marshal final event chunk: %w", err)
			}
			responseBodyBuilder.WriteString(sseDataPrefix)
			responseBodyBuilder.Write(chunkBytes)
			responseBodyBuilder.WriteString("\n\n")
		}
	}

	if endOfStream {
		p.tokenUsage.TotalTokens = p.tokenUsage.InputTokens + p.tokenUsage.OutputTokens
		finalChunk := openai.ChatCompletionResponseChunk{
			Object:  "chat.completion.chunk",
			Choices: []openai.ChatCompletionResponseChunkChoice{},
			Usage: &openai.ChatCompletionResponseUsage{
				PromptTokens:     int(p.tokenUsage.InputTokens),
				CompletionTokens: int(p.tokenUsage.OutputTokens),
				TotalTokens:      int(p.tokenUsage.TotalTokens),
			},
		}

		// Add active tool calls to the final chunk.
		var toolCalls []openai.ChatCompletionMessageToolCallParam
		for _, tool := range p.activeToolCalls {
			toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
				ID:   &tool.id,
				Type: openai.ChatCompletionMessageToolCallTypeFunction,
				Function: openai.ChatCompletionMessageToolCallFunctionParam{
					Name:      tool.name,
					Arguments: tool.inputJSON,
				},
			})
		}

		if len(toolCalls) > 0 {
			delta := openai.ChatCompletionResponseChunkChoiceDelta{
				ToolCalls: toolCalls,
			}
			finalChunk.Choices = append(finalChunk.Choices, openai.ChatCompletionResponseChunkChoice{
				Delta: &delta,
			})
		}

		if finalChunk.Usage.PromptTokens > 0 || finalChunk.Usage.CompletionTokens > 0 || len(finalChunk.Choices) > 0 {
			chunkBytes, err := json.Marshal(finalChunk)
			if err != nil {
				return nil, nil, LLMTokenUsage{}, fmt.Errorf("failed to marshal final stream chunk: %w", err)
			}
			responseBodyBuilder.WriteString(sseDataPrefix)
			responseBodyBuilder.Write(chunkBytes)
			responseBodyBuilder.WriteString("\n\n")
		}
		responseBodyBuilder.WriteString(sseDataPrefix + sseDoneMessage + "\n\n")
	}

	finalBody := responseBodyBuilder.String()
	mut := &extprocv3.BodyMutation_Body{Body: []byte(finalBody)}

	return &extprocv3.HeaderMutation{}, &extprocv3.BodyMutation{Mutation: mut}, p.tokenUsage, nil
}

func (p *anthropicStreamParser) parseAndHandleEvent(eventBlock string) (*openai.ChatCompletionResponseChunk, error) {
	var eventType string
	var eventData strings.Builder

	lines := strings.Split(eventBlock, "\n")
	for _, line := range lines {
		// Per the SSE spec, lines starting with a colon are comments and should be ignored.
		if strings.HasPrefix(line, ":") {
			continue
		}

		if strings.HasPrefix(line, sseEventPrefix) {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, sseEventPrefix))
		} else if strings.HasPrefix(line, sseDataPrefix) {
			// This handles JSON data that might be split across multiple 'data:' lines
			// by concatenating them (Anthropic's format).
			data := strings.TrimSpace(strings.TrimPrefix(line, sseDataPrefix))
			eventData.WriteString(data)
		}
	}

	// After checking all lines, if we found an event, handle it.
	if eventType != "" {
		return p.handleAnthropicStreamEvent(eventType, []byte(eventData.String()))
	}

	return nil, nil
}

func (p *anthropicStreamParser) handleAnthropicStreamEvent(eventType string, data []byte) (*openai.ChatCompletionResponseChunk, error) {
	switch eventType {
	case string(constant.ValueOf[constant.MessageStart]()):
		var event anthropic.MessageStartEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal message_start: %w", err)
		}
		p.activeMessageID = event.Message.ID
		p.tokenUsage.InputTokens = uint32(event.Message.Usage.InputTokens) //nolint:gosec
		return nil, nil

	case string(constant.ValueOf[constant.ContentBlockStart]()):
		var event anthropic.ContentBlockStartEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal content_block_start: %w", err)
		}
		if event.ContentBlock.Type == string(constant.ValueOf[constant.ToolUse]()) || event.ContentBlock.Type == string(constant.ValueOf[constant.ServerToolUse]()) {
			toolIdx := int(event.Index)
			p.activeToolCalls[toolIdx] = &streamingToolCall{
				id:        event.ContentBlock.ID,
				name:      event.ContentBlock.Name,
				inputJSON: "",
			}
			delta := openai.ChatCompletionResponseChunkChoiceDelta{
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{
					{
						Index: &toolIdx,
						ID:    &event.ContentBlock.ID,
						Type:  openai.ChatCompletionMessageToolCallTypeFunction,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name: event.ContentBlock.Name,
						},
					},
				},
			}
			return p.constructOpenAIChatCompletionChunk(delta, ""), nil
		} else if event.ContentBlock.Type == string(constant.ValueOf[constant.Thinking]()) || event.ContentBlock.Type == string(constant.ValueOf[constant.RedactedThinking]()) {
			// This is a latency-hiding event, ignore it.
			return nil, nil
		}

		return nil, nil

	case string(constant.ValueOf[constant.MessageDelta]()):
		var event anthropic.MessageDeltaEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal message_delta: %w", err)
		}
		p.tokenUsage.OutputTokens += uint32(event.Usage.OutputTokens) //nolint:gosec
		if event.Delta.StopReason != "" {
			p.stopReason = event.Delta.StopReason
		}
		return nil, nil

	case string(constant.ValueOf[constant.ContentBlockDelta]()):
		var event anthropic.ContentBlockDeltaEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal content_block_delta: %w", err)
		}
		switch event.Delta.Type {
		case string(constant.ValueOf[constant.TextDelta]()):
			delta := openai.ChatCompletionResponseChunkChoiceDelta{Content: &event.Delta.Text}
			return p.constructOpenAIChatCompletionChunk(delta, ""), nil
		case string(constant.ValueOf[constant.InputJSONDelta]()):
			tool, ok := p.activeToolCalls[int(event.Index)]
			if !ok {
				return nil, fmt.Errorf("received input_json_delta for unknown tool at index %d", event.Index)
			}
			index := int(event.Index)
			delta := openai.ChatCompletionResponseChunkChoiceDelta{
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{
					{
						Index: &index,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Arguments: event.Delta.PartialJSON,
						},
					},
				},
			}
			tool.inputJSON += event.Delta.PartialJSON
			return p.constructOpenAIChatCompletionChunk(delta, ""), nil
		case string(constant.ValueOf[constant.ThinkingDelta]()):
			// This is a latency-hiding event, ignore it.
			return nil, nil
		}

	case string(constant.ValueOf[constant.ContentBlockStop]()):
		// This event is for state cleanup, no chunk is sent.
		var event anthropic.ContentBlockStopEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal content_block_stop: %w", err)
		}
		delete(p.activeToolCalls, int(event.Index))
		return nil, nil

	case string(constant.ValueOf[constant.MessageStop]()):
		var event anthropic.MessageStopEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal message_stop: %w", err)
		}

		if p.stopReason == "" {
			p.stopReason = anthropic.StopReasonEndTurn
		}

		finishReason, err := anthropicToOpenAIFinishReason(p.stopReason)
		if err != nil {
			return nil, err
		}
		return p.constructOpenAIChatCompletionChunk(openai.ChatCompletionResponseChunkChoiceDelta{}, finishReason), nil

	case string(constant.ValueOf[constant.Error]()):
		var errEvent anthropic.ErrorResponse
		if err := json.Unmarshal(data, &errEvent); err != nil {
			return nil, fmt.Errorf("unparsable error event: %s", string(data))
		}
		return nil, fmt.Errorf("anthropic stream error: %s - %s", errEvent.Error.Type, errEvent.Error.Message)

	case "ping":
		// Per documentation, ping events can be ignored.
		return nil, nil
	}
	return nil, nil
}

// constructOpenAIChatCompletionChunk builds the stream chunk.
func (p *anthropicStreamParser) constructOpenAIChatCompletionChunk(delta openai.ChatCompletionResponseChunkChoiceDelta, finishReason openai.ChatCompletionChoicesFinishReason) *openai.ChatCompletionResponseChunk {
	// Add the 'assistant' role to the very first chunk of the response.
	if !p.sentFirstChunk {
		// Only add the role if the delta actually contains content or a tool call.
		if delta.Content != nil || len(delta.ToolCalls) > 0 {
			delta.Role = openai.ChatMessageRoleAssistant
			p.sentFirstChunk = true
		}
	}

	return &openai.ChatCompletionResponseChunk{
		Object: "chat.completion.chunk",
		Choices: []openai.ChatCompletionResponseChunkChoice{
			{
				Delta:        &delta,
				FinishReason: finishReason,
			},
		},
	}
}
