// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/awsbedrock"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

func TestOpenAIToAWSBedrockTranslatorV1ChatCompletion_RequestBody(t *testing.T) {
	tests := []struct {
		name   string
		output awsbedrock.ConverseInput
		input  openai.ChatCompletionRequest
	}{
		{
			name: "basic test",
			input: openai.ChatCompletionRequest{
				Stream: false,
				Model:  "gpt-4o",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionSystemMessageParam{
							Content: openai.StringOrArray{
								Value: "from-system",
							},
						}, Type: openai.ChatMessageRoleSystem,
					},
					{
						Value: openai.ChatCompletionDeveloperMessageParam{
							Content: openai.StringOrArray{
								Value: "from-developer",
							},
						}, Type: openai.ChatMessageRoleDeveloper,
					},
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "from-user",
							},
						}, Type: openai.ChatMessageRoleUser,
					},
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "part1",
							},
						}, Type: openai.ChatMessageRoleUser,
					},
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "part2",
							},
						}, Type: openai.ChatMessageRoleUser,
					},
					{
						Value: openai.ChatCompletionToolMessageParam{
							Content: openai.StringOrArray{
								Value: "Weather in Queens, NY is 70F and clear skies.",
							},
							ToolCallID: "call_6g7a",
						}, Type: openai.ChatMessageRoleTool,
					},
					{
						Value: openai.ChatCompletionAssistantMessageParam{
							Content: openai.StringOrAssistantRoleContentUnion{
								Value: openai.ChatCompletionAssistantMessageParamContent{
									Type: openai.ChatCompletionAssistantMessageParamContentTypeText,
									Text: ptr.To("I dunno"),
								},
							},
							ToolCalls: []openai.ChatCompletionMessageToolCallParam{
								{
									ID: "call_6g7a",
									Function: openai.ChatCompletionMessageToolCallFunctionParam{
										Arguments: "{\"code_block\":\"from playwright.sync_api import sync_playwright\\n\"}",
										Name:      "exec_python_code",
									},
									Type: openai.ChatCompletionMessageToolCallTypeFunction,
								},
							},
						}, Type: openai.ChatMessageRoleAssistant,
					},
					{
						Value: openai.ChatCompletionAssistantMessageParam{
							Content: openai.StringOrAssistantRoleContentUnion{
								Value: "I also dunno",
							},
						}, Type: openai.ChatMessageRoleAssistant,
					},
					{
						Value: openai.ChatCompletionAssistantMessageParam{
							Content: openai.StringOrAssistantRoleContentUnion{
								Value: "",
							},
						}, Type: openai.ChatMessageRoleAssistant,
					},
				},
			},
			output: awsbedrock.ConverseInput{
				InferenceConfig: &awsbedrock.InferenceConfiguration{},
				System: []*awsbedrock.SystemContentBlock{
					{
						Text: "from-system",
					},
					{
						Text: "from-developer",
					},
				},
				Messages: []*awsbedrock.Message{
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("from-user"),
							},
						},
					},
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("part1"),
							},
						},
					},
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("part2"),
							},
						},
					},
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								ToolResult: &awsbedrock.ToolResultBlock{
									ToolUseID: ptr.To("call_6g7a"),
									Content: []*awsbedrock.ToolResultContentBlock{
										{
											Text: ptr.To("Weather in Queens, NY is 70F and clear skies."),
										},
									},
								},
							},
						},
					},
					{
						Role: openai.ChatMessageRoleAssistant,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("I dunno"),
							},
							{
								ToolUse: &awsbedrock.ToolUseBlock{
									Name:      "exec_python_code",
									ToolUseID: "call_6g7a",
									Input:     map[string]interface{}{"code_block": "from playwright.sync_api import sync_playwright\n"},
								},
							},
						},
					},
					{
						Role: openai.ChatMessageRoleAssistant,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("I also dunno"),
							},
						},
					},
					{
						Role:    openai.ChatMessageRoleAssistant,
						Content: []*awsbedrock.ContentBlock{},
					},
				},
			},
		},
		{
			name: "test content array",
			input: openai.ChatCompletionRequest{
				Stream: false,
				Model:  "gpt-4o",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionSystemMessageParam{
							Content: openai.StringOrArray{
								Value: []openai.ChatCompletionContentPartTextParam{
									{Text: "from-system"},
								},
							},
						}, Type: openai.ChatMessageRoleSystem,
					},
					{
						Value: openai.ChatCompletionDeveloperMessageParam{
							Content: openai.StringOrArray{
								Value: []openai.ChatCompletionContentPartTextParam{
									{Text: "from-developer"},
								},
							},
						}, Type: openai.ChatMessageRoleDeveloper,
					},
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: []openai.ChatCompletionContentPartUserUnionParam{
									{TextContent: &openai.ChatCompletionContentPartTextParam{Text: "from-user"}},
								},
							},
						}, Type: openai.ChatMessageRoleUser,
					},
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: []openai.ChatCompletionContentPartUserUnionParam{
									{TextContent: &openai.ChatCompletionContentPartTextParam{Text: "user1"}},
								},
							},
						}, Type: openai.ChatMessageRoleUser,
					},
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: []openai.ChatCompletionContentPartUserUnionParam{
									{TextContent: &openai.ChatCompletionContentPartTextParam{Text: "user2"}},
								},
							},
						}, Type: openai.ChatMessageRoleUser,
					},
				},
			},
			output: awsbedrock.ConverseInput{
				InferenceConfig: &awsbedrock.InferenceConfiguration{},
				System: []*awsbedrock.SystemContentBlock{
					{
						Text: "from-system",
					},
					{
						Text: "from-developer",
					},
				},
				Messages: []*awsbedrock.Message{
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("from-user"),
							},
						},
					},
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("user1"),
							},
						},
					},
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("user2"),
							},
						},
					},
				},
			},
		},
		{
			name: "test image",
			input: openai.ChatCompletionRequest{
				Stream: false,
				Model:  "gpt-4o",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionSystemMessageParam{
							Content: openai.StringOrArray{
								Value: []openai.ChatCompletionContentPartTextParam{
									{Text: "from-system"},
								},
							},
						}, Type: openai.ChatMessageRoleSystem,
					},
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: []openai.ChatCompletionContentPartUserUnionParam{
									{ImageContent: &openai.ChatCompletionContentPartImageParam{
										ImageURL: openai.ChatCompletionContentPartImageImageURLParam{
											URL: "data:image/jpeg;base64,dGVzdA==",
										},
									}},
								},
							},
						}, Type: openai.ChatMessageRoleUser,
					},
				},
			},
			output: awsbedrock.ConverseInput{
				InferenceConfig: &awsbedrock.InferenceConfiguration{},
				System: []*awsbedrock.SystemContentBlock{
					{
						Text: "from-system",
					},
				},
				Messages: []*awsbedrock.Message{
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Image: &awsbedrock.ImageBlock{
									Source: awsbedrock.ImageSource{
										Bytes: []byte("test"),
									},
									Format: "jpeg",
								},
							},
						},
					},
				},
			},
		},
		{
			name: "test parameters",
			input: openai.ChatCompletionRequest{
				Stream:      false,
				Model:       "gpt-4o",
				MaxTokens:   ptr.To(int64(10)),
				TopP:        ptr.To(float64(1)),
				Temperature: ptr.To(0.7),
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "from-user",
							},
						}, Type: openai.ChatMessageRoleUser,
					},
				},
			},
			output: awsbedrock.ConverseInput{
				InferenceConfig: &awsbedrock.InferenceConfiguration{
					MaxTokens:   ptr.To(int64(10)),
					TopP:        ptr.To(float64(1)),
					Temperature: ptr.To(0.7),
				},
				Messages: []*awsbedrock.Message{
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("from-user"),
							},
						},
					},
				},
			},
		},
		{
			name: "test tools function calling with empty tool choice",
			input: openai.ChatCompletionRequest{
				Stream:      false,
				Model:       "gpt-4o",
				MaxTokens:   ptr.To(int64(10)),
				TopP:        ptr.To(float64(1)),
				Temperature: ptr.To(0.7),
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "from-user",
							},
						}, Type: openai.ChatMessageRoleUser,
					},
				},
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "get_current_weather",
							Description: "Get the current weather in a given location",
							Parameters: map[string]interface{}{
								"type": "object",
								"properties": map[string]interface{}{
									"location": map[string]interface{}{
										"type":        "string",
										"description": "The city and state, e.g. San Francisco, CA",
									},
									"unit": map[string]interface{}{
										"type": "string",
										"enum": []string{"celsius", "fahrenheit"},
									},
								},
								"required": []string{"location"},
							},
						},
					},
				},
			},
			output: awsbedrock.ConverseInput{
				InferenceConfig: &awsbedrock.InferenceConfiguration{
					MaxTokens:   ptr.To(int64(10)),
					TopP:        ptr.To(float64(1)),
					Temperature: ptr.To(0.7),
				},
				Messages: []*awsbedrock.Message{
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("from-user"),
							},
						},
					},
				},
				ToolConfig: &awsbedrock.ToolConfiguration{
					Tools: []*awsbedrock.Tool{
						{
							ToolSpec: &awsbedrock.ToolSpecification{
								Name:        ptr.To("get_current_weather"),
								Description: ptr.To("Get the current weather in a given location"),
								InputSchema: &awsbedrock.ToolInputSchema{
									JSON: map[string]interface{}{
										"type": "object",
										"properties": map[string]interface{}{
											"location": map[string]interface{}{
												"type":        "string",
												"description": "The city and state, e.g. San Francisco, CA",
											},
											"unit": map[string]interface{}{
												"type": "string",
												"enum": []any{"celsius", "fahrenheit"},
											},
										},
										"required": []any{"location"},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "test auto tool choice",
			input: openai.ChatCompletionRequest{
				Model: "gpt-4o",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "from-user",
							},
						}, Type: openai.ChatMessageRoleUser,
					},
				},
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "get_current_weather",
							Description: "Get the current weather in a given location",
						},
					},
				},
				ToolChoice: "auto",
			},
			output: awsbedrock.ConverseInput{
				InferenceConfig: &awsbedrock.InferenceConfiguration{},
				Messages: []*awsbedrock.Message{
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("from-user"),
							},
						},
					},
				},
				ToolConfig: &awsbedrock.ToolConfiguration{
					Tools: []*awsbedrock.Tool{
						{
							ToolSpec: &awsbedrock.ToolSpecification{
								Name:        ptr.To("get_current_weather"),
								Description: ptr.To("Get the current weather in a given location"),
								InputSchema: &awsbedrock.ToolInputSchema{},
							},
						},
					},
					ToolChoice: &awsbedrock.ToolChoice{Auto: &awsbedrock.AutoToolChoice{}},
				},
			},
		},
		{
			name: "test required tool choice",
			input: openai.ChatCompletionRequest{
				Model: "gpt-4o",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "from-user",
							},
						}, Type: openai.ChatMessageRoleUser,
					},
				},
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "get_current_weather",
							Description: "Get the current weather in a given location",
						},
					},
				},
				ToolChoice: "required",
			},
			output: awsbedrock.ConverseInput{
				InferenceConfig: &awsbedrock.InferenceConfiguration{},
				Messages: []*awsbedrock.Message{
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("from-user"),
							},
						},
					},
				},
				ToolConfig: &awsbedrock.ToolConfiguration{
					Tools: []*awsbedrock.Tool{
						{
							ToolSpec: &awsbedrock.ToolSpecification{
								Name:        ptr.To("get_current_weather"),
								Description: ptr.To("Get the current weather in a given location"),
								InputSchema: &awsbedrock.ToolInputSchema{},
							},
						},
					},
					ToolChoice: &awsbedrock.ToolChoice{Any: &awsbedrock.AnyToolChoice{}},
				},
			},
		},
		{
			name: "test tool choice for anthropic claude model",
			input: openai.ChatCompletionRequest{
				Model: "bedrock.anthropic.claude-3-5-sonnet-20240620-v1:0",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "from-user",
							},
						}, Type: openai.ChatMessageRoleUser,
					},
				},
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "get_current_weather",
							Description: "Get the current weather in a given location",
						},
					},
				},
				ToolChoice: "some-tools",
			},
			output: awsbedrock.ConverseInput{
				InferenceConfig: &awsbedrock.InferenceConfiguration{},
				Messages: []*awsbedrock.Message{
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("from-user"),
							},
						},
					},
				},
				ToolConfig: &awsbedrock.ToolConfiguration{
					Tools: []*awsbedrock.Tool{
						{
							ToolSpec: &awsbedrock.ToolSpecification{
								Name:        ptr.To("get_current_weather"),
								Description: ptr.To("Get the current weather in a given location"),
								InputSchema: &awsbedrock.ToolInputSchema{},
							},
						},
					},
					ToolChoice: &awsbedrock.ToolChoice{
						Tool: &awsbedrock.SpecificToolChoice{
							Name: ptr.To("some-tools"),
						},
					},
				},
			},
		},
		{
			name: "test tool choices for anthropic claude model",
			input: openai.ChatCompletionRequest{
				Model: "bedrock.anthropic.claude-3-5-sonnet-20240620-v1:0",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "from-user",
							},
						}, Type: openai.ChatMessageRoleUser,
					},
				},
				Tools: []openai.Tool{
					{
						Type: "function",
						Function: &openai.FunctionDefinition{
							Name:        "get_current_weather",
							Description: "Get the current weather in a given location",
						},
					},
				},
				ToolChoice: openai.ToolChoice{
					Type: openai.ToolType("function"),
					Function: openai.ToolFunction{
						Name: "my_function",
					},
				},
			},
			output: awsbedrock.ConverseInput{
				InferenceConfig: &awsbedrock.InferenceConfiguration{},
				Messages: []*awsbedrock.Message{
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("from-user"),
							},
						},
					},
				},
				ToolConfig: &awsbedrock.ToolConfiguration{
					Tools: []*awsbedrock.Tool{
						{
							ToolSpec: &awsbedrock.ToolSpecification{
								Name:        ptr.To("get_current_weather"),
								Description: ptr.To("Get the current weather in a given location"),
								InputSchema: &awsbedrock.ToolInputSchema{},
							},
						},
					},
					ToolChoice: &awsbedrock.ToolChoice{
						Tool: &awsbedrock.SpecificToolChoice{
							Name: ptr.To("function"),
						},
					},
				},
			},
		},
		{
			name: "test single stop word",
			input: openai.ChatCompletionRequest{
				Model: "gpt-4o",
				Messages: []openai.ChatCompletionMessageParamUnion{
					{
						Value: openai.ChatCompletionUserMessageParam{
							Content: openai.StringOrUserRoleContentUnion{
								Value: "from-user",
							},
						}, Type: openai.ChatMessageRoleUser,
					},
				},
				Stop: []*string{ptr.To("stop_only")},
			},
			output: awsbedrock.ConverseInput{
				InferenceConfig: &awsbedrock.InferenceConfiguration{
					StopSequences: []*string{ptr.To("stop_only")},
				},
				Messages: []*awsbedrock.Message{
					{
						Role: openai.ChatMessageRoleUser,
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("from-user"),
							},
						},
					},
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
			originalReq := tt.input
			hm, bm, err := o.RequestBody(nil, &originalReq, false)
			var expPath string
			require.Equal(t, tt.input.Stream, o.stream)
			if tt.input.Stream {
				expPath = fmt.Sprintf("/model/%s/converse-stream", tt.input.Model)
			} else {
				expPath = fmt.Sprintf("/model/%s/converse", tt.input.Model)
			}
			require.NoError(t, err)
			require.NotNil(t, hm)
			require.NotNil(t, hm.SetHeaders)
			require.Len(t, hm.SetHeaders, 2)
			require.Equal(t, ":path", hm.SetHeaders[0].Header.Key)
			require.Equal(t, expPath, string(hm.SetHeaders[0].Header.RawValue))
			require.Equal(t, "content-length", hm.SetHeaders[1].Header.Key)
			newBody := bm.Mutation.(*extprocv3.BodyMutation_Body).Body
			require.Equal(t, strconv.Itoa(len(newBody)), string(hm.SetHeaders[1].Header.RawValue))

			var awsReq awsbedrock.ConverseInput
			err = json.Unmarshal(newBody, &awsReq)
			require.NoError(t, err)
			if !cmp.Equal(awsReq, tt.output) {
				t.Errorf("ConvertOpenAIToBedrock(), diff(got, expected) = %s\n", cmp.Diff(awsReq, tt.output))
			}
		})
	}

	t.Run("model override", func(t *testing.T) {
		modelNameOverride := "bedrock.anthropic.claude-3-5-sonnet-20240620-v1:0"
		o := &openAIToAWSBedrockTranslatorV1ChatCompletion{modelNameOverride: modelNameOverride}
		originalReq := openai.ChatCompletionRequest{
			Model:    "claude-3-5-sonnet",
			Messages: []openai.ChatCompletionMessageParamUnion{},
		}
		hm, _, err := o.RequestBody(nil, &originalReq, false)
		require.NoError(t, err)
		require.NotNil(t, hm)
		require.NotNil(t, hm.SetHeaders)
		require.Len(t, hm.SetHeaders, 2)
		require.Equal(t, ":path", hm.SetHeaders[0].Header.Key)
		require.Equal(t, "/model/"+modelNameOverride+"/converse", string(hm.SetHeaders[0].Header.RawValue))
	})
}

