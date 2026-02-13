// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package anthropic

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessageContent_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		want    MessageContent
		wantErr bool
	}{
		{
			name:    "string content",
			jsonStr: `"Hello, world!"`,
			want:    MessageContent{Text: "Hello, world!"},
			wantErr: false,
		},
		{
			name:    "array content",
			jsonStr: `[{"type": "text", "text": "Hello, "}, {"type": "text", "text": "world!"}]`,
			want: MessageContent{Array: []ContentBlockParam{
				{Text: &TextBlockParam{Text: "Hello, ", Type: "text"}},
				{Text: &TextBlockParam{Text: "world!", Type: "text"}},
			}},
			wantErr: false,
		},
		{
			name:    "invalid content",
			jsonStr: `12345`,
			want:    MessageContent{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mc MessageContent
			err := mc.UnmarshalJSON([]byte(tt.jsonStr))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, mc)
		})
	}
}

func TestMessageContent_MessagesStreamChunk(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		exp     MessagesStreamChunk
		wantErr bool
	}{
		{
			name:    "message_start",
			jsonStr: `{"type":"message_start","message":{"id":"msg_014p7gG3wDgGV9EUtLvnow3U","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","stop_sequence":null,"usage":{"input_tokens":472,"output_tokens":2},"content":[],"stop_reason":null}}`,
			exp: MessagesStreamChunk{
				Type: "message_start",
				MessageStart: &MessagesStreamChunkMessageStart{
					ID:           "msg_014p7gG3wDgGV9EUtLvnow3U",
					Type:         "message",
					Role:         "assistant",
					Model:        "claude-sonnet-4-5-20250929",
					StopSequence: nil,
					Usage: &Usage{
						InputTokens:  472,
						OutputTokens: 2,
					},
					Content:    []MessagesContentBlock{},
					StopReason: nil,
				},
			},
			wantErr: false,
		},
		{
			name: "content_block_start",
			exp: MessagesStreamChunk{
				Type: "content_block_start",
				ContentBlockStart: &MessagesStreamChunkContentBlockStart{
					Type:  "content_block_start",
					Index: 0,
					ContentBlock: MessagesContentBlock{
						Text: &TextBlock{
							Type: "text",
							Text: "",
						},
					},
				},
			},
			jsonStr: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		},
		{
			name: "content_block_delta",
			exp: MessagesStreamChunk{
				Type: "content_block_delta",
				ContentBlockDelta: &MessagesStreamChunkContentBlockDelta{
					Type:  "content_block_delta",
					Index: 0,
					Delta: ContentBlockDelta{
						Type: "text_delta",
						Text: "Okay",
					},
				},
			},
			jsonStr: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Okay"}}`,
		},
		{
			name: "content_block_delta input_json_delta",
			exp: MessagesStreamChunk{
				Type: "content_block_delta",
				ContentBlockDelta: &MessagesStreamChunkContentBlockDelta{
					Type:  "content_block_delta",
					Index: 1,
					Delta: ContentBlockDelta{
						Type:        "input_json_delta",
						PartialJSON: "{\"query",
					},
				},
			},
			jsonStr: `{"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"query"}}`,
		},
		{
			name: "content_block_stop",
			exp: MessagesStreamChunk{
				Type: "content_block_stop",
				ContentBlockStop: &MessagesStreamChunkContentBlockStop{
					Type:  "content_block_stop",
					Index: 1,
				},
			},
			jsonStr: `{"type":"content_block_stop","index":1}`,
		},
		{
			name: "content_block_delta thinking_delta",
			exp: MessagesStreamChunk{
				Type: "content_block_delta",
				ContentBlockDelta: &MessagesStreamChunkContentBlockDelta{
					Type:  "content_block_delta",
					Index: 0,
					Delta: ContentBlockDelta{
						Type:     "thinking_delta",
						Thinking: "Let me solve this step by step",
					},
				},
			},
			jsonStr: `{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"Let me solve this step by step"}}`,
		},
		{
			name: "content_block_delta signature_delta",
			exp: MessagesStreamChunk{
				Type: "content_block_delta",
				ContentBlockDelta: &MessagesStreamChunkContentBlockDelta{
					Type:  "content_block_delta",
					Index: 0,
					Delta: ContentBlockDelta{
						Type:      "signature_delta",
						Signature: "EqQBCgIYAhIM1gbcDa9GJwZA2b3hGgxBdjrkzLoky3dl1pkiMOYds...",
					},
				},
			},
			jsonStr: `{"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"EqQBCgIYAhIM1gbcDa9GJwZA2b3hGgxBdjrkzLoky3dl1pkiMOYds..."}}`,
		},
		{
			name: "message_delta",
			exp: MessagesStreamChunk{
				Type: "message_delta",
				MessageDelta: &MessagesStreamChunkMessageDelta{
					Type: "message_delta",
					Delta: MessagesStreamChunkMessageDeltaDelta{
						StopReason:   "tool_use",
						StopSequence: "",
					},
					Usage: Usage{
						OutputTokens: 89,
					},
				},
			},
			jsonStr: `{"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":89}}`,
		},
		{
			name: "message_stop",
			exp: MessagesStreamChunk{
				Type: "message_stop",
				MessageStop: &MessagesStreamChunkMessageStop{
					Type: "message_stop",
				},
			},
			jsonStr: ` {"type":"message_stop"}`,
		},
		{
			name:    "invalid event",
			jsonStr: `abcdes`,
			wantErr: true,
		},
		{
			name:    "type field does not exist",
			jsonStr: `{"foo":"bar"}`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mse MessagesStreamChunk
			err := mse.UnmarshalJSON([]byte(tt.jsonStr))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.exp, mse)
		})
	}
}

