// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package anthropic

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"
)

// MessagesRequest represents a request to the Anthropic Messages API.
// https://docs.claude.com/en/api/messages
//
// Note that we currently only have "passthrough-ish" translators for Anthropic,
// so this struct only contains fields that are necessary for minimal processing
// as well as for observability purposes on a best-effort basis.
//
// Notably, round trip idempotency is not guaranteed when using this struct.
type MessagesRequest struct {
	// Model is the model to use for the request.
	Model string `json:"model"`

	// Messages is the list of messages in the conversation.
	// https://docs.claude.com/en/api/messages#body-messages
	Messages []MessageParam `json:"messages"`

	// MaxTokens is the maximum number of tokens to generate.
	// https://docs.claude.com/en/api/messages#body-max-tokens
	MaxTokens float64 `json:"max_tokens"`

	// Container identifier for reuse across requests.
	// https://docs.claude.com/en/api/messages#body-container
	Container *Container `json:"container,omitempty"`

	// ContextManagement is the context management configuration.
	// https://docs.claude.com/en/api/messages#body-context-management
	ContextManagement *ContextManagement `json:"context_management,omitempty"`

	// MCPServers is the list of MCP servers.
	// https://docs.claude.com/en/api/messages#body-mcp-servers
	MCPServers []MCPServer `json:"mcp_servers,omitempty"`

	// Metadata is the metadata for the request.
	// https://docs.claude.com/en/api/messages#body-metadata
	Metadata *MessagesMetadata `json:"metadata,omitempty"`

	// ServiceTier indicates the service tier for the request.
	// https://docs.claude.com/en/api/messages#body-service-tier
	ServiceTier *MessageServiceTier `json:"service_tier,omitempty"`

	// StopSequences is the list of stop sequences.
	// https://docs.claude.com/en/api/messages#body-stop-sequences
	StopSequences []string `json:"stop_sequences,omitempty"`

	// System is the system prompt to guide the model's behavior.
	// https://docs.claude.com/en/api/messages#body-system
	System *SystemPrompt `json:"system,omitempty"`

	// Temperature controls the randomness of the output.
	Temperature *float64 `json:"temperature,omitempty"`

	// Thinking is the configuration for the model's "thinking" behavior.
	// https://docs.claude.com/en/api/messages#body-thinking
	Thinking *Thinking `json:"thinking,omitempty"`

	// ToolChoice indicates the tool choice for the model.
	// https://docs.claude.com/en/api/messages#body-tool-choice
	ToolChoice *ToolChoice `json:"tool_choice,omitempty"`

	// Tools is the list of tools available to the model.
	// https://docs.claude.com/en/api/messages#body-tools
	Tools []Tool `json:"tools,omitempty"`

	// Stream indicates whether to stream the response.
	Stream bool `json:"stream,omitempty"`

	// TopP is the cumulative probability for nucleus sampling.
	TopP *float64 `json:"top_p,omitempty"`

	// TopK is the number of highest probability vocabulary tokens to keep for top-k-filtering.
	TopK *int `json:"top_k,omitempty"`
}

// MessageParam represents a single message in the Anthropic Messages API.
// https://platform.claude.com/docs/en/api/messages#message_param
type MessageParam struct {
	// Role is the role of the message. "user" or "assistant".
	Role MessageRole `json:"role"`

	// Content is the content of the message.
	Content MessageContent `json:"content"`
}

// MessageRole represents the role of a message in the Anthropic Messages API.
// https://docs.claude.com/en/api/messages#body-messages-role
type MessageRole string

const (
	MessageRoleUser      MessageRole = "user"
	MessageRoleAssistant MessageRole = "assistant"
)

// MessageContent represents the content of a message in the Anthropic Messages API.
// https://docs.claude.com/en/api/messages#body-messages-content
type MessageContent struct {
	Text  string              // Non-empty if this is not array content.
	Array []ContentBlockParam // Non-empty if this is array content.
}

func (m *MessageContent) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as string first.
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		m.Text = text
		return nil
	}

	// Try to unmarshal as array of MessageContentArrayElement.
	var array []ContentBlockParam
	if err := json.Unmarshal(data, &array); err == nil {
		m.Array = array
		return nil
	}
	return fmt.Errorf("message content must be either string or array")
}

