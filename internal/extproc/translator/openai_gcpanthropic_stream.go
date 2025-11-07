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

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

var (
	sseEventPrefix = []byte("event:")
	sseDataPrefix  = []byte("data: ")
	sseDoneMessage = []byte("[DONE]")
	emptyStrPtr    = ptr.To("")
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
	activeToolCalls map[int64]*streamingToolCall
	toolIndex       int64
	tokenUsage      LLMTokenUsage
	stopReason      anthropic.StopReason
	requestModel    internalapi.RequestModel
	sentFirstChunk  bool
}

// newAnthropicStreamParser creates a new parser for a streaming request.
func newAnthropicStreamParser(requestModel string) *anthropicStreamParser {
	toolIdx := int64(-1)
	return &anthropicStreamParser{
		requestModel:    requestModel,
		activeToolCalls: make(map[int64]*streamingToolCall),
		toolIndex:       toolIdx,
	}
}

func (p *anthropicStreamParser) writeChunk(eventBlock []byte, buf *[]byte) error {
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
		*buf = append(*buf, sseDataPrefix...)
		*buf = append(*buf, chunkBytes...)
		*buf = append(*buf, '\n', '\n')
	}
	return nil
}

// Process reads from the Anthropic SSE stream, translates events to OpenAI chunks,
// and returns the mutations for Envoy.
func (p *anthropicStreamParser) Process(body io.Reader, endOfStream bool, span tracing.ChatCompletionSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage LLMTokenUsage, responseModel string, err error,
) {
	_ = span // TODO: add support for streaming chunks in tracing.
	responseModel = p.requestModel
	if _, err = p.buffer.ReadFrom(body); err != nil {
		err = fmt.Errorf("failed to read from stream body: %w", err)
		return
	}

	for {
		eventBlock, remaining, found := bytes.Cut(p.buffer.Bytes(), []byte("\n\n"))
		if !found {
			break
		}

		if err = p.writeChunk(eventBlock, &newBody); err != nil {
			return
		}

		p.buffer.Reset()
		p.buffer.Write(remaining)
	}

	if endOfStream && p.buffer.Len() > 0 {
		finalEventBlock := p.buffer.Bytes()
		p.buffer.Reset()

		if err = p.writeChunk(finalEventBlock, &newBody); err != nil {
			return
		}
	}

	if endOfStream {
		p.tokenUsage.TotalTokens = p.tokenUsage.InputTokens + p.tokenUsage.OutputTokens
		finalChunk := openai.ChatCompletionResponseChunk{
			Object:  "chat.completion.chunk",
			Choices: []openai.ChatCompletionResponseChunkChoice{},
			Usage: &openai.Usage{
				PromptTokens:     int(p.tokenUsage.InputTokens),
				CompletionTokens: int(p.tokenUsage.OutputTokens),
				TotalTokens:      int(p.tokenUsage.TotalTokens),
				PromptTokensDetails: &openai.PromptTokensDetails{
					CachedTokens: int(p.tokenUsage.CachedInputTokens),
				},
			},
		}

		// Add active tool calls to the final chunk.
		var toolCalls []openai.ChatCompletionChunkChoiceDeltaToolCall
		for toolIndex, tool := range p.activeToolCalls {
			toolCalls = append(toolCalls, openai.ChatCompletionChunkChoiceDeltaToolCall{
				ID:   &tool.id,
				Type: openai.ChatCompletionMessageToolCallTypeFunction,
				Function: openai.ChatCompletionMessageToolCallFunctionParam{
					Name:      tool.name,
					Arguments: tool.inputJSON,
				},
				Index: toolIndex,
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
				return nil, nil, LLMTokenUsage{}, "", fmt.Errorf("failed to marshal final stream chunk: %w", err)
			}
			// Write the final chunk to the response body.
			newBody = append(newBody, sseDataPrefix...)
			newBody = append(newBody, chunkBytes...)
			newBody = append(newBody, '\n', '\n')
		}
		// Add the final [DONE] message to indicate the end of the stream.
		newBody = append(newBody, sseDataPrefix...)
		newBody = append(newBody, sseDoneMessage...)
		newBody = append(newBody, '\n', '\n')
	}
	tokenUsage = p.tokenUsage
	return
}

func (p *anthropicStreamParser) parseAndHandleEvent(eventBlock []byte) (*openai.ChatCompletionResponseChunk, error) {
	var eventType []byte
	var eventData []byte

	lines := bytes.SplitSeq(eventBlock, []byte("\n"))
	for line := range lines {
		if after, ok := bytes.CutPrefix(line, sseEventPrefix); ok {
			eventType = bytes.TrimSpace(after)
		} else if after, ok := bytes.CutPrefix(line, sseDataPrefix); ok {
			// This handles JSON data that might be split across multiple 'data:' lines
			// by concatenating them (Anthropic's format).
			data := bytes.TrimSpace(after)
			eventData = append(eventData, data...)
		}
	}

	if len(eventType) > 0 && len(eventData) > 0 {
		return p.handleAnthropicStreamEvent(eventType, eventData)
	}

	return nil, nil
}

func (p *anthropicStreamParser) handleAnthropicStreamEvent(eventType []byte, data []byte) (*openai.ChatCompletionResponseChunk, error) {
	switch string(eventType) {
	case string(constant.ValueOf[constant.MessageStart]()):
		var event anthropic.MessageStartEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal message_start: %w", err)
		}
		p.activeMessageID = event.Message.ID
		p.tokenUsage.InputTokens = uint32(event.Message.Usage.InputTokens)                 //nolint:gosec
		p.tokenUsage.CachedInputTokens += uint32(event.Message.Usage.CacheReadInputTokens) //nolint:gosec

		// reset the toolIndex for each message
		p.toolIndex = -1
		return nil, nil

	case string(constant.ValueOf[constant.ContentBlockStart]()):
		var event anthropic.ContentBlockStartEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal content_block_start: %w", err)
		}
		if event.ContentBlock.Type == string(constant.ValueOf[constant.ToolUse]()) || event.ContentBlock.Type == string(constant.ValueOf[constant.ServerToolUse]()) {
			p.toolIndex++
			var argsJSON string
			// Check if the input field is provided directly in the start event.
			if event.ContentBlock.Input != nil {
				switch input := event.ContentBlock.Input.(type) {
				case map[string]any:
					// for case where "input":{}, skip adding it to arguments.
					if len(input) > 0 {
						argsBytes, err := json.Marshal(input)
						if err != nil {
							return nil, fmt.Errorf("failed to marshal tool use input: %w", err)
						}
						argsJSON = string(argsBytes)
					}
				default:
					// although golang sdk defines type of Input to be any,
					// python sdk requires the type of Input to be Dict[str, object]:
					// https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/tool_use_block.py#L14.
					return nil, fmt.Errorf("unexpected tool use input type: %T", input)
				}
			}

			// Store the complete input JSON in our state.
			p.activeToolCalls[p.toolIndex] = &streamingToolCall{
				id:        event.ContentBlock.ID,
				name:      event.ContentBlock.Name,
				inputJSON: argsJSON,
			}

			delta := openai.ChatCompletionResponseChunkChoiceDelta{
				ToolCalls: []openai.ChatCompletionChunkChoiceDeltaToolCall{
					{
						Index: p.toolIndex,
						ID:    &event.ContentBlock.ID,
						Type:  openai.ChatCompletionMessageToolCallTypeFunction,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name: event.ContentBlock.Name,
							// Include the arguments if they are available.
							Arguments: argsJSON,
						},
					},
				},
			}
			return p.constructOpenAIChatCompletionChunk(delta, ""), nil
		}
		if event.ContentBlock.Type == string(constant.ValueOf[constant.Thinking]()) {
			delta := openai.ChatCompletionResponseChunkChoiceDelta{Content: emptyStrPtr}
			return p.constructOpenAIChatCompletionChunk(delta, ""), nil
		}

		if event.ContentBlock.Type == string(constant.ValueOf[constant.RedactedThinking]()) {
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
		p.tokenUsage.CachedInputTokens += uint32(event.Usage.CacheReadInputTokens) //nolint:gosec
		return nil, nil

	case string(constant.ValueOf[constant.ContentBlockDelta]()):
		var event anthropic.ContentBlockDeltaEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal content_block_delta: %w", err)
		}
		switch event.Delta.Type {
		case string(constant.ValueOf[constant.TextDelta]()), string(constant.ValueOf[constant.ThinkingDelta]()):
			// Treat thinking_delta just like a text_delta.
			delta := openai.ChatCompletionResponseChunkChoiceDelta{Content: &event.Delta.Text}
			return p.constructOpenAIChatCompletionChunk(delta, ""), nil
		case string(constant.ValueOf[constant.InputJSONDelta]()):
			tool, ok := p.activeToolCalls[p.toolIndex]
			if !ok {
				return nil, fmt.Errorf("received input_json_delta for unknown tool at index %d", p.toolIndex)
			}
			delta := openai.ChatCompletionResponseChunkChoiceDelta{
				ToolCalls: []openai.ChatCompletionChunkChoiceDeltaToolCall{
					{
						Index: p.toolIndex,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Arguments: event.Delta.PartialJSON,
						},
					},
				},
			}
			tool.inputJSON += event.Delta.PartialJSON
			return p.constructOpenAIChatCompletionChunk(delta, ""), nil
		}

	case string(constant.ValueOf[constant.ContentBlockStop]()):
		// This event is for state cleanup, no chunk is sent.
		var event anthropic.ContentBlockStopEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal content_block_stop: %w", err)
		}
		delete(p.activeToolCalls, p.toolIndex)
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