func TestMessageContent_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		mc      MessageContent
		want    string
		wantErr bool
	}{
		{
			name: "string content",
			mc:   MessageContent{Text: "Hello, world!"},
			want: `"Hello, world!"`,
		},
		{
			name: "array content",
			mc: MessageContent{Array: []ContentBlockParam{
				{Text: &TextBlockParam{Text: "Hello, ", Type: "text"}},
				{Text: &TextBlockParam{Text: "world!", Type: "text"}},
			}},
			want: `[{"text":"Hello, ","type":"text"},{"text":"world!","type":"text"}]`,
		},
		{
			name:    "empty content",
			mc:      MessageContent{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.mc.MarshalJSON()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestContentBlockParam_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		want    ContentBlockParam
		wantErr bool
	}{
		{
			name:    "text block",
			jsonStr: `{"type": "text", "text": "Hello"}`,
			want:    ContentBlockParam{Text: &TextBlockParam{Text: "Hello", Type: "text"}},
		},
		{
			name:    "image block",
			jsonStr: `{"type": "image", "source": {"type": "base64", "media_type": "image/png", "data": "abc123"}}`,
			want: ContentBlockParam{Image: &ImageBlockParam{
				Type:   "image",
				Source: map[string]any{"type": "base64", "media_type": "image/png", "data": "abc123"},
			}},
		},
		{
			name:    "document block",
			jsonStr: `{"type": "document", "source": {"type": "text", "data": "hello", "media_type": "text/plain"}, "context": "some context", "title": "doc title"}`,
			want: ContentBlockParam{Document: &DocumentBlockParam{
				Type:    "document",
				Source:  map[string]any{"type": "text", "data": "hello", "media_type": "text/plain"},
				Context: "some context",
				Title:   "doc title",
			}},
		},
		{
			name:    "search result block",
			jsonStr: `{"type": "search_result", "source": "https://example.com", "title": "Example", "content": [{"type": "text", "text": "result text"}]}`,
			want: ContentBlockParam{SearchResult: &SearchResultBlockParam{
				Type:    "search_result",
				Source:  "https://example.com",
				Title:   "Example",
				Content: []TextBlockParam{{Type: "text", Text: "result text"}},
			}},
		},
		{
			name:    "thinking block",
			jsonStr: `{"type": "thinking", "thinking": "Let me think...", "signature": "sig123"}`,
			want: ContentBlockParam{Thinking: &ThinkingBlockParam{
				Type:      "thinking",
				Thinking:  "Let me think...",
				Signature: "sig123",
			}},
		},
		{
			name:    "redacted thinking block",
			jsonStr: `{"type": "redacted_thinking", "data": "redacted_data_here"}`,
			want: ContentBlockParam{RedactedThinking: &RedactedThinkingBlockParam{
				Type: "redacted_thinking",
				Data: "redacted_data_here",
			}},
		},
		{
			name:    "tool use block",
			jsonStr: `{"type": "tool_use", "id": "tu_123", "name": "my_tool", "input": {"query": "test"}}`,
			want: ContentBlockParam{ToolUse: &ToolUseBlockParam{
				Type:  "tool_use",
				ID:    "tu_123",
				Name:  "my_tool",
				Input: map[string]any{"query": "test"},
			}},
		},
		{
			name:    "tool result block",
			jsonStr: `{"type": "tool_result", "tool_use_id": "tu_123", "content": "result text", "is_error": false}`,
			want: ContentBlockParam{ToolResult: &ToolResultBlockParam{
				Type:      "tool_result",
				ToolUseID: "tu_123",
				Content:   "result text",
			}},
		},
		{
			name:    "server tool use block",
			jsonStr: `{"type": "server_tool_use", "id": "stu_123", "name": "web_search", "input": {"query": "test"}}`,
			want: ContentBlockParam{ServerToolUse: &ServerToolUseBlockParam{
				Type:  "server_tool_use",
				ID:    "stu_123",
				Name:  "web_search",
				Input: map[string]any{"query": "test"},
			}},
		},
		{
			name:    "web search tool result block",
			jsonStr: `{"type": "web_search_tool_result", "tool_use_id": "stu_123", "content": [{"type": "web_search_result", "title": "Result", "url": "https://example.com", "encrypted_content": "enc123"}]}`,
			want: ContentBlockParam{WebSearchToolResult: &WebSearchToolResultBlockParam{
				Type:      "web_search_tool_result",
				ToolUseID: "stu_123",
				Content: []any{
					map[string]any{"type": "web_search_result", "title": "Result", "url": "https://example.com", "encrypted_content": "enc123"},
				},
			}},
		},
		{
			name:    "missing type",
			jsonStr: `{"text": "Hello"}`,
			wantErr: true,
		},
		{
			name:    "unknown type",
			jsonStr: `{"type": "unknown", "text": "Hello"}`,
			want:    ContentBlockParam{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cbp ContentBlockParam
			err := cbp.UnmarshalJSON([]byte(tt.jsonStr))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, cbp)
		})
	}
}