func (m *MessageContent) MarshalJSON() ([]byte, error) {
	if m.Text != "" {
		return json.Marshal(m.Text)
	}
	if len(m.Array) > 0 {
		return json.Marshal(m.Array)
	}
	return nil, fmt.Errorf("message content must have either text or array")
}

type (
	// ContentBlockParam represents an element of the array content in a message.
	// https://docs.claude.com/en/api/messages#body-messages-content
	ContentBlockParam struct {
		Text *TextBlockParam
		// TODO add others when we need it for observability, etc.
	}

	TextBlockParam struct {
		Text         string `json:"text"`
		Type         string `json:"type"` // Always "text".
		CacheControl any    `json:"cache_control,omitempty"`
		Citations    []any  `json:"citations,omitempty"`
	}
)

func (m *ContentBlockParam) UnmarshalJSON(data []byte) error {
	typ := gjson.GetBytes(data, "type")
	if !typ.Exists() {
		return errors.New("missing type field in message content block")
	}
	switch typ.String() {
	case "text":
		var textBlock TextBlockParam
		if err := json.Unmarshal(data, &textBlock); err != nil {
			return fmt.Errorf("failed to unmarshal text block: %w", err)
		}
		m.Text = &textBlock
		return nil
	default:
		// TODO add others when we need it for observability, etc.
		// Fow now, we ignore undefined types.
		return nil
	}
}

func (m *ContentBlockParam) MarshalJSON() ([]byte, error) {
	if m.Text != nil {
		return json.Marshal(m.Text)
	}
	// TODO add others when we need it for observability, etc.
	return nil, fmt.Errorf("content block must have a defined type")
}

// MessagesMetadata represents the metadata for the Anthropic Messages API request.
// https://docs.claude.com/en/api/messages#body-metadata
type MessagesMetadata struct {
	// UserID is an optional user identifier for tracking purposes.
	UserID *string `json:"user_id,omitempty"`
}

// MessageServiceTier represents the service tier for the Anthropic Messages API request.
//
// https://docs.claude.com/en/api/messages#body-service-tier
type MessageServiceTier string

const (
	MessageServiceTierAuto         MessageServiceTier = "auto"
	MessageServiceTierStandardOnly MessageServiceTier = "standard_only"
)

// Container represents a container identifier for reuse across requests.
// https://docs.claude.com/en/api/messages#body-container
type Container any // TODO when we need it for observability, etc.

type (
	// ToolUnion represents a tool available to the model.
	// https://platform.claude.com/docs/en/api/messages#tool_union
	ToolUnion struct {
		Tool *Tool
		// TODO when we need it for observability, etc.
	}
	Tool struct {
		Type         string          `json:"type"` // Always "custom".
		Name         string          `json:"name"`
		InputSchema  ToolInputSchema `json:"input_schema"`
		CacheControl any             `json:"cache_schema,omitempty"`
		Description  string          `json:"description,omitempty"`
	}

	ToolInputSchema struct {
		Type       string         `json:"type"` // Always "object".
		Properties map[string]any `json:"properties,omitempty"`
		Required   []string       `json:"required,omitempty"`
	}
)

func (t *ToolUnion) UnmarshalJSON(data []byte) error {
	typ := gjson.GetBytes(data, "type")
	if !typ.Exists() {
		return errors.New("missing type field in tool")
	}
	switch typ.String() {
	case "custom":
		var tool Tool
		if err := json.Unmarshal(data, &tool); err != nil {
			return fmt.Errorf("failed to unmarshal tool: %w", err)
		}
		t.Tool = &tool
		return nil
	default:
		// TODO add others when we need it for observability, etc.
		// Fow now, we ignore undefined types.
		return nil
	}
}

// ToolChoice represents the tool choice for the model.
// https://docs.claude.com/en/api/messages#body-tool-choice
type ToolChoice any // TODO when we need it for observability, etc.

// Thinking represents the configuration for the model's "thinking" behavior.
// https://docs.claude.com/en/api/messages#body-thinking
type Thinking any // TODO when we need it for observability, etc.

// SystemPrompt represents a system prompt to guide the model's behavior.
// https://docs.claude.com/en/api/messages#body-system
type SystemPrompt struct {
	Text  string
	Texts []TextBlockParam
}

func (s *SystemPrompt) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as string first.
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		s.Text = text
		return nil
	}

	// Try to unmarshal as array of TextBlockParam.
	var texts []TextBlockParam
	if err := json.Unmarshal(data, &texts); err == nil {
		s.Texts = texts
		return nil
	}
	return fmt.Errorf("system prompt must be either string or array of text blocks")
}

