// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
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
			jsonStr: `[{}, {}]`,
			want:    MessageContent{Array: []MessageContentArrayElement{{}, {}}},
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

func TestMessagesRequest_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name     string
		jsonData string
		want     *MessagesRequest
		wantErr  bool
	}{
		{
			name: "system as string",
			jsonData: `{
				"model": "claude-3-5-sonnet-20241022",
				"messages": [{"role": "user", "content": "Hello"}],
				"max_tokens": 1024,
				"system": "You are a helpful assistant."
			}`,
			want: &MessagesRequest{
				Model:     "claude-3-5-sonnet-20241022",
				Messages:  []Message{{Role: MessageRoleUser, Content: MessageContent{Text: "Hello"}}},
				MaxTokens: 1024,
			},
			wantErr: false,
		},
		{
			name: "system as object array",
			jsonData: `{
				"model": "claude-3-5-sonnet-20241022",
				"messages": [{"role": "user", "content": "Hello"}],
				"max_tokens": 1024,
				"system": [
					{
						"text": "Today's date is 2024-06-01.",
						"type": "text"
					}
				]
			}`,
			want: &MessagesRequest{
				Model:     "claude-3-5-sonnet-20241022",
				Messages:  []Message{{Role: MessageRoleUser, Content: MessageContent{Text: "Hello"}}},
				MaxTokens: 1024,
			},
			wantErr: false,
		},
		{
			name: "system omitted",
			jsonData: `{
				"model": "claude-3-5-sonnet-20241022",
				"messages": [{"role": "user", "content": "Hello"}],
				"max_tokens": 1024
			}`,
			want: &MessagesRequest{
				Model:     "claude-3-5-sonnet-20241022",
				Messages:  []Message{{Role: MessageRoleUser, Content: MessageContent{Text: "Hello"}}},
				MaxTokens: 1024,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got MessagesRequest
			err := json.Unmarshal([]byte(tt.jsonData), &got)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want.Model, got.Model)
			assert.Equal(t, tt.want.Messages, got.Messages)
			assert.Equal(t, tt.want.MaxTokens, got.MaxTokens)
		})
	}
}
