// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// SSEEvent represents a Server-Sent Event.
type SSEEvent struct {
	Event string
	Data  string
	ID    string
	Retry string
}

// ChatCompletionChunk represents a chunk from the OpenAI streaming API.
type ChatCompletionChunk struct {
	ID      string `json:"id"`
	Object  string `json:"object"`
	Created int64  `json:"created"`
	Model   string `json:"model"`
	Choices []struct {
		Index int `json:"index"`
		Delta struct {
			Role    string `json:"role,omitempty"`
			Content string `json:"content,omitempty"`
		} `json:"delta"`
		FinishReason string `json:"finish_reason,omitempty"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
		TotalTokens      int `json:"total_tokens"`
	} `json:"usage,omitempty"`
}

// SSEReader reads Server-Sent Events from an io.Reader.
type SSEReader struct {
	scanner *bufio.Scanner
}

// NewSSEReader creates a new SSE reader.
func NewSSEReader(r io.Reader) *SSEReader {
	return &SSEReader{
		scanner: bufio.NewScanner(r),
	}
}

// ReadEvent reads the next SSE event from the stream.
func (r *SSEReader) ReadEvent() (*SSEEvent, error) {
	event := &SSEEvent{}

	for r.scanner.Scan() {
		line := r.scanner.Text()

		// Empty line signals end of event.
		if line == "" {
			if event.Data != "" || event.Event != "" {
				return event, nil
			}
			continue
		}

		// Parse field.
		switch {
		case strings.HasPrefix(line, "data:"):
			event.Data = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		case strings.HasPrefix(line, "event:"):
			event.Event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		case strings.HasPrefix(line, "id:"):
			event.ID = strings.TrimSpace(strings.TrimPrefix(line, "id:"))
		case strings.HasPrefix(line, "retry:"):
			event.Retry = strings.TrimSpace(strings.TrimPrefix(line, "retry:"))
		}
	}

	if err := r.scanner.Err(); err != nil {
		return nil, err
	}

	// Check if we have a partial event at EOF.
	if event.Data != "" || event.Event != "" {
		return event, io.EOF
	}

	return nil, io.EOF
}

// ReadChatCompletionStream reads and parses OpenAI chat completion chunks from an SSE stream.
func ReadChatCompletionStream(r io.Reader) ([]ChatCompletionChunk, string, error) {
	reader := NewSSEReader(r)
	var chunks []ChatCompletionChunk
	var fullContent strings.Builder

	for {
		event, err := reader.ReadEvent()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("failed to read SSE event: %w", err)
		}

		// Skip empty events.
		if event.Data == "" {
			continue
		}

		// Check for end of stream.
		if event.Data == "[DONE]" {
			break
		}

		// Parse JSON chunk.
		var chunk ChatCompletionChunk
		if err := json.Unmarshal([]byte(event.Data), &chunk); err != nil {
			// Skip malformed chunks.
			continue
		}

		chunks = append(chunks, chunk)

		// Concatenate content from delta.
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta.Content != "" {
			fullContent.WriteString(chunk.Choices[0].Delta.Content)
		}
	}

	return chunks, fullContent.String(), nil
}

// ExtractTokenUsage extracts token usage from the chunks (usually in the last chunk with usage info).
func ExtractTokenUsage(chunks []ChatCompletionChunk) (promptTokens, completionTokens, totalTokens int) {
	// Look for usage information in chunks (usually in the last chunk).
	for i := len(chunks) - 1; i >= 0; i-- {
		if chunks[i].Usage != nil {
			return chunks[i].Usage.PromptTokens,
				chunks[i].Usage.CompletionTokens,
				chunks[i].Usage.TotalTokens
		}
	}
	return 0, 0, 0
}