func TestOpenAIToAWSBedrockTranslatorV1ChatCompletion_ResponseHeaders(t *testing.T) {
	t.Run("streaming", func(t *testing.T) {
		o := &openAIToAWSBedrockTranslatorV1ChatCompletion{stream: true}
		hm, err := o.ResponseHeaders(map[string]string{
			"content-type": "application/vnd.amazon.eventstream",
		})
		require.NoError(t, err)
		require.NotNil(t, hm)
		require.NotNil(t, hm.SetHeaders)
		require.Len(t, hm.SetHeaders, 1)
		require.Equal(t, "content-type", hm.SetHeaders[0].Header.Key)
		require.Equal(t, "text/event-stream", hm.SetHeaders[0].Header.Value)
	})
	t.Run("non-streaming", func(t *testing.T) {
		o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
		hm, err := o.ResponseHeaders(nil)
		require.NoError(t, err)
		require.Nil(t, hm)
	})
}

func TestOpenAIToAWSBedrockTranslatorV1ChatCompletion_Streaming_ResponseBody(t *testing.T) {
	t.Run("streaming", func(t *testing.T) {
		o := &openAIToAWSBedrockTranslatorV1ChatCompletion{stream: true}
		buf, err := base64.StdEncoding.DecodeString(base64RealStreamingEvents)
		require.NoError(t, err)

		var results []string
		for i := 0; i < len(buf); i++ {
			hm, bm, tokenUsage, err := o.ResponseBody(nil, bytes.NewBuffer([]byte{buf[i]}), i == len(buf)-1)
			require.NoError(t, err)
			require.Nil(t, hm)
			require.NotNil(t, bm)
			require.NotNil(t, bm.Mutation)
			newBody := bm.Mutation.(*extprocv3.BodyMutation_Body).Body
			if len(newBody) > 0 {
				results = append(results, string(newBody))
			}
			if tokenUsage.OutputTokens > 0 {
				require.Equal(t, uint32(75), tokenUsage.OutputTokens)
			}
		}

		result := strings.Join(results, "")

		require.Equal(t,
			`data: {"choices":[{"delta":{"content":"","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":"To","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" calculate the cosine","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" of 7,","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" we can use the","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" \"","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":"cosine\" function","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" that","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" is","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" available to","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" us.","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" Let","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":"'s use","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" this","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" function to","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" get","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" the result","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":".","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"id":"tooluse_QklrEHKjRu6Oc4BQUfy7ZQ","function":{"arguments":"","name":"cosine"},"type":"function"}]}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"id":"","function":{"arguments":"","name":""},"type":"function"}]}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"role":"assistant","tool_calls":[{"id":"","function":{"arguments":"{\"x\": 7}","name":""},"type":"function"}]}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":"","role":"assistant"},"finish_reason":"tool_calls"}],"object":"chat.completion.chunk"}

data: {"object":"chat.completion.chunk","usage":{"completion_tokens":75,"prompt_tokens":386,"total_tokens":461}}

data: [DONE]
`, result)
	})
}