func TestContentBlockParam_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		cbp     ContentBlockParam
		want    string
		wantErr bool
	}{
		{
			name: "text block",
			cbp:  ContentBlockParam{Text: &TextBlockParam{Text: "Hello", Type: "text"}},
			want: `{"text":"Hello","type":"text"}`,
		},
		{
			name: "image block",
			cbp: ContentBlockParam{Image: &ImageBlockParam{
				Type:   "image",
				Source: map[string]any{"type": "base64", "media_type": "image/png", "data": "abc123"},
			}},
			want: `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc123"}}`,
		},
		{
			name: "document block",
			cbp: ContentBlockParam{Document: &DocumentBlockParam{
				Type:    "document",
				Source:  map[string]any{"type": "text", "data": "hello", "media_type": "text/plain"},
				Context: "some context",
				Title:   "doc title",
			}},
			want: `{"type":"document","source":{"type":"text","data":"hello","media_type":"text/plain"},"context":"some context","title":"doc title"}`,
		},
		{
			name: "search result block",
			cbp: ContentBlockParam{SearchResult: &SearchResultBlockParam{
				Type:    "search_result",
				Source:  "https://example.com",
				Title:   "Example",
				Content: []TextBlockParam{{Type: "text", Text: "result text"}},
			}},
			want: `{"type":"search_result","content":[{"type":"text","text":"result text"}],"source":"https://example.com","title":"Example"}`,
		},
		{
			name: "thinking block",
			cbp: ContentBlockParam{Thinking: &ThinkingBlockParam{
				Type:      "thinking",
				Thinking:  "Let me think...",
				Signature: "sig123",
			}},
			want: `{"type":"thinking","thinking":"Let me think...","signature":"sig123"}`,
		},
		{
			name: "redacted thinking block",
			cbp: ContentBlockParam{RedactedThinking: &RedactedThinkingBlockParam{
				Type: "redacted_thinking",
				Data: "redacted_data_here",
			}},
			want: `{"type":"redacted_thinking","data":"redacted_data_here"}`,
		},
		{
			name: "tool use block",
			cbp: ContentBlockParam{ToolUse: &ToolUseBlockParam{
				Type:  "tool_use",
				ID:    "tu_123",
				Name:  "my_tool",
				Input: map[string]any{"query": "test"},
			}},
			want: `{"type":"tool_use","id":"tu_123","name":"my_tool","input":{"query":"test"}}`,
		},
		{
			name: "tool result block",
			cbp: ContentBlockParam{ToolResult: &ToolResultBlockParam{
				Type:      "tool_result",
				ToolUseID: "tu_123",
				Content:   "result text",
			}},
			want: `{"type":"tool_result","tool_use_id":"tu_123","content":"result text"}`,
		},
		{
			name: "server tool use block",
			cbp: ContentBlockParam{ServerToolUse: &ServerToolUseBlockParam{
				Type:  "server_tool_use",
				ID:    "stu_123",
				Name:  "web_search",
				Input: map[string]any{"query": "test"},
			}},
			want: `{"type":"server_tool_use","id":"stu_123","name":"web_search","input":{"query":"test"}}`,
		},
		{
			name: "web search tool result block",
			cbp: ContentBlockParam{WebSearchToolResult: &WebSearchToolResultBlockParam{
				Type:      "web_search_tool_result",
				ToolUseID: "stu_123",
				Content:   "some content",
			}},
			want: `{"type":"web_search_tool_result","tool_use_id":"stu_123","content":"some content"}`,
		},
		{
			name:    "empty block",
			cbp:     ContentBlockParam{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.cbp.MarshalJSON()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestSystemPrompt_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		want    SystemPrompt
		wantErr bool
	}{
		{
			name:    "string prompt",
			jsonStr: `"You are a helpful assistant."`,
			want:    SystemPrompt{Text: "You are a helpful assistant."},
		},
		{
			name:    "array prompt",
			jsonStr: `[{"type": "text", "text": "You are a helpful assistant."}]`,
			want: SystemPrompt{Texts: []TextBlockParam{
				{Text: "You are a helpful assistant.", Type: "text"},
			}},
		},
		{
			name:    "invalid prompt",
			jsonStr: `12345`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var sp SystemPrompt
			err := sp.UnmarshalJSON([]byte(tt.jsonStr))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, sp)
		})
	}
}

