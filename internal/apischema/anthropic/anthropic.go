// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package anthropic

import (
	"errors"
	"fmt"
	"strings"

	"github.com/tidwall/gjson"

	"github.com/envoyproxy/ai-gateway/internal/json"
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
	Tools []ToolUnion `json:"tools,omitempty"`

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
	return fmt.Errorf("message content must be either text or array")
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
	// https://platform.claude.com/docs/en/api/messages#body-messages-content
	ContentBlockParam struct {
		Text                *TextBlockParam
		Image               *ImageBlockParam
		Document            *DocumentBlockParam
		SearchResult        *SearchResultBlockParam
		Thinking            *ThinkingBlockParam
		RedactedThinking    *RedactedThinkingBlockParam
		ToolUse             *ToolUseBlockParam
		ToolResult          *ToolResultBlockParam
		ServerToolUse       *ServerToolUseBlockParam
		WebSearchToolResult *WebSearchToolResultBlockParam
	}

	// TextBlockParam represents a text content block.
	// https://platform.claude.com/docs/en/api/messages#text_block_param
	TextBlockParam struct {
		Text         string `json:"text"`
		Type         string `json:"type"` // Always "text".
		CacheControl any    `json:"cache_control,omitempty"`
		Citations    []any  `json:"citations,omitempty"`
	}

	// ImageBlockParam represents an image content block.
	// https://platform.claude.com/docs/en/api/messages#image_block_param
	ImageBlockParam struct {
		Type         string `json:"type"` // Always "image".
		Source       any    `json:"source"`
		CacheControl any    `json:"cache_control,omitempty"`
	}

	// DocumentBlockParam represents a document content block.
	// https://platform.claude.com/docs/en/api/messages#document_block_param
	DocumentBlockParam struct {
		Type         string `json:"type"` // Always "document".
		Source       any    `json:"source"`
		CacheControl any    `json:"cache_control,omitempty"`
		Citations    any    `json:"citations,omitempty"`
		Context      string `json:"context,omitempty"`
		Title        string `json:"title,omitempty"`
	}

	// SearchResultBlockParam represents a search result content block.
	// https://platform.claude.com/docs/en/api/messages#search_result_block_param
	SearchResultBlockParam struct {
		Type         string           `json:"type"` // Always "search_result".
		Content      []TextBlockParam `json:"content"`
		Source       string           `json:"source"`
		Title        string           `json:"title"`
		CacheControl any              `json:"cache_control,omitempty"`
		Citations    any              `json:"citations,omitempty"`
	}

	// ThinkingBlockParam represents a thinking content block in a request.
	// https://platform.claude.com/docs/en/api/messages#thinking_block_param
	ThinkingBlockParam struct {
		Type      string `json:"type"` // Always "thinking".
		Thinking  string `json:"thinking"`
		Signature string `json:"signature"`
	}

	// RedactedThinkingBlockParam represents a redacted thinking content block.
	// https://platform.claude.com/docs/en/api/messages#redacted_thinking_block_param
	RedactedThinkingBlockParam struct {
		Type string `json:"type"` // Always "redacted_thinking".
		Data string `json:"data"`
	}

	// ToolUseBlockParam represents a tool use content block in a request.
	// https://platform.claude.com/docs/en/api/messages#tool_use_block_param
	ToolUseBlockParam struct {
		Type         string         `json:"type"` // Always "tool_use".
		ID           string         `json:"id"`
		Name         string         `json:"name"`
		Input        map[string]any `json:"input"`
		CacheControl any            `json:"cache_control,omitempty"`
	}

	// ToolResultBlockParam represents a tool result content block.
	// https://platform.claude.com/docs/en/api/messages#tool_result_block_param
	ToolResultBlockParam struct {
		Type         string `json:"type"` // Always "tool_result".
		ToolUseID    string `json:"tool_use_id"`
		Content      any    `json:"content,omitempty"` // string or array of content blocks.
		IsError      bool   `json:"is_error,omitempty"`
		CacheControl any    `json:"cache_control,omitempty"`
	}

	// ServerToolUseBlockParam represents a server tool use content block.
	// https://platform.claude.com/docs/en/api/messages#server_tool_use_block_param
	ServerToolUseBlockParam struct {
		Type         string         `json:"type"` // Always "server_tool_use".
		ID           string         `json:"id"`
		Name         string         `json:"name"`
		Input        map[string]any `json:"input"`
		CacheControl any            `json:"cache_control,omitempty"`
	}

	// WebSearchToolResultBlockParam represents a web search tool result content block.
	// https://platform.claude.com/docs/en/api/messages#web_search_tool_result_block_param
	WebSearchToolResultBlockParam struct {
		Type         string `json:"type"` // Always "web_search_tool_result".
		ToolUseID    string `json:"tool_use_id"`
		Content      any    `json:"content"`
		CacheControl any    `json:"cache_control,omitempty"`
	}
)

