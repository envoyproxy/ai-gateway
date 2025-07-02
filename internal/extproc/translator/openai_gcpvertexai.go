// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0

package translator

import (
	"encoding/json"
	"fmt"
	"io"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/genai"

	"github.com/envoyproxy/ai-gateway/internal/apischema/gcp"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// NewChatCompletionOpenAIToGCPVertexAITranslator implements [Factory] for OpenAI to GCP Gemini translation.
// This translator converts OpenAI ChatCompletion API requests to GCP Gemini API format.
func NewChatCompletionOpenAIToGCPVertexAITranslator() OpenAIChatCompletionTranslator {
	return &openAIToGCPVertexAITranslatorV1ChatCompletion{}
}

type openAIToGCPVertexAITranslatorV1ChatCompletion struct{}

// RequestBody implements [Translator.RequestBody] for GCP Gemini.
// This method translates an OpenAI ChatCompletion request to a GCP Gemini API request.
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) RequestBody(_ []byte, openAIReq *openai.ChatCompletionRequest, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	pathSuffix := buildGCPModelPathSuffix(GCPModelPublisherGoogle, openAIReq.Model, GCPMethodGenerateContent)

	gcpReq, err := o.openAIMessageToGeminiMessage(openAIReq)
	if err != nil {
		return nil, nil, fmt.Errorf("error converting OpenAI request to Gemini request: %w", err)
	}
	gcpReqBody, err := json.Marshal(gcpReq)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshaling Gemini request: %w", err)
	}

	headerMutation, bodyMutation = buildGCPRequestMutations(&pathSuffix, gcpReqBody)
	return headerMutation, bodyMutation, nil
}

// ResponseHeaders implements [Translator.ResponseHeaders].
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) ResponseHeaders(headers map[string]string) (
	headerMutation *extprocv3.HeaderMutation, err error,
) {
	// TODO: Implement if needed.
	_ = headers
	return nil, nil
}

// ResponseBody implements [Translator.ResponseBody] for GCP Gemini.
// This method translates a GCP Gemini API response to the OpenAI ChatCompletion format.
func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) ResponseBody(_ map[string]string, body io.Reader, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, err error,
) {
	// Read the body
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return nil, nil, LLMTokenUsage{}, fmt.Errorf("error reading response body: %w", err)
	}

	// Parse the GCP response
	var gcpResp genai.GenerateContentResponse
	if err = json.Unmarshal(bodyBytes, &gcpResp); err != nil {
		return nil, nil, LLMTokenUsage{}, fmt.Errorf("error unmarshaling GCP response: %w", err)
	}

	var openAIRespBytes []byte
	if len(gcpResp.Candidates) > 0 {
		// Convert to OpenAI format
		openAIResp, err := o.geminiResponseToOpenAIMessage(gcpResp)
		if err != nil {
			return nil, nil, LLMTokenUsage{}, fmt.Errorf("error converting GCP response to OpenAI format: %w", err)
		}

		// Marshal the OpenAI response
		openAIRespBytes, err = json.Marshal(openAIResp)
		if err != nil {
			return nil, nil, LLMTokenUsage{}, fmt.Errorf("error marshaling OpenAI response: %w", err)
		}
	}

	// Update token usage if available
	var usage LLMTokenUsage
	if gcpResp.UsageMetadata != nil {
		usage = LLMTokenUsage{
			InputTokens:  uint32(gcpResp.UsageMetadata.PromptTokenCount),     // nolint:gosec
			OutputTokens: uint32(gcpResp.UsageMetadata.CandidatesTokenCount), // nolint:gosec
			TotalTokens:  uint32(gcpResp.UsageMetadata.TotalTokenCount),      // nolint:gosec
		}
	}

	headerMutation, bodyMutation = buildGCPRequestMutations(nil, openAIRespBytes)

	return headerMutation, bodyMutation, usage, nil
}

func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) openAIMessageToGeminiMessage(openAIReq *openai.ChatCompletionRequest) (gcp.GenerateContentRequest, error) {
	// Convert OpenAI messages to Gemini Contents and SystemInstruction
	contents, systemInstruction, err := toGeminiContents(openAIReq.Messages)
	if err != nil {
		return gcp.GenerateContentRequest{}, err
	}

	// Convert generation config
	generationConfig, err := toGeminiGenerationConfig(openAIReq)
	if err != nil {
		return gcp.GenerateContentRequest{}, fmt.Errorf("error converting generation config: %w", err)
	}

	gcr := gcp.GenerateContentRequest{
		Contents:          contents,
		Tools:             nil,
		ToolConfig:        nil,
		GenerationConfig:  generationConfig,
		SystemInstruction: systemInstruction,
	}

	return gcr, nil
}

func (o *openAIToGCPVertexAITranslatorV1ChatCompletion) geminiResponseToOpenAIMessage(gcr genai.GenerateContentResponse) (openai.ChatCompletionResponse, error) {
	// Convert candidates to OpenAI choices
	choices, err := toOpenAIChoices(gcr.Candidates)
	if err != nil {
		return openai.ChatCompletionResponse{}, fmt.Errorf("error converting choices: %w", err)
	}

	// Set up the OpenAI response
	openaiResp := openai.ChatCompletionResponse{
		Choices: choices,
		Object:  "chat.completion",
		Usage:   toOpenAIUsage(gcr.UsageMetadata),
	}

	return openaiResp, nil
}
