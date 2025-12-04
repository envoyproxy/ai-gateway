package anthropic

import (
	"testing"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/stretchr/testify/require"
)

func TestConvertSSEToResponse(t *testing.T) {
	tests := []struct {
		name   string
		chunks []*anthropic.MessagesStreamEvent
		want   *anthropic.MessagesResponse
	}{
		{
			name: "basic text stream",
			chunks: []*anthropic.MessagesStreamEvent{
				{
					Type: anthropic.MessagesStreamEventTypeMessageStart,
					MessageStart: &anthropic.MessagesStreamEventMessageStart{
						ID:    "msg_123",
						Model: "claude-3",
						Role:  "assistant",
						Usage: &anthropic.Usage{InputTokens: 10},
					},
				},
				{
					Type: anthropic.MessagesStreamEventTypeContentBlockStart,
					ContentBlockStart: &anthropic.MessagesStreamEventContentBlockStart{
						Index: 0,
						ContentBlock: anthropic.MessagesContentBlock{
							Text: &anthropic.TextBlock{Type: "text", Text: ""},
						},
					},
				},
				{
					Type: anthropic.MessagesStreamEventTypeContentBlockDelta,
					ContentBlockDelta: &anthropic.MessagesStreamEventContentBlockDelta{
						Index: 0,
						Delta: anthropic.ContentBlockDelta{Type: "text_delta", Text: "Hello"},
					},
				},
				{
					Type: anthropic.MessagesStreamEventTypeContentBlockDelta,
					ContentBlockDelta: &anthropic.MessagesStreamEventContentBlockDelta{
						Index: 0,
						Delta: anthropic.ContentBlockDelta{Type: "text_delta", Text: " World"},
					},
				},
				{
					Type:             anthropic.MessagesStreamEventTypeContentBlockStop,
					ContentBlockStop: &anthropic.MessagesStreamEventContentBlockStop{Index: 0},
				},
				{
					Type: anthropic.MessagesStreamEventTypeMessageDelta,
					MessageDelta: &anthropic.MessagesStreamEventMessageDelta{
						Usage: anthropic.Usage{OutputTokens: 5},
						Delta: anthropic.MessagesStreamEventMessageDeltaDelta{StopReason: "end_turn"},
					},
				},
				{
					Type: anthropic.MessagesStreamEventTypeMessageStop,
				},
			},
			want: &anthropic.MessagesResponse{
				ID:    "msg_123",
				Model: "claude-3",
				Role:  "assistant",
				Usage: &anthropic.Usage{InputTokens: 10, OutputTokens: 5},
				Content: []anthropic.MessagesContentBlock{
					{Text: &anthropic.TextBlock{Type: "text", Text: "Hello World"}},
				},
				StopReason:   func() *anthropic.StopReason { s := anthropic.StopReason("end_turn"); return &s }(),
				StopSequence: func() *string { s := ""; return &s }(),
			},
		},
		{
			name: "tool use stream",
			chunks: []*anthropic.MessagesStreamEvent{
				{
					Type: anthropic.MessagesStreamEventTypeMessageStart,
					MessageStart: &anthropic.MessagesStreamEventMessageStart{
						ID:    "msg_tool",
						Model: "claude-3",
						Role:  "assistant",
						Usage: &anthropic.Usage{InputTokens: 20},
					},
				},
				{
					Type: anthropic.MessagesStreamEventTypeContentBlockStart,
					ContentBlockStart: &anthropic.MessagesStreamEventContentBlockStart{
						Index: 0,
						ContentBlock: anthropic.MessagesContentBlock{
							Tool: &anthropic.ToolUseBlock{Type: "tool_use", ID: "tool_1", Name: "get_weather", Input: map[string]any{}},
						},
					},
				},
				{
					Type: anthropic.MessagesStreamEventTypeContentBlockDelta,
					ContentBlockDelta: &anthropic.MessagesStreamEventContentBlockDelta{
						Index: 0,
						Delta: anthropic.ContentBlockDelta{Type: "input_json_delta", PartialJSON: `{"loc`},
					},
				},
				{
					Type: anthropic.MessagesStreamEventTypeContentBlockDelta,
					ContentBlockDelta: &anthropic.MessagesStreamEventContentBlockDelta{
						Index: 0,
						Delta: anthropic.ContentBlockDelta{Type: "input_json_delta", PartialJSON: `ation": "NYC"}`},
					},
				},
				{
					Type:             anthropic.MessagesStreamEventTypeContentBlockStop,
					ContentBlockStop: &anthropic.MessagesStreamEventContentBlockStop{Index: 0},
				},
				{
					Type: anthropic.MessagesStreamEventTypeMessageDelta,
					MessageDelta: &anthropic.MessagesStreamEventMessageDelta{
						Usage: anthropic.Usage{OutputTokens: 10},
						Delta: anthropic.MessagesStreamEventMessageDeltaDelta{StopReason: "tool_use"},
					},
				},
			},
			want: &anthropic.MessagesResponse{
				ID:    "msg_tool",
				Model: "claude-3",
				Role:  "assistant",
				Usage: &anthropic.Usage{InputTokens: 20, OutputTokens: 10},
				Content: []anthropic.MessagesContentBlock{
					{Tool: &anthropic.ToolUseBlock{
						Type: "tool_use", ID: "tool_1", Name: "get_weather",
						Input: map[string]any{"location": "NYC"},
					}},
				},
				StopReason:   func() *anthropic.StopReason { s := anthropic.StopReason("tool_use"); return &s }(),
				StopSequence: func() *string { s := ""; return &s }(),
			},
		},
		{
			name: "thinking stream",
			chunks: []*anthropic.MessagesStreamEvent{
				{
					Type: anthropic.MessagesStreamEventTypeMessageStart,
					MessageStart: &anthropic.MessagesStreamEventMessageStart{
						ID:    "msg_think",
						Model: "claude-3-5-sonnet",
						Role:  "assistant",
						Usage: &anthropic.Usage{InputTokens: 30},
					},
				},
				{
					Type: anthropic.MessagesStreamEventTypeContentBlockStart,
					ContentBlockStart: &anthropic.MessagesStreamEventContentBlockStart{
						Index: 0,
						ContentBlock: anthropic.MessagesContentBlock{
							Thinking: &anthropic.ThinkingBlock{Type: "thinking", Thinking: ""},
						},
					},
				},
				{
					Type: anthropic.MessagesStreamEventTypeContentBlockDelta,
					ContentBlockDelta: &anthropic.MessagesStreamEventContentBlockDelta{
						Index: 0,
						Delta: anthropic.ContentBlockDelta{Type: "thinking_delta", Thinking: "Let me "},
					},
				},
				{
					Type: anthropic.MessagesStreamEventTypeContentBlockDelta,
					ContentBlockDelta: &anthropic.MessagesStreamEventContentBlockDelta{
						Index: 0,
						Delta: anthropic.ContentBlockDelta{Type: "thinking_delta", Thinking: "think."},
					},
				},
				{
					Type: anthropic.MessagesStreamEventTypeContentBlockDelta,
					ContentBlockDelta: &anthropic.MessagesStreamEventContentBlockDelta{
						Index: 0,
						Delta: anthropic.ContentBlockDelta{Type: "signature_delta", Signature: "sig123"},
					},
				},
				{
					Type:             anthropic.MessagesStreamEventTypeContentBlockStop,
					ContentBlockStop: &anthropic.MessagesStreamEventContentBlockStop{Index: 0},
				},
				{
					Type: anthropic.MessagesStreamEventTypeContentBlockStart,
					ContentBlockStart: &anthropic.MessagesStreamEventContentBlockStart{
						Index: 1,
						ContentBlock: anthropic.MessagesContentBlock{
							Text: &anthropic.TextBlock{Type: "text", Text: ""},
						},
					},
				},
				{
					Type: anthropic.MessagesStreamEventTypeContentBlockDelta,
					ContentBlockDelta: &anthropic.MessagesStreamEventContentBlockDelta{
						Index: 1,
						Delta: anthropic.ContentBlockDelta{Type: "text_delta", Text: "Answer"},
					},
				},
				{
					Type: anthropic.MessagesStreamEventTypeMessageDelta,
					MessageDelta: &anthropic.MessagesStreamEventMessageDelta{
						Usage: anthropic.Usage{OutputTokens: 20},
						Delta: anthropic.MessagesStreamEventMessageDeltaDelta{StopReason: "end_turn"},
					},
				},
			},
			want: &anthropic.MessagesResponse{
				ID:    "msg_think",
				Model: "claude-3-5-sonnet",
				Role:  "assistant",
				Usage: &anthropic.Usage{InputTokens: 30, OutputTokens: 20},
				Content: []anthropic.MessagesContentBlock{
					{Thinking: &anthropic.ThinkingBlock{Type: "thinking", Thinking: "Let me think.", Signature: "sig123"}},
					{Text: &anthropic.TextBlock{Type: "text", Text: "Answer"}},
				},
				StopReason:   func() *anthropic.StopReason { s := anthropic.StopReason("end_turn"); return &s }(),
				StopSequence: func() *string { s := ""; return &s }(),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := convertSSEToResponse(tt.chunks)
			require.Equal(t, tt.want, got)
		})
	}
}
