// Package openai contains the following are the OpenAI API schema definitions.
// Note that we intentionally do not use the code generation tools like OpenAPI Generator not only to keep the code simple
// but also because the OpenAI's OpenAPI definition is not compliant with the spec and the existing tools do not work well.
package openai

import (
	"encoding/json"
	"strings"
)

// ChatCompletionRequest represents a request to /v1/chat/completions.
// https://platform.openai.com/docs/api-reference/chat/create
type ChatCompletionRequest struct {
	// Model is described in the OpenAI API documentation:
	// https://platform.openai.com/docs/api-reference/chat/create#chat-create-model
	Model string `json:"model"`

	// Messages is described in the OpenAI API documentation:
	// https://platform.openai.com/docs/api-reference/chat/create#chat-create-messages
	Messages []ChatCompletionRequestMessage `json:"messages"`

	// Stream is described in the OpenAI API documentation:
	// https://platform.openai.com/docs/api-reference/chat/create#chat-create-stream
	Stream bool `json:"stream,omitempty"`
}

// ChatCompletionRequestMessage represents a message in a ChatCompletionRequest.
// https://platform.openai.com/docs/api-reference/chat/create#chat-create-messages
type ChatCompletionRequestMessage struct {
	// Role is the role of the message. The role of the message (whether it represents the user or the AI).
	Role string `json:"role,omitempty"`
	// Content is the content of the message.
	Content any `json:"content,omitempty"`
}

// ChatCompletionResponse represents a response from /v1/chat/completions.
// https://platform.openai.com/docs/api-reference/chat/object
type ChatCompletionResponse struct {
	// Choices are described in the OpenAI API documentation:
	// https://platform.openai.com/docs/api-reference/chat/object#chat/object-choices
	Choices []ChatCompletionResponseChoice `json:"choices,omitempty"`

	// Object is always "chat.completion" for completions.
	// https://platform.openai.com/docs/api-reference/chat/object#chat/object-object
	Object string `json:"object,omitempty"`

	// Usage is described in the OpenAI API documentation:
	// https://platform.openai.com/docs/api-reference/chat/object#chat/object-usage
	Usage ChatCompletionResponseUsage `json:"usage,omitempty"`
}

// ChatCompletionResponseChoice is described in the OpenAI API documentation:
// https://platform.openai.com/docs/api-reference/chat/object#chat/object-choices
type ChatCompletionResponseChoice struct {
	// Message is described in the OpenAI API documentation:
	// https://platform.openai.com/docs/api-reference/chat/object#chat/object-choices
	Message ChatCompletionResponseChoiceMessage `json:"message,omitempty"`
}

// ChatCompletionResponseChoiceMessage is described in the OpenAI API documentation:
// https://platform.openai.com/docs/api-reference/chat/object#chat/object-choices
type ChatCompletionResponseChoiceMessage struct {
	Content *string `json:"content,omitempty"`
	Role    string  `json:"role,omitempty"`
}

// ChatCompletionResponseUsage is described in the OpenAI API documentation:
// https://platform.openai.com/docs/api-reference/chat/object#chat/object-usage
type ChatCompletionResponseUsage struct {
	CompletionTokens int `json:"completion_tokens,omitempty"`
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

// ChatCompletionResponseChunk is described in the OpenAI API documentation:
// https://platform.openai.com/docs/api-reference/chat/streaming#chat-create-messages
type ChatCompletionResponseChunk struct {
	// Choices are described in the OpenAI API documentation:
	// https://platform.openai.com/docs/api-reference/chat/streaming#chat/streaming-choices
	Choices []ChatCompletionResponseChunkChoice `json:"choices,omitempty"`

	// Object is always "chat.completion.chunk" for completions.
	// https://platform.openai.com/docs/api-reference/chat/streaming#chat/streaming-object
	Object string `json:"object,omitempty"`

	// Usage is described in the OpenAI API documentation:
	// https://platform.openai.com/docs/api-reference/chat/streaming#chat/streaming-usage
	Usage *ChatCompletionResponseUsage `json:"usage,omitempty"`
}

// String implements fmt.Stringer.
func (c *ChatCompletionResponseChunk) String() string {
	buf, _ := json.Marshal(c)
	return strings.ReplaceAll(string(buf), ",", ", ")
}

// ChatCompletionResponseChunkChoice is described in the OpenAI API documentation:
// https://platform.openai.com/docs/api-reference/chat/streaming#chat/streaming-choices
type ChatCompletionResponseChunkChoice struct {
	Delta *ChatCompletionResponseChunkChoiceDelta `json:"delta,omitempty"`
}

// ChatCompletionResponseChunkChoiceDelta is described in the OpenAI API documentation:
// https://platform.openai.com/docs/api-reference/chat/streaming#chat/streaming-choices
type ChatCompletionResponseChunkChoiceDelta struct {
	Content *string `json:"content,omitempty"`
	Role    *string `json:"role,omitempty"`
}