func (m *ContentBlockParam) UnmarshalJSON(data []byte) error {
	typ := gjson.GetBytes(data, "type")
	if !typ.Exists() {
		return errors.New("missing type field in message content block")
	}
	switch typ.String() {
	case "text":
		var block TextBlockParam
		if err := json.Unmarshal(data, &block); err != nil {
			return fmt.Errorf("failed to unmarshal text block: %w", err)
		}
		m.Text = &block
	case "image":
		var block ImageBlockParam
		if err := json.Unmarshal(data, &block); err != nil {
			return fmt.Errorf("failed to unmarshal image block: %w", err)
		}
		m.Image = &block
	case "document":
		var block DocumentBlockParam
		if err := json.Unmarshal(data, &block); err != nil {
			return fmt.Errorf("failed to unmarshal document block: %w", err)
		}
		m.Document = &block
	case "search_result":
		var block SearchResultBlockParam
		if err := json.Unmarshal(data, &block); err != nil {
			return fmt.Errorf("failed to unmarshal search result block: %w", err)
		}
		m.SearchResult = &block
	case "thinking":
		var block ThinkingBlockParam
		if err := json.Unmarshal(data, &block); err != nil {
			return fmt.Errorf("failed to unmarshal thinking block: %w", err)
		}
		m.Thinking = &block
	case "redacted_thinking":
		var block RedactedThinkingBlockParam
		if err := json.Unmarshal(data, &block); err != nil {
			return fmt.Errorf("failed to unmarshal redacted thinking block: %w", err)
		}
		m.RedactedThinking = &block
	case "tool_use":
		var block ToolUseBlockParam
		if err := json.Unmarshal(data, &block); err != nil {
			return fmt.Errorf("failed to unmarshal tool use block: %w", err)
		}
		m.ToolUse = &block
	case "tool_result":
		var block ToolResultBlockParam
		if err := json.Unmarshal(data, &block); err != nil {
			return fmt.Errorf("failed to unmarshal tool result block: %w", err)
		}
		m.ToolResult = &block
	case "server_tool_use":
		var block ServerToolUseBlockParam
		if err := json.Unmarshal(data, &block); err != nil {
			return fmt.Errorf("failed to unmarshal server tool use block: %w", err)
		}
		m.ServerToolUse = &block
	case "web_search_tool_result":
		var block WebSearchToolResultBlockParam
		if err := json.Unmarshal(data, &block); err != nil {
			return fmt.Errorf("failed to unmarshal web search tool result block: %w", err)
		}
		m.WebSearchToolResult = &block
	default:
		// Ignore unknown types for forward compatibility.
		return nil
	}
	return nil
}

