// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

const (
	sseEventPrefix = "event:"
	sseDataPrefix  = "data: "
	sseDoneMessage = ""
)

// streamingToolCall holds the state for a single tool call that is being streamed.
type streamingToolCall struct {
	ID         string
	Name       string
	InputJSON  bytes.Buffer
	SentHeader bool
}

// AnthropicStreamParser manages the stateful translation of an Anthropic SSE stream
// to an OpenAI-compatible SSE stream.
type AnthropicStreamParser struct {
	eventScanner    *bufio.Scanner
	buffer          bytes.Buffer
	activeMessageID string
	activeToolCalls map[int]*streamingToolCall
	tokenUsage      LLMTokenUsage
	stopReason      anthropic.StopReason
	model           string
}

// NewAnthropicStreamParser creates a new parser for a streaming request.
func NewAnthropicStreamParser(modelName string) *AnthropicStreamParser {
	return &AnthropicStreamParser{
		model:           modelName,
		activeToolCalls: make(map[int]*streamingToolCall),
	}
}

// Process reads from the Anthropic SSE stream, translates events to OpenAI chunks,
// and returns the mutations for Envoy.
func (p *AnthropicStreamParser) Process(body io.Reader, endOfStream bool) (
	*extprocv3.HeaderMutation, *extprocv3.BodyMutation, LLMTokenUsage, error,
) {
	if _, err := p.buffer.ReadFrom(body); err != nil {
		return nil, nil, LLMTokenUsage{}, fmt.Errorf("failed to read from stream body: %w", err)
	}

	fmt.Printf("Raw Body: %s\n", p.buffer.String()) // Added debug print

	var responseBodyBuilder strings.Builder
	for {
		eventBlock, remaining, found := bytes.Cut(p.buffer.Bytes(), []byte("\n\n"))
		if !found {
			break // No complete event in buffer, wait for more data.
		}

		p.buffer.Reset()
		p.buffer.Write(remaining) // Put remaining data back in buffer.

		chunk, err := p.parseAndHandleEvent(string(eventBlock))
		if err != nil {
			return nil, nil, LLMTokenUsage{}, err
		}

		if chunk != nil {
			chunkBytes, err := json.Marshal(chunk)
			if err != nil {
				return nil, nil, LLMTokenUsage{}, fmt.Errorf("failed to marshal stream chunk: %w", err)
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

		// Add active tool calls to the final chunk
		var toolCalls []openai.ChatCompletionMessageToolCallParam
		for _, tool := range p.activeToolCalls {
			toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
				ID:   tool.ID,
				Type: openai.ChatCompletionMessageToolCallTypeFunction,
				Function: openai.ChatCompletionMessageToolCallFunctionParam{
					Name:      tool.Name,
					Arguments: tool.InputJSON.String(),
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

		chunkBytes, err := json.Marshal(finalChunk)
		if err != nil {
			return nil, nil, LLMTokenUsage{}, fmt.Errorf("failed to marshal final stream chunk: %w", err)
		}
		responseBodyBuilder.WriteString(sseDataPrefix)
		responseBodyBuilder.Write(chunkBytes)
		responseBodyBuilder.WriteString("\n\n")
		responseBodyBuilder.WriteString(sseDataPrefix + sseDoneMessage + "\n\n")
	}

	finalBody := responseBodyBuilder.String()
	if len(finalBody) == 0 {
		return nil, nil, LLMTokenUsage{}, nil
	}

	mut := &extprocv3.BodyMutation_Body{Body: []byte(finalBody)}
	bodyMutation := &extprocv3.BodyMutation{Mutation: mut}
	headerMutation := &extprocv3.HeaderMutation{
		RemoveHeaders: []string{"content-encoding"},
	}

	return headerMutation, bodyMutation, p.tokenUsage, nil
}

func (p *AnthropicStreamParser) parseAndHandleEvent(eventBlock string) (*openai.ChatCompletionResponseChunk, error) {
	var eventType string
	var eventData strings.Builder
	lines := strings.Split(eventBlock, "\n")
	fmt.Println(len(lines), " lines in event block") // Added debug print

	for _, line := range lines {
		fmt.Println("Processing line:", line) // Added debug print
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, sseEventPrefix) {
			if eventType == "" {
				eventType = strings.TrimSpace(strings.TrimPrefix(line, sseEventPrefix))
				fmt.Printf("Extracted EventType: %s\n", eventType)
			}
		} else if strings.HasPrefix(line, sseDataPrefix) {
			if eventType != "" {
				data := strings.TrimSpace(strings.TrimPrefix(line, sseDataPrefix))
				eventData.WriteString(data)
				fmt.Printf("Accumulated EventData: %s\n", eventData.String())
			}
		} else if line != "" {
			fmt.Printf("Unexpected line in event block: %s\n", line)
		}
	}
	if eventType != "" {
		fmt.Printf("Final EventType: %s, Final EventData: %s\n", eventType, eventData.String())
		return p.handleAnthropicStreamEvent(eventType, []byte(eventData.String()))
	}
	return nil, nil
}

func (p *AnthropicStreamParser) handleAnthropicStreamEvent(eventType string, data []byte) (*openai.ChatCompletionResponseChunk, error) {
	fmt.Printf("EventType: %s, Data: %s\n", eventType, string(data)) // Added debug print
	switch eventType {
	case string(constant.ValueOf[constant.MessageStart]()):
		fmt.Println("Handling MessageStart event") // Added debug print
		var event anthropic.MessageStartEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal message_start: %w", err)
		}
		p.activeMessageID = event.Message.ID
		p.tokenUsage.InputTokens = uint32(event.Message.Usage.InputTokens)
		return nil, nil

	case string(constant.ValueOf[constant.ContentBlockStart]()):
		fmt.Println("Handling ContentBlockStart event") // Added debug print
		var event anthropic.ContentBlockStartEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal content_block_start: %w", err)
		}
		if event.ContentBlock.Type == string(constant.ValueOf[constant.ToolUse]()) {
			p.activeToolCalls[int(event.Index)] = &streamingToolCall{
				ID:        event.ContentBlock.ID,
				Name:      event.ContentBlock.Name,
				InputJSON: bytes.Buffer{}, // Initialize the InputJSON buffer.
			}
			delta := openai.ChatCompletionResponseChunkChoiceDelta{
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{
					{
						ID:   event.ContentBlock.ID,
						Type: openai.ChatCompletionMessageToolCallTypeFunction,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name: event.ContentBlock.Name,
							// Setting Arguments as empty as the arguments are populated in ContentBlockDelta events,
							// not at the start of the block.
							Arguments: "",
						},
					},
				},
			}
			return p.constructOpenAIChatCompletionChunk(delta, ""), nil
		}
		return nil, nil

	case string(constant.ValueOf[constant.MessageDelta]()):
		var event anthropic.MessageDeltaEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal message_delta: %w", err)
		}
		p.tokenUsage.OutputTokens = uint32(event.Usage.OutputTokens)
		if event.Delta.StopReason != "" {
			p.stopReason = event.Delta.StopReason
		}
		delta := openai.ChatCompletionResponseChunkChoiceDelta{}
		return p.constructOpenAIChatCompletionChunk(delta, ""), nil

	case string(constant.ValueOf[constant.ContentBlockDelta]()):
		fmt.Println("Handling ContentBlockDelta event")
		fmt.Println("event type in content block delta ", eventType) // Added debug print
		fmt.Printf("ContentBlockDelta Data: %s\n", string(data))     // Added debug print
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
			delta := openai.ChatCompletionResponseChunkChoiceDelta{
				ToolCalls: []openai.ChatCompletionMessageToolCallParam{
					{
						ID: tool.ID,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Arguments: event.Delta.PartialJSON,
						},
					},
				},
			}
			tool.InputJSON.WriteString(event.Delta.PartialJSON)
			return p.constructOpenAIChatCompletionChunk(delta, ""), nil
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
		finishReason, err := anthropicToOpenAIFinishReason(p.stopReason)
		if err != nil {
			return nil, err
		}
		// Pass the finish reason to the constructor.
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
func (p *AnthropicStreamParser) constructOpenAIChatCompletionChunk(delta openai.ChatCompletionResponseChunkChoiceDelta, finishReason openai.ChatCompletionChoicesFinishReason) *openai.ChatCompletionResponseChunk {
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