func TestOpenAIToAWSBedrockTranslator_ResponseError(t *testing.T) {
	tests := []struct {
		name            string
		responseHeaders map[string]string
		input           io.Reader
		output          openai.Error
	}{
		{
			name: "test unhealthy upstream",
			responseHeaders: map[string]string{
				":status":      "503",
				"content-type": "text/plain",
			},
			input: bytes.NewBuffer([]byte("service not available")),
			output: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    awsBedrockBackendError,
					Code:    ptr.To("503"),
					Message: "service not available",
				},
			},
		},
		{
			name: "test AWS throttled error response",
			responseHeaders: map[string]string{
				":status":              "429",
				"content-type":         "application/json",
				awsErrorTypeHeaderName: "ThrottledException",
			},
			input: bytes.NewBuffer([]byte(`{"message": "aws bedrock rate limit exceeded"}`)),
			output: openai.Error{
				Type: "error",
				Error: openai.ErrorType{
					Type:    "ThrottledException",
					Code:    ptr.To("429"),
					Message: "aws bedrock rate limit exceeded",
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := json.Marshal(tt.input)
			require.NoError(t, err)

			o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
			hm, bm, err := o.ResponseError(tt.responseHeaders, tt.input)
			require.NoError(t, err)
			require.NotNil(t, bm)
			require.NotNil(t, bm.Mutation)
			require.NotNil(t, bm.Mutation.(*extprocv3.BodyMutation_Body))
			newBody := bm.Mutation.(*extprocv3.BodyMutation_Body).Body
			require.NotNil(t, newBody)
			require.NotNil(t, hm)
			require.NotNil(t, hm.SetHeaders)
			require.Len(t, hm.SetHeaders, 1)
			require.Equal(t, "content-length", hm.SetHeaders[0].Header.Key)
			require.Equal(t, strconv.Itoa(len(newBody)), string(hm.SetHeaders[0].Header.RawValue))

			var openAIError openai.Error
			err = json.Unmarshal(newBody, &openAIError)
			require.NoError(t, err)
			if !cmp.Equal(openAIError, tt.output) {
				t.Errorf("ConvertAWSBedrockErrorResp(), diff(got, expected) = %s\n", cmp.Diff(openAIError, tt.output))
			}
		})
	}
}