func TestSystemPrompt_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		sp      SystemPrompt
		want    string
		wantErr bool
	}{
		{
			name: "string prompt",
			sp:   SystemPrompt{Text: "You are a helpful assistant."},
			want: `"You are a helpful assistant."`,
		},
		{
			name: "array prompt",
			sp: SystemPrompt{Texts: []TextBlockParam{
				{Text: "You are a helpful assistant.", Type: "text"},
			}},
			want: `[{"text":"You are a helpful assistant.","type":"text"}]`,
		},
		{
			name:    "empty prompt",
			sp:      SystemPrompt{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.sp.MarshalJSON()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestMessagesContentBlock_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		want    MessagesContentBlock
		wantErr bool
	}{
		{
			name:    "text block",
			jsonStr: `{"type": "text", "text": "Hello"}`,
			want:    MessagesContentBlock{Text: &TextBlock{Text: "Hello", Type: "text"}},
		},
		{
			name:    "missing type",
			jsonStr: `{"text": "Hello"}`,
			wantErr: true,
		},
		{
			name:    "unknown type",
			jsonStr: `{"type": "unknown"}`,
			want:    MessagesContentBlock{},
		},
		{
			name:    "tool use block",
			jsonStr: `{"type": "tool_use", "name": "my_tool", "input": {"query": "What is the weather today?"}}`,
			want: MessagesContentBlock{Tool: &ToolUseBlock{
				Type: "tool_use",
				Name: "my_tool",
				Input: map[string]any{
					"query": "What is the weather today?",
				},
			}},
		},
		{
			name:    "thinking block",
			jsonStr: `{"type": "thinking", "thinking": "Let me think about that."}`,
			want: MessagesContentBlock{Thinking: &ThinkingBlock{
				Type:     "thinking",
				Thinking: "Let me think about that.",
			}},
		},
		{
			name:    "redacted thinking block",
			jsonStr: `{"type": "redacted_thinking", "data": "redacted_data"}`,
			want: MessagesContentBlock{RedactedThinking: &RedactedThinkingBlock{
				Type: "redacted_thinking",
				Data: "redacted_data",
			}},
		},
		{
			name:    "server tool use block",
			jsonStr: `{"type": "server_tool_use", "id": "stu_1", "name": "web_search", "input": {"query": "test"}}`,
			want: MessagesContentBlock{ServerToolUse: &ServerToolUseBlock{
				Type:  "server_tool_use",
				ID:    "stu_1",
				Name:  "web_search",
				Input: map[string]any{"query": "test"},
			}},
		},
		{
			name:    "web search tool result block",
			jsonStr: `{"type": "web_search_tool_result", "tool_use_id": "stu_1", "content": "results"}`,
			want: MessagesContentBlock{WebSearchToolResult: &WebSearchToolResultBlock{
				Type:      "web_search_tool_result",
				ToolUseID: "stu_1",
				Content:   "results",
			}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mcb MessagesContentBlock
			err := mcb.UnmarshalJSON([]byte(tt.jsonStr))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, mcb)

			marshaled, err := mcb.MarshalJSON()
			// This is mostly for coverage. marshaling is not currently used in the main code.
			if err == nil {
				var unmarshaled MessagesContentBlock
				err = unmarshaled.UnmarshalJSON(marshaled)
				require.NoError(t, err)
				require.Equal(t, mcb, unmarshaled)
			}
		})
	}
}