func (m *ContentBlockParam) MarshalJSON() ([]byte, error) {
	if m.Text != nil {
		return json.Marshal(m.Text)
	}
	if m.Image != nil {
		return json.Marshal(m.Image)
	}
	if m.Document != nil {
		return json.Marshal(m.Document)
	}
	if m.SearchResult != nil {
		return json.Marshal(m.SearchResult)
	}
	if m.Thinking != nil {
		return json.Marshal(m.Thinking)
	}
	if m.RedactedThinking != nil {
		return json.Marshal(m.RedactedThinking)
	}
	if m.ToolUse != nil {
		return json.Marshal(m.ToolUse)
	}
	if m.ToolResult != nil {
		return json.Marshal(m.ToolResult)
	}
	if m.ServerToolUse != nil {
		return json.Marshal(m.ServerToolUse)
	}
	if m.WebSearchToolResult != nil {
		return json.Marshal(m.WebSearchToolResult)
	}
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
// This became a beta status so it is not implemented for now.
// https://platform.claude.com/docs/en/api/beta/messages/create
type Container any // TODO when we need it for observability, etc.

type (
	// ToolUnion represents a tool available to the model.
	// https://platform.claude.com/docs/en/api/messages#tool_union
	ToolUnion struct {
		Tool                   *Tool
		BashTool               *BashTool
		TextEditorTool20250124 *TextEditorTool20250124
		TextEditorTool20250429 *TextEditorTool20250429
		TextEditorTool20250728 *TextEditorTool20250728
		WebSearchTool          *WebSearchTool
	}

	// Tool represents a custom tool definition.
	// https://platform.claude.com/docs/en/api/messages#tool
	Tool struct {
		Type         string          `json:"type"` // Always "custom".
		Name         string          `json:"name"`
		InputSchema  ToolInputSchema `json:"input_schema"`
		CacheControl any             `json:"cache_control,omitempty"`
		Description  string          `json:"description,omitempty"`
	}

	// BashTool represents the bash tool for computer use.
	// https://platform.claude.com/docs/en/api/messages#tool_bash_20250124
	BashTool struct {
		Type         string `json:"type"` // Always "bash_20250124".
		Name         string `json:"name"` // Always "bash".
		CacheControl any    `json:"cache_control,omitempty"`
	}

	// TextEditorTool20250124 represents the text editor tool (v1).
	// https://platform.claude.com/docs/en/api/messages#tool_text_editor_20250124
	TextEditorTool20250124 struct {
		Type         string `json:"type"` // Always "text_editor_20250124".
		Name         string `json:"name"` // Always "str_replace_editor".
		CacheControl any    `json:"cache_control,omitempty"`
	}

	// TextEditorTool20250429 represents the text editor tool (v2).
	// https://platform.claude.com/docs/en/api/messages#tool_text_editor_20250429
	TextEditorTool20250429 struct {
		Type         string `json:"type"` // Always "text_editor_20250429".
		Name         string `json:"name"` // Always "str_replace_based_edit_tool".
		CacheControl any    `json:"cache_control,omitempty"`
	}

	// TextEditorTool20250728 represents the text editor tool (v3).
	// https://platform.claude.com/docs/en/api/messages#tool_text_editor_20250728
	TextEditorTool20250728 struct {
		Type          string   `json:"type"` // Always "text_editor_20250728".
		Name          string   `json:"name"` // Always "str_replace_based_edit_tool".
		MaxCharacters *float64 `json:"max_characters,omitempty"`
		CacheControl  any      `json:"cache_control,omitempty"`
	}

	// WebSearchTool represents the web search tool.
	// https://platform.claude.com/docs/en/api/messages#web_search_tool_20250305
	WebSearchTool struct {
		Type           string             `json:"type"` // Always "web_search_20250305".
		Name           string             `json:"name"` // Always "web_search".
		AllowedDomains []string           `json:"allowed_domains,omitempty"`
		BlockedDomains []string           `json:"blocked_domains,omitempty"`
		MaxUses        *float64           `json:"max_uses,omitempty"`
		UserLocation   *WebSearchLocation `json:"user_location,omitempty"`
		CacheControl   any                `json:"cache_control,omitempty"`
	}

	// WebSearchLocation represents the user location for the web search tool.
	WebSearchLocation struct {
		Type     string `json:"type"` // Always "approximate".
		City     string `json:"city,omitempty"`
		Country  string `json:"country,omitempty"`
		Region   string `json:"region,omitempty"`
		Timezone string `json:"timezone,omitempty"`
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
	case "bash_20250124":
		var tool BashTool
		if err := json.Unmarshal(data, &tool); err != nil {
			return fmt.Errorf("failed to unmarshal bash tool: %w", err)
		}
		t.BashTool = &tool
	case "text_editor_20250124":
		var tool TextEditorTool20250124
		if err := json.Unmarshal(data, &tool); err != nil {
			return fmt.Errorf("failed to unmarshal text editor tool: %w", err)
		}
		t.TextEditorTool20250124 = &tool
	case "text_editor_20250429":
		var tool TextEditorTool20250429
		if err := json.Unmarshal(data, &tool); err != nil {
			return fmt.Errorf("failed to unmarshal text editor tool: %w", err)
		}
		t.TextEditorTool20250429 = &tool
	case "text_editor_20250728":
		var tool TextEditorTool20250728
		if err := json.Unmarshal(data, &tool); err != nil {
			return fmt.Errorf("failed to unmarshal text editor tool: %w", err)
		}
		t.TextEditorTool20250728 = &tool
	case "web_search_20250305":
		var tool WebSearchTool
		if err := json.Unmarshal(data, &tool); err != nil {
			return fmt.Errorf("failed to unmarshal web search tool: %w", err)
		}
		t.WebSearchTool = &tool
	default:
		// Ignore unknown types for forward compatibility.
		return nil
	}
	return nil
}

func (t *ToolUnion) MarshalJSON() ([]byte, error) {
	if t.Tool != nil {
		return json.Marshal(t.Tool)
	}
	if t.BashTool != nil {
		return json.Marshal(t.BashTool)
	}
	if t.TextEditorTool20250124 != nil {
		return json.Marshal(t.TextEditorTool20250124)
	}
	if t.TextEditorTool20250429 != nil {
		return json.Marshal(t.TextEditorTool20250429)
	}
	if t.TextEditorTool20250728 != nil {
		return json.Marshal(t.TextEditorTool20250728)
	}
	if t.WebSearchTool != nil {
		return json.Marshal(t.WebSearchTool)
	}
	return nil, fmt.Errorf("tool union must have a defined type")
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

// MessagesStreamChunk represents a single event in the streaming response from the Anthropic Messages API.
// https://docs.claude.com/en/docs/build-with-claude/streaming
type MessagesStreamChunk struct {
	Type MessagesStreamChunkType
	// MessageStart is present if the event type is "message_start" or "message_delta".
	MessageStart *MessagesStreamChunkMessageStart
	// MessageDelta is present if the event type is "message_delta".
	MessageDelta *MessagesStreamChunkMessageDelta
	// MessageStop is present if the event type is "message_stop".
	MessageStop *MessagesStreamChunkMessageStop
	// ContentBlockStart is present if the event type is "content_block_start".
	ContentBlockStart *MessagesStreamChunkContentBlockStart
	// ContentBlockDelta is present if the event type is "content_block_delta".
	ContentBlockDelta *MessagesStreamChunkContentBlockDelta
	// ContentBlockStop is present if the event type is "content_block_stop".
	ContentBlockStop *MessagesStreamChunkContentBlockStop
}

// MessagesStreamChunkType represents the type of a streaming event in the Anthropic Messages API.
// https://docs.claude.com/en/docs/build-with-claude/streaming#event-types
type MessagesStreamChunkType string

const (
	MessagesStreamChunkTypeMessageStart      MessagesStreamChunkType = "message_start"
	MessagesStreamChunkTypeMessageDelta      MessagesStreamChunkType = "message_delta"
	MessagesStreamChunkTypeMessageStop       MessagesStreamChunkType = "message_stop"
	MessagesStreamChunkTypeContentBlockStart MessagesStreamChunkType = "content_block_start"
	MessagesStreamChunkTypeContentBlockDelta MessagesStreamChunkType = "content_block_delta"
	MessagesStreamChunkTypeContentBlockStop  MessagesStreamChunkType = "content_block_stop"
)

type (
	// MessagesStreamChunkMessageStart represents the message content in a "message_start".
	MessagesStreamChunkMessageStart MessagesResponse
	// MessagesStreamChunkMessageStop represents the message content in a "message_stop".
	MessagesStreamChunkMessageStop struct {
		Type MessagesStreamChunkType `json:"type"` // Type is always "message_stop".
	}
	// MessagesStreamChunkContentBlockStart represents the content block in a "content_block_start".
	MessagesStreamChunkContentBlockStart struct {
		Type         MessagesStreamChunkType `json:"type"` // Type is always "content_block_start".
		Index        int                     `json:"index"`
		ContentBlock MessagesContentBlock    `json:"content_block"`
	}
	// MessagesStreamChunkContentBlockDelta represents the content block delta in a "content_block_delta".
	MessagesStreamChunkContentBlockDelta struct {
		Type  MessagesStreamChunkType `json:"type"` // Type is always "content_block_delta".
		Index int                     `json:"index"`
		Delta ContentBlockDelta       `json:"delta"`
	}
	// MessagesStreamChunkContentBlockStop represents the content block in a "content_block_stop".
	MessagesStreamChunkContentBlockStop struct {
		Type  MessagesStreamChunkType `json:"type"` // Type is always "content_block_stop".
		Index int                     `json:"index"`
	}
	// MessagesStreamChunkMessageDelta represents the message content in a "message_delta".
	//
	// Note: the definition of this event is vague in the Anthropic documentation.
	// This follows the same code from their official SDK.
	// https://github.com/anthropics/anthropic-sdk-go/blob/3a0275d6034e4eda9fbc8366d8a5d8b3a462b4cc/message.go#L2424-L2451
	MessagesStreamChunkMessageDelta struct {
		Type MessagesStreamChunkType `json:"type"` // Type is always "message_delta".
		// Delta contains the delta information for the message.
		// This is cumulative per documentation.
		Usage Usage                                `json:"usage"`
		Delta MessagesStreamChunkMessageDeltaDelta `json:"delta"`
	}
)

type MessagesStreamChunkMessageDeltaDelta struct {
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

func (m *MessagesStreamChunk) UnmarshalJSON(data []byte) error {
	eventType := gjson.GetBytes(data, "type")
	if !eventType.Exists() {
		return fmt.Errorf("missing type field in stream event")
	}
	m.Type = MessagesStreamChunkType(eventType.String())
	switch typ := MessagesStreamChunkType(eventType.String()); typ {
	case MessagesStreamChunkTypeMessageStart:
		messageBytes := gjson.GetBytes(data, "message")
		r := strings.NewReader(messageBytes.Raw)
		decoder := json.NewDecoder(r)
		var message MessagesStreamChunkMessageStart
		if err := decoder.Decode(&message); err != nil {
			return fmt.Errorf("failed to unmarshal message in stream event: %w", err)
		}
		m.MessageStart = &message
	case MessagesStreamChunkTypeMessageDelta:
		var messageDelta MessagesStreamChunkMessageDelta
		if err := json.Unmarshal(data, &messageDelta); err != nil {
			return fmt.Errorf("failed to unmarshal message delta in stream event: %w", err)
		}
		m.MessageDelta = &messageDelta
	case MessagesStreamChunkTypeMessageStop:
		var messageStop MessagesStreamChunkMessageStop
		if err := json.Unmarshal(data, &messageStop); err != nil {
			return fmt.Errorf("failed to unmarshal message stop in stream event: %w", err)
		}
		m.MessageStop = &messageStop
	case MessagesStreamChunkTypeContentBlockStart:
		var contentBlockStart MessagesStreamChunkContentBlockStart
		if err := json.Unmarshal(data, &contentBlockStart); err != nil {
			return fmt.Errorf("failed to unmarshal content block start in stream event: %w", err)
		}
		m.ContentBlockStart = &contentBlockStart
	case MessagesStreamChunkTypeContentBlockDelta:
		var contentBlockDelta MessagesStreamChunkContentBlockDelta
		if err := json.Unmarshal(data, &contentBlockDelta); err != nil {
			return fmt.Errorf("failed to unmarshal content block delta in stream event: %w", err)
		}
		m.ContentBlockDelta = &contentBlockDelta
	case MessagesStreamChunkTypeContentBlockStop:
		var contentBlockStop MessagesStreamChunkContentBlockStop
		if err := json.Unmarshal(data, &contentBlockStop); err != nil {
			return fmt.Errorf("failed to unmarshal content block stop in stream event: %w", err)
		}
		m.ContentBlockStop = &contentBlockStop
	default:
		return fmt.Errorf("unknown stream event type: %s", typ)
	}
	return nil
}

// ErrorResponse represents an error response from the Anthropic API.
// https://platform.claude.com/docs/en/api/errors
type ErrorResponse struct {
	Error     ErrorResponseMessage `json:"error"`
	RequestID string               `json:"request_id"`
	// Type is always "error".
	Type string `json:"type"`
}

// ErrorResponseMessage represents the error message in an Anthropic API error response
// which corresponds to the HTTP status code.
// https://platform.claude.com/docs/en/api/errors#http-errors
type ErrorResponseMessage struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}
