// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/openai/openai-go/v2"
	"github.com/openai/openai-go/v2/packages/param"
	"github.com/stretchr/testify/require"
	"google.golang.org/genai"
	"k8s.io/utils/ptr"
)

func TestChatCompletionRequest_VendorFieldsExtraction(t *testing.T) {
	tests := []struct {
		name           string
		jsonData       []byte
		expected       *ChatCompletionRequest
		expectedErrMsg string
	}{
		{
			name: "Request with GCP Vertex AI vendor fields",
			jsonData: []byte(`{
				"model": "gemini-1.5-pro",
				"messages": [
					{
						"role": "user",
						"content": "Hello, world!"
					}
				],
				"generationConfig": {
					"thinkingConfig": {
						"includeThoughts": true,
						"thinkingBudget": 1000
					}
				},
                "safetySettings": [{
                    "category": "HARM_CATEGORY_HARASSMENT",
                    "threshold": "BLOCK_ONLY_HIGH"
                }]
			}`),
			expected: &ChatCompletionRequest{
				Model: "gemini-1.5-pro",
				Messages: []ChatCompletionMessageParamUnion{
					{
						OfUser: &ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "Hello, world!"},
						},
					},
				},
				GCPVertexAIVendorFields: &GCPVertexAIVendorFields{
					GenerationConfig: &GCPVertexAIGenerationConfig{
						ThinkingConfig: &genai.ThinkingConfig{
							IncludeThoughts: true,
							ThinkingBudget:  ptr.To(int32(1000)),
						},
					},
					SafetySettings: []*genai.SafetySetting{
						{
							Category:  genai.HarmCategoryHarassment,
							Threshold: genai.HarmBlockThresholdBlockOnlyHigh,
						},
					},
				},
			},
		},
		{
			name: "Request with multiple vendor fields",
			jsonData: []byte(`{
				"model": "claude-3",
				"messages": [
					{
						"role": "user",
						"content": "Multiple vendors test"
					}
				],
				"generationConfig": {
					"thinkingConfig": {
						"includeThoughts": true,
						"thinkingBudget": 1000
					}
				},
				"thinking": {
					"type": "enabled",
					"budget_tokens": 1000
				}
			}`),
			expected: &ChatCompletionRequest{
				Model: "claude-3",
				Messages: []ChatCompletionMessageParamUnion{
					{
						OfUser: &ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "Multiple vendors test"},
						},
					},
				},
				AnthropicVendorFields: &AnthropicVendorFields{
					Thinking: &anthropic.ThinkingConfigParamUnion{
						OfEnabled: &anthropic.ThinkingConfigEnabledParam{
							BudgetTokens: 1000,
							Type:         "enabled",
						},
					},
				},
				GCPVertexAIVendorFields: &GCPVertexAIVendorFields{
					GenerationConfig: &GCPVertexAIGenerationConfig{
						ThinkingConfig: &genai.ThinkingConfig{
							IncludeThoughts: true,
							ThinkingBudget:  ptr.To(int32(1000)),
						},
					},
				},
			},
		},
		{
			name: "Request without vendor fields",
			jsonData: []byte(`{
				"model": "gpt-4",
				"messages": [
					{
						"role": "user",
						"content": "Standard request"
					}
				]
			}`),
			expected: &ChatCompletionRequest{
				Model: "gpt-4",
				Messages: []ChatCompletionMessageParamUnion{
					{
						OfUser: &ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "Standard request"},
						},
					},
				},
			},
		},
		{
			name: "Request with empty vendor fields",
			jsonData: []byte(`{
				"model": "gemini-pro",
				"messages": [
					{
						"role": "user",
						"content": "Empty vendor fields"
					}
				]
			}`),
			expected: &ChatCompletionRequest{
				Model: "gemini-pro",
				Messages: []ChatCompletionMessageParamUnion{
					{
						OfUser: &ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "Empty vendor fields"},
						},
					},
				},
			},
		},
		{
			name: "Request with null vendor fields",
			jsonData: []byte(`{
				"model": "gpt-3.5",
				"messages": [
					{
						"role": "user",
						"content": "Null vendor fields"
					}
				]
			}`),
			expected: &ChatCompletionRequest{
				Model: "gpt-3.5",
				Messages: []ChatCompletionMessageParamUnion{
					{
						OfUser: &ChatCompletionUserMessageParam{
							Role:    ChatMessageRoleUser,
							Content: StringOrUserRoleContentUnion{Value: "Null vendor fields"},
						},
					},
				},
			},
		},
		{
			name: "Malformed vendor fields JSON",
			jsonData: []byte(`{
				"model": "gemini-1.5-pro",
				"messages": [
					{
						"role": "user",
						"content": "Test malformed vendor fields"
					}
				],
				"generationConfig": {
					"thinkingConfig":
				}
			}`),
			expectedErrMsg: "invalid character",
		},
		{
			name: "Invalid vendor field type",
			jsonData: []byte(`{
				"model": "gemini-1.5-pro",
				"messages": [
					{
						"role": "user",
						"content": "Test invalid vendor field type"
					}
				],
				"generationConfig": "invalid_string_type"
			}`),
			expectedErrMsg: "cannot unmarshal string into Go struct field",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var actual ChatCompletionRequest
			err := json.Unmarshal(tt.jsonData, &actual)

			if tt.expectedErrMsg != "" {
				require.Error(t, err)
				require.Contains(t, err.Error(), tt.expectedErrMsg)
				return
			}

			require.NoError(t, err)
			if diff := cmp.Diff(tt.expected, &actual, cmpopts.IgnoreUnexported(anthropic.ThinkingConfigEnabledParam{}, anthropic.ThinkingConfigParamUnion{},
				openai.ChatCompletionNewParamsStopUnion{}, param.Opt[string]{})); diff != "" {
				t.Errorf("ChatCompletionRequest mismatch (-expected +actual):\n%s", diff)
			}
		})
	}
}