func TestMessagesContentBlock_MarshalJSON(t *testing.T) {
	t.Run("empty block returns error", func(t *testing.T) {
		mcb := MessagesContentBlock{}
		_, err := mcb.MarshalJSON()
		require.Error(t, err)
	})
}

func TestToolUnion_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		want    ToolUnion
		wantErr bool
	}{
		{
			name:    "custom tool",
			jsonStr: `{"type":"custom","name":"my_tool","input_schema":{"type":"object"}}`,
			want: ToolUnion{Tool: &Tool{
				Type: "custom", Name: "my_tool",
				InputSchema: ToolInputSchema{Type: "object"},
			}},
		},
		{
			name:    "bash tool",
			jsonStr: `{"type":"bash_20250124","name":"bash"}`,
			want:    ToolUnion{BashTool: &BashTool{Type: "bash_20250124", Name: "bash"}},
		},
		{
			name:    "text editor tool 20250124",
			jsonStr: `{"type":"text_editor_20250124","name":"str_replace_editor"}`,
			want:    ToolUnion{TextEditorTool20250124: &TextEditorTool20250124{Type: "text_editor_20250124", Name: "str_replace_editor"}},
		},
		{
			name:    "text editor tool 20250429",
			jsonStr: `{"type":"text_editor_20250429","name":"str_replace_based_edit_tool"}`,
			want:    ToolUnion{TextEditorTool20250429: &TextEditorTool20250429{Type: "text_editor_20250429", Name: "str_replace_based_edit_tool"}},
		},
		{
			name:    "text editor tool 20250728",
			jsonStr: `{"type":"text_editor_20250728","name":"str_replace_based_edit_tool"}`,
			want:    ToolUnion{TextEditorTool20250728: &TextEditorTool20250728{Type: "text_editor_20250728", Name: "str_replace_based_edit_tool"}},
		},
		{
			name:    "web search tool",
			jsonStr: `{"type":"web_search_20250305","name":"web_search"}`,
			want:    ToolUnion{WebSearchTool: &WebSearchTool{Type: "web_search_20250305", Name: "web_search"}},
		},
		{
			name:    "missing type",
			jsonStr: `{"name":"my_tool"}`,
			wantErr: true,
		},
		{
			name:    "unknown type ignored",
			jsonStr: `{"type":"future_tool","name":"x"}`,
			want:    ToolUnion{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tu ToolUnion
			err := tu.UnmarshalJSON([]byte(tt.jsonStr))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, tu)
		})
	}
}

