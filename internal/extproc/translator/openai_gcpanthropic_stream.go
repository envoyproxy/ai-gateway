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
	scanner := bufio.NewScanner(strings.NewReader(eventBlock))
	var eventType, eventData string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, sseEventPrefix) {
			eventType = strings.TrimSpace(strings.TrimPrefix(line, sseEventPrefix))
		} else if strings.HasPrefix(line, sseDataPrefix) {
			eventData += strings.TrimPrefix(line, sseDataPrefix)
		}
	}

	if eventType != "" {
		return p.handleAnthropicStreamEvent(eventType, []byte(eventData))
	}
	return nil, nil
}

func (p *AnthropicStreamParser) handleAnthropicStreamEvent(eventType string, data []byte) (*openai.ChatCompletionResponseChunk, error) {
	switch eventType {
	case "message_start":
		var event anthropic.MessageStartEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal message_start: %w", err)
		}
		p.activeMessageID = event.Message.ID
		p.tokenUsage.InputTokens = uint32(event.Message.Usage.InputTokens)
		return nil, nil

	case "content_block_delta":
		var event anthropic.ContentBlockDeltaEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal content_block_delta: %w", err)
		}
		if event.Type == "text_delta" {
			return p.constructOpenAIChatCompletionChunk(openai.ChatCompletionResponseChunkChoiceDelta{Content: &event.Delta.Text}, nil), nil
		}

	case "message_delta":
		var event anthropic.MessageDeltaEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal message_delta: %w", err)
		}
		p.tokenUsage.OutputTokens = uint32(event.Usage.OutputTokens)
		if event.Delta.StopReason != "" {
			p.stopReason = event.Delta.StopReason
		}

	case "content_block_start":
		var jsonData []byte
		var event anthropic.ContentBlockStartEvent
		if err := json.Unmarshal(jsonData, &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal content_block_start: %w", err)
		}
		if event.ContentBlock.Type == "tool_use" {
			// TODO: update index.
			p.activeToolCalls[0] = &streamingToolCall{
				ID:   event.ContentBlock.ID,
				Name: event.ContentBlock.Name,
			}
		}
		return nil, nil
	case "message_stop":
		finishReason, err := anthropicToOpenAIFinishReason(p.stopReason)
		if err != nil {
			return nil, err
		}
		// Pass the finish reason to the constructor.
		return p.constructOpenAIChatCompletionChunk(openai.ChatCompletionResponseChunkChoiceDelta{}, &finishReason), nil

	case "error":
		var errEvent anthropic.ErrorResponse
		if err := json.Unmarshal(data, &errEvent); err != nil {
			return nil, fmt.Errorf("unparsable error event: %s", string(data))
		}
		return nil, fmt.Errorf("anthropic stream error: %s - %s", errEvent.Error.Type, errEvent.Error.Message)
	}
	return nil, nil
}

// constructOpenAIChatCompletionChunk correctly builds the chunk.
func (p *AnthropicStreamParser) constructOpenAIChatCompletionChunk(delta openai.ChatCompletionResponseChunkChoiceDelta, finishReason *openai.ChatCompletionChoicesFinishReason) *openai.ChatCompletionResponseChunk {
	return &openai.ChatCompletionResponseChunk{
		Object: "chat.completion.chunk",
		Choices: []openai.ChatCompletionResponseChunkChoice{
			{
				Delta:        &delta,
				FinishReason: *finishReason,
			},
		},
	}
}
