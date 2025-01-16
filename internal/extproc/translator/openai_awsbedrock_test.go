package translator

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/awsbedrock"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/extproc/router"
)

func TestNewOpenAIToAWSBedrockTranslator(t *testing.T) {
	t.Run("unsupported path", func(t *testing.T) {
		_, err := newOpenAIToAWSBedrockTranslator("unsupported-path")
		require.Error(t, err)
	})
	t.Run("v1/chat/completions", func(t *testing.T) {
		translator, err := newOpenAIToAWSBedrockTranslator("/v1/chat/completions")
		require.NoError(t, err)
		require.NotNil(t, translator)
	})
}

func TestOpenAIToAWSBedrockTranslatorV1ChatCompletion_RequestBody(t *testing.T) {
	t.Run("invalid body", func(t *testing.T) {
		o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
		_, _, _, err := o.RequestBody(&extprocv3.HttpBody{Body: []byte("invalid")})
		require.Error(t, err)
	})
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
											URL: "data:image/jpeg;base64,dGVzdAo=",
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
										Bytes: []byte("dGVzdAo="),
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
			name: "test function calling",
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
				ToolChoice: "auto",
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
					ToolChoice: &awsbedrock.ToolChoice{
						Auto: &awsbedrock.AutoToolChoice{},
					},
					Tools: []*awsbedrock.Tool{
						{
							ToolSpec: &awsbedrock.ToolSpecification{
								Name:        ptr.To("function"),
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
			originalReq := tt.input
			hm, bm, mode, err := o.RequestBody(router.RequestBody(&originalReq))
			var expPath string
			if tt.input.Stream {
				expPath = "/model/gpt-4o/converse-stream"
				require.True(t, o.stream)
				require.NotNil(t, mode)
				require.Equal(t, extprocv3http.ProcessingMode_STREAMED, mode.ResponseBodyMode)
				require.Equal(t, extprocv3http.ProcessingMode_SEND, mode.ResponseHeaderMode)
			} else {
				expPath = "/model/gpt-4o/converse"
				require.False(t, o.stream)
				require.Nil(t, mode)
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
			hm, bm, usedToken, err := o.ResponseBody(bytes.NewBuffer([]byte{buf[i]}), i == len(buf)-1)
			require.NoError(t, err)
			require.Nil(t, hm)
			require.NotNil(t, bm)
			require.NotNil(t, bm.Mutation)
			newBody := bm.Mutation.(*extprocv3.BodyMutation_Body).Body
			if len(newBody) > 0 {
				results = append(results, string(newBody))
			}
			if usedToken > 0 {
				require.Equal(t, uint32(77), usedToken)
			}
		}

		result := strings.Join(results, "")

		require.Equal(t, `data: {"choices":[{"delta":{"content":"","role":"assistant"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":"Don"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":"'t worry, I'm here to help. It"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" seems like you're testing my ability to respond appropriately"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":". If you'd like to continue the test,"}}],"object":"chat.completion.chunk"}

data: {"choices":[{"delta":{"content":" I'm ready."}}],"object":"chat.completion.chunk"}

data: {"object":"chat.completion.chunk","usage":{"completion_tokens":36,"prompt_tokens":41,"total_tokens":77}}

data: [DONE]
`, result)
	})
}

func TestOpenAIToAWSBedrockTranslatorV1ChatCompletion_ResponseBody(t *testing.T) {
	t.Run("invalid body", func(t *testing.T) {
		o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
		_, _, _, err := o.ResponseBody(bytes.NewBuffer([]byte("invalid")), false)
		require.Error(t, err)
	})
	tests := []struct {
		name   string
		input  awsbedrock.ConverseOutput
		output openai.ChatCompletionResponse
	}{
		{
			name: "basic_testing",
			input: awsbedrock.ConverseOutput{
				Usage: &awsbedrock.TokenUsage{
					InputTokens:  10,
					OutputTokens: 20,
					TotalTokens:  30,
				},
				Output: &awsbedrock.ConverseOutput_{
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
							Role:    "assistant",
						},
					},
					{
						Index: 1,
						Message: openai.ChatCompletionResponseChoiceMessage{
							Content: ptr.To("from"),
							Role:    "assistant",
						},
					},
					{
						Index: 2,
						Message: openai.ChatCompletionResponseChoiceMessage{
							Content: ptr.To("assistant"),
							Role:    "assistant",
						},
					},
				},
			},
		},
		{
			name: "test stop reason",
			input: awsbedrock.ConverseOutput{
				Usage: &awsbedrock.TokenUsage{
					InputTokens:  10,
					OutputTokens: 20,
					TotalTokens:  30,
				},
				StopReason: ptr.To("stop_sequence"),
				Output: &awsbedrock.ConverseOutput_{
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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body, err := json.Marshal(tt.input)
			require.NoError(t, err)
			o := &openAIToAWSBedrockTranslatorV1ChatCompletion{}
			hm, bm, usedToken, err := o.ResponseBody(bytes.NewBuffer(body), false)
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
			require.Equal(t, uint32(30), usedToken)
			if !cmp.Equal(openAIResp, tt.output) {
				t.Errorf("ConvertOpenAIToBedrock(), diff(got, expected) = %s\n", cmp.Diff(openAIResp, tt.output))
			}
		})
	}
}