func TestToolUnion_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		tu      ToolUnion
		want    string
		wantErr bool
	}{
		{
			name: "custom tool",
			tu:   ToolUnion{Tool: &Tool{Type: "custom", Name: "t", InputSchema: ToolInputSchema{Type: "object"}}},
			want: `{"type":"custom","name":"t","input_schema":{"type":"object"}}`,
		},
		{
			name: "bash tool",
			tu:   ToolUnion{BashTool: &BashTool{Type: "bash_20250124", Name: "bash"}},
			want: `{"type":"bash_20250124","name":"bash"}`,
		},
		{
			name: "text editor 20250124",
			tu:   ToolUnion{TextEditorTool20250124: &TextEditorTool20250124{Type: "text_editor_20250124", Name: "str_replace_editor"}},
			want: `{"type":"text_editor_20250124","name":"str_replace_editor"}`,
		},
		{
			name: "text editor 20250429",
			tu:   ToolUnion{TextEditorTool20250429: &TextEditorTool20250429{Type: "text_editor_20250429", Name: "n"}},
			want: `{"type":"text_editor_20250429","name":"n"}`,
		},
		{
			name: "text editor 20250728",
			tu:   ToolUnion{TextEditorTool20250728: &TextEditorTool20250728{Type: "text_editor_20250728", Name: "n"}},
			want: `{"type":"text_editor_20250728","name":"n"}`,
		},
		{
			name: "web search tool",
			tu:   ToolUnion{WebSearchTool: &WebSearchTool{Type: "web_search_20250305", Name: "web_search"}},
			want: `{"type":"web_search_20250305","name":"web_search"}`,
		},
		{
			name:    "empty tool union",
			tu:      ToolUnion{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.tu.MarshalJSON()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestToolChoice_UnmarshalJSON(t *testing.T) {
	boolTrue := true
	tests := []struct {
		name    string
		jsonStr string
		want    ToolChoice
		wantErr bool
	}{
		{
			name:    "auto",
			jsonStr: `{"type":"auto","disable_parallel_tool_use":true}`,
			want:    ToolChoice{Auto: &ToolChoiceAuto{Type: "auto", DisableParallelToolUse: &boolTrue}},
		},
		{
			name:    "any",
			jsonStr: `{"type":"any"}`,
			want:    ToolChoice{Any: &ToolChoiceAny{Type: "any"}},
		},
		{
			name:    "tool",
			jsonStr: `{"type":"tool","name":"my_tool"}`,
			want:    ToolChoice{Tool: &ToolChoiceTool{Type: "tool", Name: "my_tool"}},
		},
		{
			name:    "none",
			jsonStr: `{"type":"none"}`,
			want:    ToolChoice{None: &ToolChoiceNone{Type: "none"}},
		},
		{
			name:    "missing type",
			jsonStr: `{"name":"x"}`,
			wantErr: true,
		},
		{
			name:    "unknown type ignored",
			jsonStr: `{"type":"future"}`,
			want:    ToolChoice{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tc ToolChoice
			err := tc.UnmarshalJSON([]byte(tt.jsonStr))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, tc)
		})
	}
}

func TestToolChoice_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		tc      ToolChoice
		want    string
		wantErr bool
	}{
		{
			name: "auto",
			tc:   ToolChoice{Auto: &ToolChoiceAuto{Type: "auto"}},
			want: `{"type":"auto"}`,
		},
		{
			name: "any",
			tc:   ToolChoice{Any: &ToolChoiceAny{Type: "any"}},
			want: `{"type":"any"}`,
		},
		{
			name: "tool",
			tc:   ToolChoice{Tool: &ToolChoiceTool{Type: "tool", Name: "my_tool"}},
			want: `{"type":"tool","name":"my_tool"}`,
		},
		{
			name: "none",
			tc:   ToolChoice{None: &ToolChoiceNone{Type: "none"}},
			want: `{"type":"none"}`,
		},
		{
			name:    "empty tool choice",
			tc:      ToolChoice{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.tc.MarshalJSON()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestThinking_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		want    Thinking
		wantErr bool
	}{
		{
			name:    "enabled",
			jsonStr: `{"type":"enabled","budget_tokens":2048}`,
			want:    Thinking{Enabled: &ThinkingEnabled{Type: "enabled", BudgetTokens: 2048}},
		},
		{
			name:    "disabled",
			jsonStr: `{"type":"disabled"}`,
			want:    Thinking{Disabled: &ThinkingDisabled{Type: "disabled"}},
		},
		{
			name:    "adaptive",
			jsonStr: `{"type":"adaptive"}`,
			want:    Thinking{Adaptive: &ThinkingAdaptive{Type: "adaptive"}},
		},
		{
			name:    "missing type",
			jsonStr: `{"budget_tokens":1024}`,
			wantErr: true,
		},
		{
			name:    "unknown type ignored",
			jsonStr: `{"type":"future"}`,
			want:    Thinking{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var th Thinking
			err := th.UnmarshalJSON([]byte(tt.jsonStr))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.want, th)
		})
	}
}

func TestThinking_MarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		th      Thinking
		want    string
		wantErr bool
	}{
		{
			name: "enabled",
			th:   Thinking{Enabled: &ThinkingEnabled{Type: "enabled", BudgetTokens: 2048}},
			want: `{"type":"enabled","budget_tokens":2048}`,
		},
		{
			name: "disabled",
			th:   Thinking{Disabled: &ThinkingDisabled{Type: "disabled"}},
			want: `{"type":"disabled"}`,
		},
		{
			name: "adaptive",
			th:   Thinking{Adaptive: &ThinkingAdaptive{Type: "adaptive"}},
			want: `{"type":"adaptive"}`,
		},
		{
			name:    "empty thinking",
			th:      Thinking{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.th.MarshalJSON()
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.JSONEq(t, tt.want, string(got))
		})
	}
}

