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

func TestMessageContent_MessagesStreamEvent(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		exp     MessagesStreamEvent
		wantErr bool
	}{
		{
			name:    "message_start",
			jsonStr: `{"type":"message_start","message":{"id":"msg_014p7gG3wDgGV9EUtLvnow3U","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","stop_sequence":null,"usage":{"input_tokens":472,"output_tokens":2},"content":[],"stop_reason":null}}`,
			exp: MessagesStreamEvent{
				Type: "message_start",
				MessageStart: &MessagesStreamEventMessageStart{
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
			exp: MessagesStreamEvent{
				Type: "content_block_start",
			},
			jsonStr: `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		},
		{
			name: "content_block_delta",
			exp: MessagesStreamEvent{
				Type: "content_block_delta",
			},
			jsonStr: `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Okay"}}`,
		},
		{
			name: "content_block_stop",
			exp: MessagesStreamEvent{
				Type: "content_block_stop",
			},
			jsonStr: `{"type":"content_block_stop","index":1}`,
		},
		{
			name: "message_delta",
			exp: MessagesStreamEvent{
				Type: "message_delta",
				MessageDelta: &MessagesStreamEventMessageDelta{
					Delta: MessagesStreamEventMessageDeltaDelta{
						StopReason:   "tool_use",
						StopSequence: nil,
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
			exp: MessagesStreamEvent{
				Type: "message_stop",
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
			var mse MessagesStreamEvent
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

func TestToolUnion_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		want    ToolUnion
		wantErr bool
	}{
		{
			name:    "custom tool",
			jsonStr: `{"type": "custom", "name": "my_tool", "input_schema": {"type": "object"}}`,
			want: ToolUnion{Tool: &Tool{
				Type:        "custom",
				Name:        "my_tool",
				InputSchema: ToolInputSchema{Type: "object"},
			}},
		},
		{
			name:    "missing type",
			jsonStr: `{"name": "my_tool"}`,
			wantErr: true,
		},
		{
			name:    "unknown type",
			jsonStr: `{"type": "unknown", "name": "my_tool"}`,
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
			jsonStr: `{"type": "unknown", "text": "Hello"}`,
			want:    MessagesContentBlock{},
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
		})
	}
}