const base64RealStreamingEvents = "AAAAnwAAAFKzEV9wCzpldmVudC10eXBlBwAMbWVzc2FnZVN0YXJ0DTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsicCI6ImFiY2RlZmdoaWprbG1ub3BxcnN0dXZ3eHl6QUJDREVGR0giLCJyb2xlIjoiYXNzaXN0YW50In0i9wVBAAAAxQAAAFex2HyVCzpldmVudC10eXBlBwARY29udGVudEJsb2NrRGVsdGENOmNvbnRlbnQtdHlwZQcAEGFwcGxpY2F0aW9uL2pzb24NOm1lc3NhZ2UtdHlwZQcABWV2ZW50eyJjb250ZW50QmxvY2tJbmRleCI6MCwiZGVsdGEiOnsidGV4dCI6IkRvbiJ9LCJwIjoiYWJjZGVmZ2hpamtsbW5vcHFyc3R1dnd4eXpBQkNERUZHSElKS0xNTk8ifb/whawAAADAAAAAV3k48+ULOmV2ZW50LXR5cGUHABFjb250ZW50QmxvY2tEZWx0YQ06Y29udGVudC10eXBlBwAQYXBwbGljYXRpb24vanNvbg06bWVzc2FnZS10eXBlBwAFZXZlbnR7ImNvbnRlbnRCbG9ja0luZGV4IjowLCJkZWx0YSI6eyJ0ZXh0IjoiJ3Qgd29ycnksIEknbSBoZXJlIHRvIGhlbHAuIEl0In0sInAiOiJhYmNkZWZnaGkifenahv0AAADgAAAAV7j53OELOmV2ZW50LXR5cGUHABFjb250ZW50QmxvY2tEZWx0YQ06Y29udGVudC10eXBlBwAQYXBwbGljYXRpb24vanNvbg06bWVzc2FnZS10eXBlBwAFZXZlbnR7ImNvbnRlbnRCbG9ja0luZGV4IjowLCJkZWx0YSI6eyJ0ZXh0IjoiIHNlZW1zIGxpa2UgeW91J3JlIHRlc3RpbmcgbXkgYWJpbGl0eSB0byByZXNwb25kIGFwcHJvcHJpYXRlbHkifSwicCI6ImFiY2RlZmdoaSJ9dNZCqAAAAM8AAABX+2hkNAs6ZXZlbnQtdHlwZQcAEWNvbnRlbnRCbG9ja0RlbHRhDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudEJsb2NrSW5kZXgiOjAsImRlbHRhIjp7InRleHQiOiIuIElmIHlvdSdkIGxpa2UgdG8gY29udGludWUgdGhlIHRlc3QsIn0sInAiOiJhYmNkZWZnaGlqa2xtbm9wcSJ9xQJqAgAAALUAAABXSAqcWgs6ZXZlbnQtdHlwZQcAEWNvbnRlbnRCbG9ja0RlbHRhDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudEJsb2NrSW5kZXgiOjAsImRlbHRhIjp7InRleHQiOiIgSSdtIHJlYWR5LiJ9LCJwIjoiYWJjZGVmZ2hpamtsbW5vcHEifTOb7esAAAC5AAAAVvr9Qc0LOmV2ZW50LXR5cGUHABBjb250ZW50QmxvY2tTdG9wDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsiY29udGVudEJsb2NrSW5kZXgiOjAsInAiOiJhYmNkZWZnaGlqa2xtbm9wcXJzdHV2d3h5ekFCQ0RFRkdISUpLTE1OT1BRUlNUVVZXWFlaMCJ9iABE1AAAAI0AAABRMDjKKAs6ZXZlbnQtdHlwZQcAC21lc3NhZ2VTdG9wDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsicCI6ImFiY2RlZmdoaWprbCIsInN0b3BSZWFzb24iOiJlbmRfdHVybiJ9LttU3QAAAPoAAABO9sL7Ags6ZXZlbnQtdHlwZQcACG1ldGFkYXRhDTpjb250ZW50LXR5cGUHABBhcHBsaWNhdGlvbi9qc29uDTptZXNzYWdlLXR5cGUHAAVldmVudHsibWV0cmljcyI6eyJsYXRlbmN5TXMiOjQ1Mn0sInAiOiJhYmNkZWZnaGlqa2xtbm9wcXJzdHV2d3h5ekFCQ0RFRkdISUpLTE1OT1BRUlNUVVZXWFlaMDEyMzQ1IiwidXNhZ2UiOnsiaW5wdXRUb2tlbnMiOjQxLCJvdXRwdXRUb2tlbnMiOjM2LCJ0b3RhbFRva2VucyI6Nzd9fX96gYI="