func TestOpenAIToAWSBedrockTranslatorV1ChatCompletion_ResponseBody(t *testing.T) {
	t.Run("invalid body", func(t *testing.T) {
		o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
		_, _, _, err := o.ResponseBody(nil, bytes.NewBuffer([]byte("invalid")), false)
		require.Error(t, err)
	})
	tests := []struct {
		name   string
		input  awsbedrock.ConverseResponse
		output openai.ChatCompletionResponse
	}{
		{
			name: "basic_testing",
			input: awsbedrock.ConverseResponse{
				Usage: &awsbedrock.TokenUsage{
					InputTokens:  10,
					OutputTokens: 20,
					TotalTokens:  30,
				},
				Output: &awsbedrock.ConverseOutput{
					Message: awsbedrock.Message{
						Role: "assistant",
						Content: []*awsbedrock.ContentBlock{
							{Text: ptr.To("response")},
							{Text: ptr.To("from")},
							{Text: ptr.To("assistant")},
						},
					},
				},
			},
			output: openai.ChatCompletionResponse{
				Object: "chat.completion",
				Usage: openai.ChatCompletionResponseUsage{
					TotalTokens:      30,
					PromptTokens:     10,
					CompletionTokens: 20,
				},
				Choices: []openai.ChatCompletionResponseChoice{
					{
						Index: 0,
						Message: openai.ChatCompletionResponseChoiceMessage{
							Content: ptr.To("response"),
							Role:    awsbedrock.ConversationRoleAssistant,
						},
						FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
					},
				},
			},
		},
		{
			name: "test stop reason",
			input: awsbedrock.ConverseResponse{
				Usage: &awsbedrock.TokenUsage{
					InputTokens:  10,
					OutputTokens: 20,
					TotalTokens:  30,
				},
				StopReason: ptr.To("stop_sequence"),
				Output: &awsbedrock.ConverseOutput{
					Message: awsbedrock.Message{
						Role: awsbedrock.ConversationRoleAssistant,
						Content: []*awsbedrock.ContentBlock{
							{Text: ptr.To("response")},
						},
					},
				},
			},
			output: openai.ChatCompletionResponse{
				Object: "chat.completion",
				Usage: openai.ChatCompletionResponseUsage{
					TotalTokens:      30,
					PromptTokens:     10,
					CompletionTokens: 20,
				},
				Choices: []openai.ChatCompletionResponseChoice{
					{
						Index:        0,
						FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
						Message: openai.ChatCompletionResponseChoiceMessage{
							Content: ptr.To("response"),
							Role:    awsbedrock.ConversationRoleAssistant,
						},
					},
				},
			},
		},
		{
			name: "test tool use",
			input: awsbedrock.ConverseResponse{
				StopReason: ptr.To(awsbedrock.StopReasonToolUse),
				Output: &awsbedrock.ConverseOutput{
					Message: awsbedrock.Message{
						Role: awsbedrock.ConversationRoleAssistant,
						// Text and ToolUse are sent in two different content blocks for AWS Bedrock, OpenAI merges them in one message.
						Content: []*awsbedrock.ContentBlock{
							{
								Text: ptr.To("response"),
							},
							{
								ToolUse: &awsbedrock.ToolUseBlock{
									Name:      "exec_python_code",
									ToolUseID: "call_6g7a",
									Input:     map[string]interface{}{"code_block": "from playwright.sync_api import sync_playwright\n"},
								},
							},
						},
					},
				},
			},
			output: openai.ChatCompletionResponse{
				Object: "chat.completion",
				Choices: []openai.ChatCompletionResponseChoice{
					{
						Index:        0,
						FinishReason: openai.ChatCompletionChoicesFinishReasonToolCalls,
						Message: openai.ChatCompletionResponseChoiceMessage{
							Content: ptr.To("response"),
							Role:    awsbedrock.ConversationRoleAssistant,
							ToolCalls: []openai.ChatCompletionMessageToolCallParam{
								{
									ID: "call_6g7a",
									Function: openai.ChatCompletionMessageToolCallFunctionParam{
										Name:      "exec_python_code",
										Arguments: "{\"code_block\":\"from playwright.sync_api import sync_playwright\\n\"}",
									},
									Type: openai.ChatCompletionMessageToolCallTypeFunction,
								},
							},
						},
					},
				},
			},
		},
		{
			name: "merge content",
			input: awsbedrock.ConverseResponse{
				Usage: &awsbedrock.TokenUsage{
					InputTokens:  10,
					OutputTokens: 20,
					TotalTokens:  30,
				},
				Output: &awsbedrock.ConverseOutput{
					Message: awsbedrock.Message{
						Role: awsbedrock.ConversationRoleAssistant,
						Content: []*awsbedrock.ContentBlock{
							{Text: ptr.To("response")},
							{ToolUse: &awsbedrock.ToolUseBlock{
								Name:      "exec_python_code",
								ToolUseID: "call_6g7a",
								Input:     map[string]interface{}{"code_block": "from playwright.sync_api import sync_playwright\n"},
							}},
						},
					},
				},
			},
			output: openai.ChatCompletionResponse{
				Object: "chat.completion",
				Usage: openai.ChatCompletionResponseUsage{
					TotalTokens:      30,
					PromptTokens:     10,
					CompletionTokens: 20,
				},
				Choices: []openai.ChatCompletionResponseChoice{
					{
						Index: 0,
						Message: openai.ChatCompletionResponseChoiceMessage{
							Content: ptr.To("response"),
							Role:    awsbedrock.ConversationRoleAssistant,
							ToolCalls: []openai.ChatCompletionMessageToolCallParam{
								{
									ID: "call_6g7a",
									Function: openai.ChatCompletionMessageToolCallFunctionParam{
										Name:      "exec_python_code",
										Arguments: "{\"code_block\":\"from playwright.sync_api import sync_playwright\\n\"}",
									},
									Type: openai.ChatCompletionMessageToolCallTypeFunction,
								},
							},
						},
						FinishReason: openai.ChatCompletionChoicesFinishReasonStop,
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.input)
			require.NoError(t, err)

			o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
			hm, bm, usedToken, err := o.ResponseBody(nil, bytes.NewBuffer(body), false)
			require.NoError(t, err)
			require.NotNil(t, bm)
			require.NotNil(t, bm.Mutation)
			require.NotNil(t, bm.Mutation.(*extprocv3.BodyMutation_Body))
			newBody := bm.Mutation.(*extprocv3.BodyMutation_Body).Body
			require.NotNil(t, newBody)
			require.NotNil(t, hm)
			require.NotNil(t, hm.SetHeaders)
			require.Len(t, hm.SetHeaders, 1)
			require.Equal(t, "content-length", hm.SetHeaders[0].Header.Key)
			require.Equal(t, strconv.Itoa(len(newBody)), string(hm.SetHeaders[0].Header.RawValue))

			var openAIResp openai.ChatCompletionResponse
			err = json.Unmarshal(newBody, &openAIResp)
			require.NoError(t, err)
			require.Equal(t,
				LLMTokenUsage{
					InputTokens:  uint32(tt.output.Usage.PromptTokens),     //nolint:gosec
					OutputTokens: uint32(tt.output.Usage.CompletionTokens), //nolint:gosec
					TotalTokens:  uint32(tt.output.Usage.TotalTokens),      //nolint:gosec
				}, usedToken)
			if !cmp.Equal(openAIResp, tt.output) {
				t.Errorf("ConvertOpenAIToBedrock(), diff(got, expected) = %s\n", cmp.Diff(openAIResp, tt.output))
			}
		})
	}
}

// base64RealStreamingEvents is the base64 encoded raw binary response from bedrock anthropic.claude model.
// The request is to find the cosine of number 7 with a tool configuration.
/*
{
  "messages": [
    {
      "role": "user",
      "content": [{"text": "What is the cosine of 7?"}]
    }
  ],
  "toolConfig": {
    "tools": [
      {
        "toolSpec": {
          "name": "cosine",
          "description": "Calculate the cosine of x.",
          "inputSchema": {
            "json": {
              "type": "object",
              "properties": {
                "x": {
                  "type": "number",
                  "description": "The number to pass to the function."
                }
              },
              "required": ["x"]
            }
          }
        }
      }
    ]
  },
  "system": [{"text": "You must only do math by using a tool."}]
}
*/

const base64RealStreamingEvents = "AAAAmwAAAFJGkfmwCzpldmVudC10eXBlBwAMbWVzc2FnZVN0YXJ0DTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsicCI6ImFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6QUJDRCIsInJvbGUiOiJhc3Npc3RhbnQifbCidJ0AAACpAAAAV+0a5tkLOmV2ZW50LXR5cGUHABFjb250ZW50QmxvY2tEZWx0YQ06Y29udGVudC10eXBlBwAQYXBwbGljYXRpb24vanNvbg06bWVzc2FnZS10eXBlBwAFZXZlbnR7ImNvbnRlbnRCbG9ja0luZGV4IjowLCJkZWx0YSI6eyJ0ZXh0IjoiVG8ifSwicCI6ImFiY2RlZmdoaWprbG1uIn0rY75JAAAAsQAAAFe9ijqaCzpldmVudC10eXBlBwARY29udGVudEJsb2NrRGVsdGENOmNvbnRlbnQtdHlwZQcAEGFwcGxpY2F0aW9uL2pzb24NOm1lc3NhZ2UtdHlwZQcABWV2ZW50eyJjb250ZW50QmxvY2tJbmRleCI6MCwiZGVsdGEiOnsidGV4dCI6IiBjYWxjdWxhdGUgdGhlIGNvc2luZSJ9LCJwIjoiYWJjIn3hywqfAAAA2gAAAFdTaHzGCzpldmVudC10eXBlBwARY29udGVudEJsb2NrRGVsdGENOmNvbnRlbnQtdHlwZQcAEGFwcGxpY2F0aW9uL2pzb24NOm1lc3NhZ2UtdHlwZQcABWV2ZW50eyJjb250ZW50QmxvY2tJbmRleCI6MCwiZGVsdGEiOnsidGV4dCI6IiBvZiA3LCJ9LCJwIjoiYWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXpBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWjAxMjM0NTYifUsRwHsAAADXAAAAV6v4uHcLOmV2ZW50LXR5cGUHABFjb250ZW50QmxvY2tEZWx0YQ06Y29udGVudC10eXBlBwAQYXBwbGljYXRpb24vanNvbg06bWVzc2FnZS10eXBlBwAFZXZlbnR7ImNvbnRlbnRCbG9ja0luZGV4IjowLCJkZWx0YSI6eyJ0ZXh0IjoiIHdlIGNhbiB1c2UgdGhlIn0sInAiOiJhYmNkZWZnaGlqa2xtbm9wcXJzdHV2d3h5ekFCQ0RFRkdISUpLTE1OT1BRUlNUVSJ9jBuxjAAAALwAAABXRRr+Kws6ZXZlbnQtdHlwZQcAEWNvbnRlbnRCbG9ja0RlbHRhDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudEJsb2NrSW5kZXgiOjAsImRlbHRhIjp7InRleHQiOiIgXCIifSwicCI6ImFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6QUJDREVGIn3SOp66AAAA2wAAAFduCFV2CzpldmVudC10eXBlBwARY29udGVudEJsb2NrRGVsdGENOmNvbnRlbnQtdHlwZQcAEGFwcGxpY2F0aW9uL2pzb24NOm1lc3NhZ2UtdHlwZQcABWV2ZW50eyJjb250ZW50QmxvY2tJbmRleCI6MCwiZGVsdGEiOnsidGV4dCI6ImNvc2luZVwiIGZ1bmN0aW9uIn0sInAiOiJhYmNkZWZnaGlqa2xtbm9wcXJzdHV2d3h5ekFCQ0RFRkdISUpLTE1OT1BRUlNUVVZXIn2f+1UQAAAA2QAAAFcUyAYWCzpldmVudC10eXBlBwARY29udGVudEJsb2NrRGVsdGENOmNvbnRlbnQtdHlwZQcAEGFwcGxpY2F0aW9uL2pzb24NOm1lc3NhZ2UtdHlwZQcABWV2ZW50eyJjb250ZW50QmxvY2tJbmRleCI6MCwiZGVsdGEiOnsidGV4dCI6IiB0aGF0In0sInAiOiJhYmNkZWZnaGlqa2xtbm9wcXJzdHV2d3h5ekFCQ0RFRkdISUpLTE1OT1BRUlNUVVZXWFlaMDEyMzQ1NiJ9uD7t8wAAAM8AAABX+2hkNAs6ZXZlbnQtdHlwZQcAEWNvbnRlbnRCbG9ja0RlbHRhDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudEJsb2NrSW5kZXgiOjAsImRlbHRhIjp7InRleHQiOiIgaXMifSwicCI6ImFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6QUJDREVGR0hJSktMTU5PUFFSU1RVVldYWSJ9p52nrQAAAMQAAABXjLhVJQs6ZXZlbnQtdHlwZQcAEWNvbnRlbnRCbG9ja0RlbHRhDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudEJsb2NrSW5kZXgiOjAsImRlbHRhIjp7InRleHQiOiIgYXZhaWxhYmxlIHRvIn0sInAiOiJhYmNkZWZnaGlqa2xtbm9wcXJzdHV2d3h5ekFCQ0QifYC08b0AAADTAAAAV154HrcLOmV2ZW50LXR5cGUHABFjb250ZW50QmxvY2tEZWx0YQ06Y29udGVudC10eXBlBwAQYXBwbGljYXRpb24vanNvbg06bWVzc2FnZS10eXBlBwAFZXZlbnR7ImNvbnRlbnRCbG9ja0luZGV4IjowLCJkZWx0YSI6eyJ0ZXh0IjoiIHVzLiJ9LCJwIjoiYWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXpBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWjAxIn0mTm4jAAAAtAAAAFd1arXqCzpldmVudC10eXBlBwARY29udGVudEJsb2NrRGVsdGENOmNvbnRlbnQtdHlwZQcAEGFwcGxpY2F0aW9uL2pzb24NOm1lc3NhZ2UtdHlwZQcABWV2ZW50eyJjb250ZW50QmxvY2tJbmRleCI6MCwiZGVsdGEiOnsidGV4dCI6IiBMZXQifSwicCI6ImFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3In34BFwTAAAA0AAAAFcZ2GRnCzpldmVudC10eXBlBwARY29udGVudEJsb2NrRGVsdGENOmNvbnRlbnQtdHlwZQcAEGFwcGxpY2F0aW9uL2pzb24NOm1lc3NhZ2UtdHlwZQcABWV2ZW50eyJjb250ZW50QmxvY2tJbmRleCI6MCwiZGVsdGEiOnsidGV4dCI6IidzIHVzZSJ9LCJwIjoiYWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXpBQkNERUZHSElKS0xNTk9QUVJTVFVWVyJ9vfdBjQAAALwAAABXRRr+Kws6ZXZlbnQtdHlwZQcAEWNvbnRlbnRCbG9ja0RlbHRhDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudEJsb2NrSW5kZXgiOjAsImRlbHRhIjp7InRleHQiOiIgdGhpcyJ9LCJwIjoiYWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXpBQkNEIn1Xtb4jAAAAuAAAAFewmljrCzpldmVudC10eXBlBwARY29udGVudEJsb2NrRGVsdGENOmNvbnRlbnQtdHlwZQcAEGFwcGxpY2F0aW9uL2pzb24NOm1lc3NhZ2UtdHlwZQcABWV2ZW50eyJjb250ZW50QmxvY2tJbmRleCI6MCwiZGVsdGEiOnsidGV4dCI6IiBmdW5jdGlvbiB0byJ9LCJwIjoiYWJjZGVmZ2hpamtsbW5vcHFycyJ9GYv84AAAALQAAABXdWq16gs6ZXZlbnQtdHlwZQcAEWNvbnRlbnRCbG9ja0RlbHRhDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudEJsb2NrSW5kZXgiOjAsImRlbHRhIjp7InRleHQiOiIgZ2V0In0sInAiOiJhYmNkZWZnaGlqa2xtbm9wcXJzdHV2dyJ99bdUOgAAAN4AAABXpujaBgs6ZXZlbnQtdHlwZQcAEWNvbnRlbnRCbG9ja0RlbHRhDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudEJsb2NrSW5kZXgiOjAsImRlbHRhIjp7InRleHQiOiIgdGhlIHJlc3VsdCJ9LCJwIjoiYWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXpBQkNERUZHSElKS0xNTk9QUVJTVFVWV1hZWjAxMjM0NSJ9niPS/gAAAM0AAABXgag3VAs6ZXZlbnQtdHlwZQcAEWNvbnRlbnRCbG9ja0RlbHRhDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudEJsb2NrSW5kZXgiOjAsImRlbHRhIjp7InRleHQiOiIuIn0sInAiOiJhYmNkZWZnaGlqa2xtbm9wcXJzdHV2d3h5ekFCQ0RFRkdISUpLTE1OT1BRUlNUVVZXWFkifRc68JQAAACuAAAAVig9Cl8LOmV2ZW50LXR5cGUHABBjb250ZW50QmxvY2tTdG9wDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudEJsb2NrSW5kZXgiOjAsInAiOiJhYmNkZWZnaGlqa2xtbm9wcXJzdHV2d3h5ekFCQ0RFRkdISUpLTE1OT1AifY2eizoAAAEEAAAAV67xblsLOmV2ZW50LXR5cGUHABFjb250ZW50QmxvY2tTdGFydA06Y29udGVudC10eXBlBwAQYXBwbGljYXRpb24vanNvbg06bWVzc2FnZS10eXBlBwAFZXZlbnR7ImNvbnRlbnRCbG9ja0luZGV4IjoxLCJwIjoiYWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXpBQkNERUZHSElKS0xNTk9QUVIiLCJzdGFydCI6eyJ0b29sVXNlIjp7Im5hbWUiOiJjb3NpbmUiLCJ0b29sVXNlSWQiOiJ0b29sdXNlX1FrbHJFSEtqUnU2T2M0QlFVZnk3WlEifX19kpNGawAAAK0AAABXGJpAGQs6ZXZlbnQtdHlwZQcAEWNvbnRlbnRCbG9ja0RlbHRhDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudEJsb2NrSW5kZXgiOjEsImRlbHRhIjp7InRvb2xVc2UiOnsiaW5wdXQiOiIifX0sInAiOiJhYmNkZWZnIn3XeK+kAAAAswAAAFfHSmn6CzpldmVudC10eXBlBwARY29udGVudEJsb2NrRGVsdGENOmNvbnRlbnQtdHlwZQcAEGFwcGxpY2F0aW9uL2pzb24NOm1lc3NhZ2UtdHlwZQcABWV2ZW50eyJjb250ZW50QmxvY2tJbmRleCI6MSwiZGVsdGEiOnsidG9vbFVzZSI6eyJpbnB1dCI6IntcInhcIjogN30ifX0sInAiOiJhYmMifaN4jhsAAACxAAAAVsqNCgwLOmV2ZW50LXR5cGUHABBjb250ZW50QmxvY2tTdG9wDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudEJsb2NrSW5kZXgiOjEsInAiOiJhYmNkZWZnaGlqa2xtbm9wcXJzdHV2d3h5ekFCQ0RFRkdISUpLTE1OT1BRUlMifUJp3UkAAACFAAAAUQBIgekLOmV2ZW50LXR5cGUHAAttZXNzYWdlU3RvcA06Y29udGVudC10eXBlBwAQYXBwbGljYXRpb24vanNvbg06bWVzc2FnZS10eXBlBwAFZXZlbnR7InAiOiJhYmNkIiwic3RvcFJlYXNvbiI6InRvb2xfdXNlIn3ejv14AAAAygAAAE5X40OECzpldmVudC10eXBlBwAIbWV0YWRhdGENOmNvbnRlbnQtdHlwZQcAEGFwcGxpY2F0aW9uL2pzb24NOm1lc3NhZ2UtdHlwZQcABWV2ZW50eyJtZXRyaWNzIjp7ImxhdGVuY3lNcyI6MTk1N30sInAiOiJhYmNkZWZnIiwidXNhZ2UiOnsiaW5wdXRUb2tlbnMiOjM4Niwib3V0cHV0VG9rZW5zIjo3NSwidG90YWxUb2tlbnMiOjQ2MX19Ke/W4Q=="

func TestOpenAIToAWSBedrockTranslatorExtractAmazonEventStreamEvents(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	e := eventstream.NewEncoder()
	var offsets []int
	for _, data := range []awsbedrock.ConverseStreamEvent{
		{Delta: &awsbedrock.ConverseStreamEventContentBlockDelta{Text: ptr.To("1")}},
		{Delta: &awsbedrock.ConverseStreamEventContentBlockDelta{Text: ptr.To("2")}},
		{Delta: &awsbedrock.ConverseStreamEventContentBlockDelta{Text: ptr.To("3")}},
	} {
		offsets = append(offsets, buf.Len())
		eventPayload, err := json.Marshal(data)
		require.NoError(t, err)
		err = e.Encode(buf, eventstream.Message{
			Headers: eventstream.Headers{{Name: "event-type", Value: eventstream.StringValue("content")}},
			Payload: eventPayload,
		})
		require.NoError(t, err)
	}

	eventBytes := buf.Bytes()

	t.Run("all-at-once", func(t *testing.T) {
		o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
		o.bufferedBody = eventBytes
		o.extractAmazonEventStreamEvents()
		require.Len(t, o.events, 3)
		require.Empty(t, o.bufferedBody)
		for i, text := range []string{"1", "2", "3"} {
			require.Equal(t, text, *o.events[i].Delta.Text)
		}
	})

	t.Run("in-chunks", func(t *testing.T) {
		o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
		o.bufferedBody = eventBytes[0:1]
		o.extractAmazonEventStreamEvents()
		require.Empty(t, o.events)
		require.Len(t, o.bufferedBody, 1)

		o.bufferedBody = eventBytes[0 : offsets[1]+5]
		o.extractAmazonEventStreamEvents()
		require.Len(t, o.events, 1)
		require.Equal(t, eventBytes[offsets[1]:offsets[1]+5], o.bufferedBody)

		o.events = o.events[:0]
		o.bufferedBody = eventBytes[0 : offsets[2]+5]
		o.extractAmazonEventStreamEvents()
		require.Len(t, o.events, 2)
		require.Equal(t, eventBytes[offsets[2]:offsets[2]+5], o.bufferedBody)
	})

	t.Run("real events", func(t *testing.T) {
		o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
		var err error
		o.bufferedBody, err = base64.StdEncoding.DecodeString(base64RealStreamingEvents)
		require.NoError(t, err)
		o.extractAmazonEventStreamEvents()

		var texts []string
		var usage *awsbedrock.TokenUsage
		for _, event := range o.events {
			if delta := event.Delta; delta != nil && delta.Text != nil && *delta.Text != "" {
				texts = append(texts, *event.Delta.Text)
			}
			if u := event.Usage; u != nil {
				usage = u
			}
		}
		require.Equal(t,
			"To calculate the cosine of 7, we can use the \"cosine\" function that is available to us. Let's use this function to get the result.",
			strings.Join(texts, ""),
		)
		require.NotNil(t, usage)
		require.Equal(t, 461, usage.TotalTokens)
	})
}

func TestOpenAIToAWSBedrockTranslator_convertEvent(t *testing.T) {
	ptrOf := func(s string) *string { return &s }
	for _, tc := range []struct {
		name string
		in   awsbedrock.ConverseStreamEvent
		out  *openai.ChatCompletionResponseChunk
	}{
		{
			name: "usage",
			in: awsbedrock.ConverseStreamEvent{
				Usage: &awsbedrock.TokenUsage{
					InputTokens:  10,
					OutputTokens: 20,
					TotalTokens:  30,
				},
			},
			out: &openai.ChatCompletionResponseChunk{
				Object: "chat.completion.chunk",
				Usage: &openai.ChatCompletionResponseUsage{
					TotalTokens:      30,
					PromptTokens:     10,
					CompletionTokens: 20,
				},
			},
		},
		{
			name: "role",
			in: awsbedrock.ConverseStreamEvent{
				Role: ptrOf(awsbedrock.ConversationRoleAssistant),
			},
			out: &openai.ChatCompletionResponseChunk{
				Object: "chat.completion.chunk",
				Choices: []openai.ChatCompletionResponseChunkChoice{
					{
						Delta: &openai.ChatCompletionResponseChunkChoiceDelta{
							Role:    awsbedrock.ConversationRoleAssistant,
							Content: &emptyString,
						},
					},
				},
			},
		},
		{
			name: "delta",
			in: awsbedrock.ConverseStreamEvent{
				Delta: &awsbedrock.ConverseStreamEventContentBlockDelta{Text: ptr.To("response")},
			},
			out: &openai.ChatCompletionResponseChunk{
				Object: "chat.completion.chunk",
				Choices: []openai.ChatCompletionResponseChunkChoice{
					{
						Delta: &openai.ChatCompletionResponseChunkChoiceDelta{
							Content: ptrOf("response"),
						},
					},
				},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
			chunk, ok := o.convertEvent(&tc.in)
			if tc.out == nil {
				require.False(t, ok)
			} else {
				require.Equal(t, *tc.out, chunk)
			}
		})
	}
}
