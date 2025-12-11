// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package tokenize contains Tokenize API schema definitions.
package tokenize

import (
	"encoding/json"
	"errors"

	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// Different than vLLM's definition, "model" field should be a required field for both TokenizeCompletionRequest and TokenizeChatRequest in a gateway.

// TokenizeCompletionRequest represents a request to tokenize a completion prompt.
type TokenizeCompletionRequest struct {
	// Model is the model to use for tokenization.
	Model string `json:"model"`
	// Prompt is the text prompt to tokenize.
	Prompt string `json:"prompt"`

	// AddSpecialTokens indicates if special tokens (e.g. BOS) will be added to the prompt.
	// Default is true.
	AddSpecialTokens bool `json:"add_special_tokens,omitzero"`
	// ReturnTokenStrs indicates if token strings corresponding to the token ids should be returned.
	// Default is false.
	ReturnTokenStrs *bool `json:"return_token_strs,omitempty"`
}

// TokenizeChatRequest represents a request to tokenize chat messages.
type TokenizeChatRequest struct {
	// Model is the model to use for tokenization.
	Model string `json:"model"`
	// Messages are the chat messages to tokenize.
	Messages []openai.ChatCompletionMessageParamUnion `json:"messages"`

	// AddGenerationPrompt indicates if the generation prompt will be added to the chat template.
	// This is a parameter used by chat template in tokenizer config of the model.
	// Default is true.
	AddGenerationPrompt bool `json:"add_generation_prompt,omitzero"`
	// ReturnTokenStrs indicates if token strings corresponding to the token ids should be returned.
	// Default is false.
	ReturnTokenStrs *bool `json:"return_token_strs,omitempty"`
	// ContinueFinalMessage indicates if the chat will be formatted so that the final
	// message in the chat is open-ended, without any EOS tokens. The model will continue
	// this message rather than starting a new one. This allows you to "prefill" part of
	// the model's response for it. Cannot be used at the same time as AddGenerationPrompt.
	// Default is false.
	ContinueFinalMessage bool `json:"continue_final_message,omitzero"`
	// AddSpecialTokens indicates if special tokens (e.g. BOS) will be added to the prompt
	// on top of what is added by the chat template. For most models, the chat template
	// takes care of adding the special tokens so this should be set to false.
	// Default is false.
	AddSpecialTokens bool `json:"add_special_tokens,omitzero"`
	// ChatTemplate is a Jinja template to use for this conversion.
	// As of transformers v4.44, default chat template is no longer allowed,
	// so you must provide a chat template if the tokenizer does not define one.
	ChatTemplate *string `json:"chat_template,omitempty"`
	// ChatTemplateKwargs are additional keyword args to pass to the template renderer.
	// Will be accessible by the chat template.
	ChatTemplateKwargs map[string]interface{} `json:"chat_template_kwargs,omitempty"`
	// MmProcessorKwargs are additional kwargs to pass to the HF processor.
	MmProcessorKwargs map[string]interface{} `json:"mm_processor_kwargs,omitempty"`
	// Tools is a list of tools the model may call.
	Tools []openai.Tool `json:"tools,omitempty"`

	// GCP VertexAI specific
	MediaResolution genai.MediaResolution `json:"media_resolution,omitempty"`
}

// Validate checks that the request is valid.
func (r *TokenizeChatRequest) Validate() error {
	if r.ContinueFinalMessage && r.AddGenerationPrompt {
		return errors.New("cannot set both continue_final_message and add_generation_prompt to true")
	}
	return nil
}

// TokenizeRequestUnion represents a union of tokenize request types.
// This allows the endpoint to handle both completion and chat tokenize requests.
type TokenizeRequestUnion struct {
	// OfCompletion is set when this is a completion tokenize request
	*TokenizeCompletionRequest `json:",omitzero,inline"`
	// OfChat is set when this is a chat tokenize request
	*TokenizeChatRequest `json:",omitzero,inline"`
}

// Validate checks that exactly one request type is set in the union.
func (r *TokenizeRequestUnion) Validate() error {
	if r.TokenizeCompletionRequest != nil && r.TokenizeChatRequest != nil {
		return errors.New("only one request type can be set")
	}
	if r.TokenizeCompletionRequest == nil && r.TokenizeChatRequest == nil {
		return errors.New("one request type must be set")
	}
	return nil
}

// TokenizeResponse represents the response from a tokenize request.
type TokenizeResponse struct {
	// Count is the number of tokens.
	Count int `json:"count"`
	// MaxModelLen is the maximum model length.
	MaxModelLen int `json:"max_model_len"`
	// Tokens are the token IDs.
	Tokens []int `json:"tokens"`
	// TokenStrs are the token strings, if requested.
	TokenStrs []string `json:"token_strs,omitempty"`
}

// DetokenizeRequest represents a request to detokenize tokens.
type DetokenizeRequest struct {
	// Model is the model to use for detokenization.
	Model *string `json:"model,omitempty"`
	// Tokens are the token IDs to convert back to text.
	Tokens []int `json:"tokens"`
}

// DetokenizeResponse represents the response from a detokenize request.
type DetokenizeResponse struct {
	// Prompt is the detokenized text.
	Prompt string `json:"prompt"`
}

// TokenizerInfoResponse represents the response containing tokenizer configuration
// equivalent to tokenizer_config.json
type TokenizerInfoResponse struct {
	// TokenizerClass is the class of the tokenizer.
	TokenizerClass string `json:"tokenizer_class"`
	// ExtraConfig stores additional configuration fields that may be present
	// in tokenizer_config.json (equivalent to Pydantic's ConfigDict(extra="allow")).
	ExtraConfig map[string]interface{} `json:"-"`
}

// UnmarshalJSON implements custom unmarshaling to handle additional fields
// in TokenizerInfoResponse (equivalent to Pydantic's ConfigDict(extra="allow")).
func (r *TokenizerInfoResponse) UnmarshalJSON(data []byte) error {
	// First unmarshal into a map to capture all fields
	var allFields map[string]interface{}
	if err := json.Unmarshal(data, &allFields); err != nil {
		return err
	}

	// Extract known fields
	if tokenizerClass, ok := allFields["tokenizer_class"]; ok {
		if str, ok := tokenizerClass.(string); ok {
			r.TokenizerClass = str
		}
	}

	// Store any extra fields
	r.ExtraConfig = make(map[string]interface{})
	for key, value := range allFields {
		if key != "tokenizer_class" {
			r.ExtraConfig[key] = value
		}
	}

	return nil
}

// MarshalJSON implements custom marshaling to include extra fields
func (r TokenizerInfoResponse) MarshalJSON() ([]byte, error) {
	// Create a map with all fields
	result := map[string]interface{}{
		"tokenizer_class": r.TokenizerClass,
	}

	// Add extra fields
	for key, value := range r.ExtraConfig {
		result[key] = value
	}

	return json.Marshal(result)
}