func TestOpenAIToAWSBedrockTranslatorExtractAmazonEventStreamEvents(t *testing.T) {
	buf := bytes.NewBuffer(nil)
	e := eventstream.NewEncoder()
	var offsets []int
	for _, data := range []awsbedrock.ConverseStreamEvent{
		{Delta: &awsbedrock.ConverseStreamEventContentBlockDelta{Text: "1"}},
		{Delta: &awsbedrock.ConverseStreamEventContentBlockDelta{Text: "2"}},
		{Delta: &awsbedrock.ConverseStreamEventContentBlockDelta{Text: "3"}},
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
			require.Equal(t, text, o.events[i].Delta.Text)
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
			t.Log(event.String())
			if delta := event.Delta; delta != nil && delta.Text != "" {
				texts = append(texts, event.Delta.Text)
			}
			if u := event.Usage; u != nil {
				usage = u
			}
		}
		require.Equal(t,
			"Don't worry, I'm here to help. It seems like you're testing my ability to respond appropriately. If you'd like to continue the test, I'm ready.",
			strings.Join(texts, ""),
		)
		require.NotNil(t, usage)
		require.Equal(t, 77, usage.TotalTokens)
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
				Role: ptrOf("assistant"),
			},
			out: &openai.ChatCompletionResponseChunk{
				Object: "chat.completion.chunk",
				Choices: []openai.ChatCompletionResponseChunkChoice{
					{
						Delta: &openai.ChatCompletionResponseChunkChoiceDelta{
							Role:    ptrOf("assistant"),
							Content: &emptyString,
						},
					},
				},
			},
		},
		{
			name: "delta",
			in: awsbedrock.ConverseStreamEvent{
				Delta: &awsbedrock.ConverseStreamEventContentBlockDelta{Text: "response"},
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