func (s *SystemPrompt) MarshalJSON() ([]byte, error) {
	if s.Text != "" {
		return json.Marshal(s.Text)
	}
	if len(s.Texts) > 0 {
		return json.Marshal(s.Texts)
	}
	return nil, fmt.Errorf("system prompt must have either text or texts")
}

// MCPServer represents an MCP server.
// https://docs.claude.com/en/api/messages#body-mcp-servers
type MCPServer any // TODO when we need it for observability, etc.

// ContextManagement represents the context management configuration.
// https://docs.claude.com/en/api/messages#body-context-management
type ContextManagement any // TODO when we need it for observability, etc.

// MessagesResponse represents a response from the Anthropic Messages API.
// https://docs.claude.com/en/api/messages
type MessagesResponse struct {
	// ID is the unique identifier for the response.
	// https://docs.claude.com/en/api/messages#response-id
	ID string `json:"id"`
	// Type is the type of the response.
	// This is always "messages".
	//
	// https://docs.claude.com/en/api/messages#response-type
	Type ConstantMessagesResponseTypeMessages `json:"type"`
	// Role is the role of the message in the response.
	// This is always "assistant".
	//
	// https://docs.claude.com/en/api/messages#response-role
	Role ConstantMessagesResponseRoleAssistant `json:"role"`
	// Content is the content of the message in the response.
	// https://docs.claude.com/en/api/messages#response-content
	Content []MessagesContentBlock `json:"content"`
	// Model is the model used for the response.
	// https://docs.claude.com/en/api/messages#response-model
	Model string `json:"model"`
	// StopReason is the reason for stopping the generation.
	// https://docs.claude.com/en/api/messages#response-stop-reason
	StopReason *StopReason `json:"stop_reason,omitempty"`
	// StopSequence is the stop sequence that was encountered.
	// https://docs.claude.com/en/api/messages#response-stop-sequence
	StopSequence *string `json:"stop_sequence,omitempty"`
	// Usage contains token usage information for the response.
	// https://docs.claude.com/en/api/messages#response-usage
	Usage *Usage `json:"usage,omitempty"`
}

// ConstantMessagesResponseTypeMessages is the constant type for MessagesResponse, which is always "messages".
type ConstantMessagesResponseTypeMessages string

// ConstantMessagesResponseRoleAssistant is the constant role for MessagesResponse, which is always "assistant".
type ConstantMessagesResponseRoleAssistant string

type (
	// MessagesContentBlock represents a block of content in the Anthropic Messages API response.
	// https://docs.claude.com/en/api/messages#response-content
	MessagesContentBlock struct {
		Text     *TextBlock
		Tool     *ToolUseBlock
		Thinking *ThinkingBlock
		// TODO when we need it for observability, etc.
	}

	TextBlock struct {
		Type string `json:"type"` // Always "text".
		Text string `json:"text"`
		// TODO: citation?
	}

	ToolUseBlock struct {
		Type  string         `json:"type"` // Always "tool_use".
		ID    string         `json:"id"`
		Name  string         `json:"name"`
		Input map[string]any `json:"input"`
	}

	ThinkingBlock struct {
		Type      string `json:"type"` // Always "thinking".
		Thinking  string `json:"thinking"`
		Signature string `json:"signature,omitempty"`
	}
)

func (m *MessagesContentBlock) UnmarshalJSON(data []byte) error {
	typ := gjson.GetBytes(data, "type")
	if !typ.Exists() {
		return errors.New("missing type field in message content block")
	}
	switch typ.String() {
	case "text":
		var textBlock TextBlock
		if err := json.Unmarshal(data, &textBlock); err != nil {
			return fmt.Errorf("failed to unmarshal text block: %w", err)
		}
		m.Text = &textBlock
		return nil
	case "tool_use":
		var toolUseBlock ToolUseBlock
		if err := json.Unmarshal(data, &toolUseBlock); err != nil {
			return fmt.Errorf("failed to unmarshal tool use block: %w", err)
		}
		m.Tool = &toolUseBlock
		return nil
	case "thinking":
		var thinkingBlock ThinkingBlock
		if err := json.Unmarshal(data, &thinkingBlock); err != nil {
			return fmt.Errorf("failed to unmarshal thinking block: %w", err)
		}
		m.Thinking = &thinkingBlock
		return nil
	default:
		// TODO add others when we need it for observability, etc.
		// Fow now, we ignore undefined types.
		return nil
	}
}

