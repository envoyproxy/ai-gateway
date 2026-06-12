// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tokenize

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/json"
)

func TestCompletionRequest_JSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected CompletionRequest
		wantErr  bool
	}{
		{
			name:  "basic completion request",
			input: `{"prompt": "Hello world", "model": "gpt-4"}`,
			expected: CompletionRequest{
				Prompt: "Hello world",
				Model:  "gpt-4",
			},
		},
		{
			name:  "completion request with all fields",
			input: `{"prompt": "Hello", "model": "gpt-4", "add_special_tokens": true, "return_token_strs": true}`,
			expected: CompletionRequest{
				Prompt:           "Hello",
				Model:            "gpt-4",
				AddSpecialTokens: true,
				ReturnTokenStrs:  boolPtr(true),
			},
		},
		{
			name:  "completion request with defaults",
			input: `{"prompt": "Hello"}`,
			expected: CompletionRequest{
				Prompt: "Hello",
			},
		},
		{
			name:  "completion request missing prompt",
			input: `{"model": "gpt-4"}`,
			expected: CompletionRequest{
				Model:  "gpt-4",
				Prompt: "", // Empty prompt - should be validated elsewhere
			},
			wantErr: false, // JSON unmarshaling succeeds, validation should catch this
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req CompletionRequest
			err := json.Unmarshal([]byte(tt.input), &req)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expected, req)

			// Test roundtrip marshaling
			data, err := json.Marshal(req)
			require.NoError(t, err)

			var req2 CompletionRequest
			err = json.Unmarshal(data, &req2)
			require.NoError(t, err)
			assert.Equal(t, req, req2)
		})
	}
}

func TestChatRequest_JSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected ChatRequest
		wantErr  bool
	}{
		{
			name: "basic chat request",
			input: `{
				"messages": [{"role": "user", "content": "Hello"}],
				"model": "gpt-4"
			}`,
			expected: ChatRequest{
				Model:    "gpt-4",
				Messages: []openai.ChatCompletionMessageParamUnion{},
			},
			// Note: We can't easily create the expected Messages here due to the complex union type
		},
		{
			name: "chat request with all boolean fields",
			input: `{
				"messages": [{"role": "user", "content": "Hello"}],
				"add_generation_prompt": true,
				"continue_final_message": false,
				"add_special_tokens": true,
				"return_token_strs": true
			}`,
		},
		{
			name: "chat request with template",
			input: `{
				"messages": [{"role": "user", "content": "Hello"}],
				"chat_template": "{% for message in messages %}{{ message.content }}{% endfor %}",
				"chat_template_kwargs": {"key": "value"}
			}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req ChatRequest
			err := json.Unmarshal([]byte(tt.input), &req)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			// Test that marshaling works
			data, err := json.Marshal(req)
			require.NoError(t, err)
			assert.Contains(t, string(data), "messages")
		})
	}
}

func TestChatRequest_Validate(t *testing.T) {
	tests := []struct {
		name    string
		request ChatRequest
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid request",
			request: ChatRequest{
				AddGenerationPrompt:  true,
				ContinueFinalMessage: false,
			},
			wantErr: false,
		},
		{
			name: "conflicting flags",
			request: ChatRequest{
				AddGenerationPrompt:  true,
				ContinueFinalMessage: true,
			},
			wantErr: true,
			errMsg:  "cannot set both continue_final_message and add_generation_prompt to true",
		},
		{
			name: "continue final message only",
			request: ChatRequest{
				AddGenerationPrompt:  false,
				ContinueFinalMessage: true,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.request.Validate()

			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestRequestUnion_JSON(t *testing.T) {
	tests := []struct {
		name         string
		input        string
		isChat       bool
		isCompletion bool
	}{
		{
			name:         "completion request",
			input:        `{"prompt": "Hello world"}`,
			isCompletion: true,
		},
		{
			name:   "chat request",
			input:  `{"messages": [{"role": "user", "content": "Hello"}]}`,
			isChat: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var union RequestUnion
			err := json.Unmarshal([]byte(tt.input), &union)
			require.NoError(t, err)

			// Test marshaling back
			data, err := json.Marshal(union)
			require.NoError(t, err)
			assert.NotEmpty(t, data)
		})
	}
}

func TestResponse_JSON(t *testing.T) {
	tests := []struct {
		name     string
		response Response
		wantJSON string
	}{
		{
			name: "basic response",
			response: Response{
				Count:       10,
				MaxModelLen: 4096,
				Tokens:      []int{1, 2, 3, 4, 5},
			},
			wantJSON: `{"count":10,"max_model_len":4096,"tokens":[1,2,3,4,5]}`,
		},
		{
			name: "response with token strings",
			response: Response{
				Count:       3,
				MaxModelLen: 2048,
				Tokens:      []int{1, 2, 3},
				TokenStrs:   []string{"Hello", " ", "world"},
			},
			wantJSON: `{"count":3,"max_model_len":2048,"tokens":[1,2,3],"token_strs":["Hello"," ","world"]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := json.Marshal(tt.response)
			require.NoError(t, err)
			assert.JSONEq(t, tt.wantJSON, string(data))

			// Test roundtrip
			var response2 Response
			err = json.Unmarshal(data, &response2)
			require.NoError(t, err)
			assert.Equal(t, tt.response, response2)
		})
	}
}