func TestContentBlockParam_UnmarshalJSON_ErrorPaths(t *testing.T) {
	// Each case has a valid "type" but invalid JSON for that type's struct,
	// triggering the unmarshal error path for each content block variant.
	tests := []struct {
		name    string
		jsonStr string
	}{
		{name: "text invalid", jsonStr: `{"type":"text","text":123}`},
		{name: "image invalid", jsonStr: `{"type":"image","source":null,"cache_control":}`},
		{name: "document invalid", jsonStr: `{"type":"document","source":null,"context":123}`},
		{name: "search_result invalid", jsonStr: `{"type":"search_result","content":"not_array"}`},
		{name: "thinking invalid", jsonStr: `{"type":"thinking","thinking":123}`},
		{name: "redacted_thinking invalid", jsonStr: `{"type":"redacted_thinking","data":123}`},
		{name: "tool_use invalid", jsonStr: `{"type":"tool_use","input":"not_object"}`},
		{name: "tool_result invalid", jsonStr: `{"type":"tool_result","is_error":"not_bool"}`},
		{name: "server_tool_use invalid", jsonStr: `{"type":"server_tool_use","input":"not_object"}`},
		{name: "web_search_tool_result invalid", jsonStr: `{"type":"web_search_tool_result","tool_use_id":123}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cbp ContentBlockParam
			err := cbp.UnmarshalJSON([]byte(tt.jsonStr))
			require.Error(t, err)
		})
	}
}

func TestMessagesContentBlock_UnmarshalJSON_ErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
	}{
		{name: "tool_use invalid", jsonStr: `{"type":"tool_use","input":"bad"}`},
		{name: "thinking invalid", jsonStr: `{"type":"thinking","thinking":123}`},
		{name: "redacted_thinking invalid", jsonStr: `{"type":"redacted_thinking","data":123}`},
		{name: "server_tool_use invalid", jsonStr: `{"type":"server_tool_use","input":"bad"}`},
		{name: "web_search_tool_result invalid", jsonStr: `{"type":"web_search_tool_result","tool_use_id":123}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var mcb MessagesContentBlock
			err := mcb.UnmarshalJSON([]byte(tt.jsonStr))
			require.Error(t, err)
		})
	}
}