func (m *MessagesContentBlock) MarshalJSON() ([]byte, error) {
	if m.Text != nil {
		return json.Marshal(m.Text)
	}
	if m.Tool != nil {
		return json.Marshal(m.Tool)
	}
	if m.Thinking != nil {
		return json.Marshal(m.Thinking)
	}
	// TODO add others when we need it for observability, etc.
	return nil, fmt.Errorf("content block must have a defined type")
}

// StopReason represents the reason for stopping the generation.
// https://docs.claude.com/en/api/messages#response-stop-reason
type StopReason string

const (
	StopReasonEndTurn                    StopReason = "end_turn"
	StopReasonMaxTokens                  StopReason = "max_tokens"
	StopReasonStopSequence               StopReason = "stop_sequence"
	StopReasonToolUse                    StopReason = "tool_use"
	StopReasonPauseTurn                  StopReason = "pause_turn"
	StopReasonRefusal                    StopReason = "refusal"
	StopReasonModelContextWindowExceeded StopReason = "model_context_window_exceeded"
)

// Usage represents token usage information for the Anthropic Messages API response.
// https://docs.claude.com/en/api/messages#response-usage
//
// NOTE: all of them are float64 in the API, although they are always integers in practice.
// However, the documentation doesn't explicitly state that they are integers in its format,
// so we use float64 to be able to unmarshal both 1234 and 1234.0 without errors.
type Usage struct {
	// The number of input tokens used to create the cache entry.
	CacheCreationInputTokens float64 `json:"cache_creation_input_tokens"`
	// The number of input tokens read from the cache.
	CacheReadInputTokens float64 `json:"cache_read_input_tokens"`
	// The number of input tokens which were used.
	InputTokens float64 `json:"input_tokens"`
	// The number of output tokens which were used.
	OutputTokens float64 `json:"output_tokens"`
}

// MessagesStreamEvent represents a single event in the streaming response from the Anthropic Messages API.
// https://docs.claude.com/en/docs/build-with-claude/streaming
type MessagesStreamEvent struct {
	// The type of the streaming event.
	Type MessagesStreamEventType `json:"type"`
	// MessageStart is present if the event type is "message_start" or "message_delta".
	MessageStart *MessagesStreamEventMessageStart
	// MessageDelta is present if the event type is "message_delta".
	MessageDelta *MessagesStreamEventMessageDelta
	// MessageStop is present if the event type is "message_stop".
	MessageStop *MessagesStreamEventMessageStop
	// ContentBlockStart is present if the event type is "content_block_start".
	ContentBlockStart *MessagesStreamEventContentBlockStart
	// ContentBlockDelta is present if the event type is "content_block_delta".
	ContentBlockDelta *MessagesStreamEventContentBlockDelta
	// ContentBlockStop is present if the event type is "content_block_stop".
	ContentBlockStop *MessagesStreamEventContentBlockStop
}

// MessagesStreamEventType represents the type of a streaming event in the Anthropic Messages API.
// https://docs.claude.com/en/docs/build-with-claude/streaming#event-types
type MessagesStreamEventType string

const (
	MessagesStreamEventTypeMessageStart      MessagesStreamEventType = "message_start"
	MessagesStreamEventTypeMessageDelta      MessagesStreamEventType = "message_delta"
	MessagesStreamEventTypeMessageStop       MessagesStreamEventType = "message_stop"
	MessagesStreamEventTypeContentBlockStart MessagesStreamEventType = "content_block_start"
	MessagesStreamEventTypeContentBlockDelta MessagesStreamEventType = "content_block_delta"
	MessagesStreamEventTypeContentBlockStop  MessagesStreamEventType = "content_block_stop"
)