func TestDetokenizeRequest_JSON(t *testing.T) {
	req := DetokenizeRequest{
		Model:  stringPtr("gpt-4"), // Model is *string for DetokenizeRequest
		Tokens: []int{1, 2, 3, 4, 5},
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var req2 DetokenizeRequest
	err = json.Unmarshal(data, &req2)
	require.NoError(t, err)
	assert.Equal(t, req, req2)
}

func TestDetokenizeResponse_JSON(t *testing.T) {
	resp := DetokenizeResponse{
		Prompt: "Hello world",
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)
	assert.JSONEq(t, `{"prompt":"Hello world"}`, string(data))

	var resp2 DetokenizeResponse
	err = json.Unmarshal(data, &resp2)
	require.NoError(t, err)
	assert.Equal(t, resp, resp2)
}

func TestTokenizerInfoResponse_CustomJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{
			name:  "basic response",
			input: `{"tokenizer_class": "GPT2TokenizerFast"}`,
		},
		{
			name:  "response with extra fields",
			input: `{"tokenizer_class": "GPT2TokenizerFast", "vocab_size": 50257, "model_max_length": 1024}`,
		},
		{
			name:    "invalid JSON",
			input:   `{"tokenizer_class": "GPT2TokenizerFast", "invalid": }`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var resp TokenizerInfoResponse
			err := json.Unmarshal([]byte(tt.input), &resp)

			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)

			switch tt.name {
			case "basic response":
				assert.Equal(t, "GPT2TokenizerFast", resp.TokenizerClass)
				assert.Empty(t, resp.ExtraConfig)
			case "response with extra fields":
				// Check that the tokenizer class is correct
				assert.Equal(t, "GPT2TokenizerFast", resp.TokenizerClass)
				// Check that extra fields were captured
				assert.Contains(t, resp.ExtraConfig, "vocab_size")
				assert.Contains(t, resp.ExtraConfig, "model_max_length")
				assert.Equal(t, float64(50257), resp.ExtraConfig["vocab_size"]) // JSON numbers become float64
				assert.Equal(t, float64(1024), resp.ExtraConfig["model_max_length"])
			}

			// Test marshaling works (don't test exact roundtrip equality due to map ordering)
			data, err := json.Marshal(resp)
			require.NoError(t, err)
			assert.Contains(t, string(data), "tokenizer_class")

			// Test that marshaled data can be unmarshaled again
			var resp2 TokenizerInfoResponse
			err = json.Unmarshal(data, &resp2)
			require.NoError(t, err)
			assert.Equal(t, resp.TokenizerClass, resp2.TokenizerClass)
			assert.Len(t, resp2.ExtraConfig, len(resp.ExtraConfig))
		})
	}
}

func TestTokenizerInfoResponse_MarshalJSON(t *testing.T) {
	resp := TokenizerInfoResponse{
		TokenizerClass: "GPT2TokenizerFast",
		ExtraConfig: map[string]interface{}{
			"vocab_size": 50257,
			"special_tokens": map[string]string{
				"unk_token": "<|endoftext|>",
			},
		},
	}

	data, err := json.Marshal(resp)
	require.NoError(t, err)

	// Should contain all fields
	assert.Contains(t, string(data), "tokenizer_class")
	assert.Contains(t, string(data), "vocab_size")
	assert.Contains(t, string(data), "special_tokens")
	assert.Contains(t, string(data), "GPT2TokenizerFast")

	// Test that an empty extra config works too
	resp2 := TokenizerInfoResponse{
		TokenizerClass: "SomeOtherTokenizer",
		ExtraConfig:    map[string]interface{}{},
	}

	data2, err := json.Marshal(resp2)
	require.NoError(t, err)
	assert.Contains(t, string(data2), "SomeOtherTokenizer")
	assert.JSONEq(t, `{"tokenizer_class":"SomeOtherTokenizer"}`, string(data2))
}

// Test edge cases and error conditions
func TestTokenizeRequestValidation(t *testing.T) {
	t.Run("empty prompt should be caught by validation", func(_ *testing.T) {
		// This highlights the need for validation in CompletionRequest
		req := CompletionRequest{
			Prompt: "",
			Model:  "gpt-4",
		}

		// Currently no validation exists - this is an area for improvement
		// TODO: Add validation for empty prompt
		_ = req
	})

	t.Run("empty messages should be caught by validation", func(t *testing.T) {
		// This highlights the need for validation in ChatRequest
		req := ChatRequest{
			Messages: []openai.ChatCompletionMessageParamUnion{},
			Model:    "gpt-4",
		}

		// Currently only validates conflicting flags
		err := req.Validate()
		assert.NoError(t, err) // No validation for empty messages yet

		// TODO: Add validation for empty messages
	})
}

func TestRequestUnion_HelperMethods(t *testing.T) {
	// This test highlights the need for helper methods
	t.Run("need helper methods to check union type", func(t *testing.T) {
		// Completion request
		union1 := RequestUnion{
			CompletionRequest: &CompletionRequest{
				Prompt: "Hello",
			},
		}

		// Chat request
		union2 := RequestUnion{
			ChatRequest: &ChatRequest{
				Messages: []openai.ChatCompletionMessageParamUnion{},
			},
		}

		// TODO: Add helper methods like:
		// union1.IsCompletion() bool
		// union1.IsChatCompletion() bool
		// union1.GetCompletion() *CompletionRequest
		// union1.GetChat() *ChatRequest

		// Current way to check (verbose)
		assert.NotNil(t, union1.CompletionRequest)
		assert.Nil(t, union1.ChatRequest)

		assert.Nil(t, union2.CompletionRequest)
		assert.NotNil(t, union2.ChatRequest)
	})
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func boolPtr(b bool) *bool {
	return &b
}