func TestToolUnion_UnmarshalJSON_ErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
	}{
		{name: "custom invalid", jsonStr: `{"type":"custom","input_schema":"bad"}`},
		{name: "bash invalid", jsonStr: `{"type":"bash_20250124","name":123}`},
		{name: "text_editor_20250124 invalid", jsonStr: `{"type":"text_editor_20250124","name":123}`},
		{name: "text_editor_20250429 invalid", jsonStr: `{"type":"text_editor_20250429","name":123}`},
		{name: "text_editor_20250728 invalid", jsonStr: `{"type":"text_editor_20250728","name":123}`},
		{name: "web_search invalid", jsonStr: `{"type":"web_search_20250305","name":123}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tu ToolUnion
			err := tu.UnmarshalJSON([]byte(tt.jsonStr))
			require.Error(t, err)
		})
	}
}

func TestToolChoice_UnmarshalJSON_ErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
	}{
		{name: "auto invalid", jsonStr: `{"type":"auto","disable_parallel_tool_use":"bad"}`},
		{name: "any invalid", jsonStr: `{"type":"any","disable_parallel_tool_use":"bad"}`},
		{name: "tool invalid", jsonStr: `{"type":"tool","name":123}`},
		{name: "none invalid", jsonStr: `{"type":"none","type":123}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var tc ToolChoice
			err := tc.UnmarshalJSON([]byte(tt.jsonStr))
			require.Error(t, err)
		})
	}
}

func TestThinking_UnmarshalJSON_ErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
	}{
		{name: "enabled invalid", jsonStr: `{"type":"enabled","budget_tokens":"bad"}`},
		{name: "disabled invalid", jsonStr: `{"type":"disabled","type":123}`},
		{name: "adaptive invalid", jsonStr: `{"type":"adaptive","type":123}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var th Thinking
			err := th.UnmarshalJSON([]byte(tt.jsonStr))
			require.Error(t, err)
		})
	}
}

func TestMessagesStreamChunk_UnmarshalJSON_ErrorPaths(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
	}{
		{name: "message_delta invalid", jsonStr: `{"type":"message_delta","usage":"bad"}`},
		{name: "message_stop invalid", jsonStr: `{"type":"message_stop","type":123}`},
		{name: "content_block_start invalid", jsonStr: `{"type":"content_block_start","content_block":"bad"}`},
		{name: "content_block_delta invalid", jsonStr: `{"type":"content_block_delta","delta":"bad"}`},
		{name: "content_block_stop invalid", jsonStr: `{"type":"content_block_stop","index":"bad"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var msc MessagesStreamChunk
			err := msc.UnmarshalJSON([]byte(tt.jsonStr))
			require.Error(t, err)
		})
	}
}