type (
	// MessagesStreamEventMessageStart represents the message content in a "message_start".
	MessagesStreamEventMessageStart MessagesResponse
	// MessagesStreamEventMessageStop represents the message content in a "message_stop".
	MessagesStreamEventMessageStop struct {
		Type MessagesStreamEventType `json:"type"` // Type is always "message_stop".
	}
	// MessagesStreamEventContentBlockStart represents the content block in a "content_block_start".
	MessagesStreamEventContentBlockStart struct {
		Type         MessagesStreamEventType `json:"type"` // Type is always "content_block_start".
		Index        int                     `json:"index"`
		ContentBlock MessagesContentBlock    `json:"content_block"`
	}
	// MessagesStreamEventContentBlockDelta represents the content block delta in a "content_block_delta".
	MessagesStreamEventContentBlockDelta struct {
		Type  MessagesStreamEventType `json:"type"` // Type is always "content_block_delta".
		Index int                     `json:"index"`
		Delta ContentBlockDelta       `json:"delta"`
	}
	// MessagesStreamEventContentBlockStop represents the content block in a "content_block_stop".
	MessagesStreamEventContentBlockStop struct {
		Type  MessagesStreamEventType `json:"type"` // Type is always "content_block_stop".
		Index int                     `json:"index"`
	}
	// MessagesStreamEventMessageDelta represents the message content in a "message_delta".
	//
	// Note: the definition of this event is vague in the Anthropic documentation.
	// This follows the same code from their official SDK.
	// https://github.com/anthropics/anthropic-sdk-go/blob/3a0275d6034e4eda9fbc8366d8a5d8b3a462b4cc/message.go#L2424-L2451
	MessagesStreamEventMessageDelta struct {
		Type MessagesStreamEventType `json:"type"` // Type is always "message_delta".
		// Delta contains the delta information for the message.
		// This is cumulative per documentation.
		Usage Usage                                `json:"usage"`
		Delta MessagesStreamEventMessageDeltaDelta `json:"delta"`
	}
)

type MessagesStreamEventMessageDeltaDelta struct {
	StopReason   StopReason `json:"stop_reason"`
	StopSequence string     `json:"stop_sequence"`
}

type ContentBlockDelta struct {
	Type        string `json:"type"`
	Text        string `json:"text,omitempty"`
	PartialJSON string `json:"partial_json,omitempty"`
	Thinking    string `json:"thinking,omitempty"`
	Signature   string `json:"signature,omitempty"`
}

func (m *MessagesStreamEvent) UnmarshalJSON(data []byte) error {
	eventType := gjson.GetBytes(data, "type")
	if !eventType.Exists() {
		return fmt.Errorf("missing type field in stream event")
	}
	m.Type = MessagesStreamEventType(eventType.String())
	switch m.Type {
	case MessagesStreamEventTypeMessageStart:
		messageBytes := gjson.GetBytes(data, "message")
		r := strings.NewReader(messageBytes.Raw)
		decoder := json.NewDecoder(r)
		var message MessagesStreamEventMessageStart
		if err := decoder.Decode(&message); err != nil {
			return fmt.Errorf("failed to unmarshal message in stream event: %w", err)
		}
		m.MessageStart = &message
	case MessagesStreamEventTypeMessageDelta:
		var messageDelta MessagesStreamEventMessageDelta
		if err := json.Unmarshal(data, &messageDelta); err != nil {
			return fmt.Errorf("failed to unmarshal message delta in stream event: %w", err)
		}
		m.MessageDelta = &messageDelta
	case MessagesStreamEventTypeMessageStop:
		var messageStop MessagesStreamEventMessageStop
		if err := json.Unmarshal(data, &messageStop); err != nil {
			return fmt.Errorf("failed to unmarshal message stop in stream event: %w", err)
		}
		m.MessageStop = &messageStop
	case MessagesStreamEventTypeContentBlockStart:
		var contentBlockStart MessagesStreamEventContentBlockStart
		if err := json.Unmarshal(data, &contentBlockStart); err != nil {
			return fmt.Errorf("failed to unmarshal content block start in stream event: %w", err)
		}
		m.ContentBlockStart = &contentBlockStart
	case MessagesStreamEventTypeContentBlockDelta:
		var contentBlockDelta MessagesStreamEventContentBlockDelta
		if err := json.Unmarshal(data, &contentBlockDelta); err != nil {
			return fmt.Errorf("failed to unmarshal content block delta in stream event: %w", err)
		}
		m.ContentBlockDelta = &contentBlockDelta
	case MessagesStreamEventTypeContentBlockStop:
		var contentBlockStop MessagesStreamEventContentBlockStop
		if err := json.Unmarshal(data, &contentBlockStop); err != nil {
			return fmt.Errorf("failed to unmarshal content block stop in stream event: %w", err)
		}
		m.ContentBlockStop = &contentBlockStop
	default:
		return fmt.Errorf("unknown stream event type: %s", m.Type)
	}
	return nil
}
