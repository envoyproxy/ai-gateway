package openai

import (
	"encoding/json"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/openai/openai-go"
	"github.com/stretchr/testify/require"
)

func TestOpenAIChatCompletionMessageUnmarshal(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []byte
		out  *ChatCompletionRequest
	}{
		{
			name: "basic test",
			in: []byte(`{"model": "gpu-o4",
                        "messages": [
                         {"role": "system", "content": "you are a helpful assistant"},
                         {"role": "developer", "content": "you are a helpful dev assistant"},
                         {"role": "user", "content": "what do you see in this image"}]}`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Value: ChatCompletionSystemMessageParam{
							Role: ChatMessageRoleSystem,
							Content: StringOrArray{
								Value: "you are a helpful assistant",
							},
						},
						Type: ChatMessageRoleSystem,
					},
					{
						Value: ChatCompletionDeveloperMessageParam{
							Role: ChatMessageRoleDeveloper,
							Content: StringOrArray{
								Value: "you are a helpful dev assistant",
							},
						},
						Type: ChatMessageRoleDeveloper,
					},
					{
						Value: ChatCompletionUserMessageParam{
							Role: ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{
								Value: "what do you see in this image",
							},
						},
						Type: ChatMessageRoleUser,
					},
				},
			},
		},
		{
			name: "content with array",
			in: []byte(`{"model": "gpu-o4",
                        "messages": [
                         {"role": "system", "content": [{"text": "you are a helpful assistant", "type": "text"}]},
                         {"role": "developer", "content": [{"text": "you are a helpful dev assistant", "type": "text"}]},
                         {"role": "user", "content": [{"text": "what do you see in this image", "type": "text"}]}]}`),
			out: &ChatCompletionRequest{
				Model: "gpu-o4",
				Messages: []ChatCompletionMessageParamUnion{
					{
						Value: ChatCompletionSystemMessageParam{
							Role: ChatMessageRoleSystem,
							Content: StringOrArray{
								Value: []ChatCompletionContentPartTextParam{
									{
										Text: "you are a helpful assistant",
										Type: string(openai.ChatCompletionContentPartTextTypeText),
									},
								},
							},
						},
						Type: ChatMessageRoleSystem,
					},
					{
						Value: ChatCompletionDeveloperMessageParam{
							Role: ChatMessageRoleDeveloper,
							Content: StringOrArray{
								Value: []ChatCompletionContentPartTextParam{
									{
										Text: "you are a helpful dev assistant",
										Type: string(openai.ChatCompletionContentPartTextTypeText),
									},
								},
							},
						},
						Type: ChatMessageRoleDeveloper,
					},
					{
						Value: ChatCompletionUserMessageParam{
							Role: ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{
								Value: []ChatCompletionContentPartUserUnionParam{
									{
										TextContent: &ChatCompletionContentPartTextParam{Text: "what do you see in this image", Type: "text"},
									},
								},
							},
						},
						Type: ChatMessageRoleUser,
					},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var chatCompletion ChatCompletionRequest
			err := json.Unmarshal(tc.in, &chatCompletion)
			require.NoError(t, err)
			if !cmp.Equal(&chatCompletion, tc.out) {
				t.Errorf("UnmarshalOpenAIRequest(), diff(got, expected) = %s\n", cmp.Diff(&chatCompletion, tc.out))
			}
		})
	}
}
