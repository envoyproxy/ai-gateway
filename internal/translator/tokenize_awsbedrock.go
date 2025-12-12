// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strconv"
	"strings"

	"github.com/envoyproxy/ai-gateway/internal/apischema/awsbedrock"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/apischema/tokenize"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// NewTokenizeToAWSBedrockTranslator implements [Factory] for tokenize to AWS Bedrock translation.
func NewTokenizeToAWSBedrockTranslator(modelNameOverride internalapi.ModelNameOverride) TokenizeTranslator {
	return &ToAWSBedrockTranslatorV1Tokenize{
		modelNameOverride: modelNameOverride,
	}
}

// ToAWSBedrockTranslatorV1Tokenize translates tokenize API requests to AWS Bedrock format.
// Converts OpenAI-compatible tokenize requests to AWS Bedrock Converse API format for token counting.
// Uses the Converse API with minimal token generation to extract input token usage statistics.
type ToAWSBedrockTranslatorV1Tokenize struct {
	modelNameOverride internalapi.ModelNameOverride
	requestModel      internalapi.RequestModel
}

// tokenizeToBedrockCountTokens converts an OpenAI tokenize chat request to AWS Bedrock CountTokens format.
// AWS Bedrock has a dedicated CountTokens endpoint that counts input tokens without generating any output.
func (o *ToAWSBedrockTranslatorV1Tokenize) tokenizeToBedrockCountTokens(tokenizeChatReq *tokenize.TokenizeChatRequest) (*awsbedrock.CountTokensInput, error) {
	var bedrockReq awsbedrock.CountTokensInput

	// Convert Chat Completion messages to Bedrock format
	bedrockReq.Messages = make([]*awsbedrock.Message, 0, len(tokenizeChatReq.Messages))
	for i := range tokenizeChatReq.Messages {
		msg := &tokenizeChatReq.Messages[i]
		role := msg.ExtractMessgaeRole()
		switch {
		case msg.OfUser != nil:
			userMessage := msg.OfUser
			bedrockMessage, err := o.openAIMessageToBedrockMessageRoleUser(userMessage, role)
			if err != nil {
				return nil, err
			}
			bedrockReq.Messages = append(bedrockReq.Messages, bedrockMessage)
		case msg.OfAssistant != nil:
			assistantMessage := msg.OfAssistant
			bedrockMessage, err := o.openAIMessageToBedrockMessageRoleAssistant(assistantMessage, role)
			if err != nil {
				return nil, err
			}
			bedrockReq.Messages = append(bedrockReq.Messages, bedrockMessage)
		case msg.OfSystem != nil:
			if bedrockReq.System == nil {
				bedrockReq.System = make([]*awsbedrock.SystemContentBlock, 0)
			}
			systemMessage := msg.OfSystem
			err := o.openAIMessageToBedrockMessageRoleSystem(systemMessage, &bedrockReq.System)
			if err != nil {
				return nil, err
			}
		case msg.OfTool != nil:
			toolMessage := msg.OfTool
			bedrockMessage, err := o.openAIMessageToBedrockMessageRoleTool(toolMessage, awsbedrock.ConversationRoleUser)
			if err != nil {
				return nil, err
			}
			bedrockReq.Messages = append(bedrockReq.Messages, bedrockMessage)
		default:
			return nil, fmt.Errorf("unexpected role: %s", role)
		}
	}

	// Convert ToolConfiguration if tools are present
	if len(tokenizeChatReq.Tools) > 0 {
		err := o.openAIToolsToBedrockToolConfiguration(tokenizeChatReq, &bedrockReq)
		if err != nil {
			return nil, fmt.Errorf("failed to convert tools: %w", err)
		}
	}

	return &bedrockReq, nil
}

// Helper methods adapted from the existing AWS Bedrock chat completion translator
func (o *ToAWSBedrockTranslatorV1Tokenize) openAIMessageToBedrockMessageRoleUser(
	openAiMessage *openai.ChatCompletionUserMessageParam, role string,
) (*awsbedrock.Message, error) {
	if v, ok := openAiMessage.Content.Value.(string); ok {
		return &awsbedrock.Message{
			Role: role,
			Content: []*awsbedrock.ContentBlock{
				{Text: &v},
			},
		}, nil
	} else if contents, ok := openAiMessage.Content.Value.([]openai.ChatCompletionContentPartUserUnionParam); ok {
		chatMessage := &awsbedrock.Message{Role: role}
		chatMessage.Content = make([]*awsbedrock.ContentBlock, 0, len(contents))
		for i := range contents {
			contentPart := &contents[i]
			if contentPart.OfText != nil {
				textContentPart := contentPart.OfText
				chatMessage.Content = append(chatMessage.Content, &awsbedrock.ContentBlock{
					Text: &textContentPart.Text,
				})
			}
			// Note: For tokenization, we skip image content as it adds complexity
			// and most tokenization use cases are text-only
		}
		return chatMessage, nil
	}
	return nil, fmt.Errorf("unexpected content type")
}

func (o *ToAWSBedrockTranslatorV1Tokenize) openAIMessageToBedrockMessageRoleAssistant(
	openAiMessage *openai.ChatCompletionAssistantMessageParam, role string,
) (*awsbedrock.Message, error) {
	bedrockMessage := &awsbedrock.Message{Role: role}
	contentBlocks := make([]*awsbedrock.ContentBlock, 0)

	var contentParts []openai.ChatCompletionAssistantMessageParamContent
	if v, ok := openAiMessage.Content.Value.(string); ok && len(v) > 0 {
		contentParts = append(contentParts, openai.ChatCompletionAssistantMessageParamContent{
			Type: openai.ChatCompletionAssistantMessageParamContentTypeText,
			Text: &v,
		})
	} else if singleContent, ok := openAiMessage.Content.Value.(openai.ChatCompletionAssistantMessageParamContent); ok {
		contentParts = append(contentParts, singleContent)
	} else if sliceContent, ok := openAiMessage.Content.Value.([]openai.ChatCompletionAssistantMessageParamContent); ok {
		contentParts = sliceContent
	}

	for _, content := range contentParts {
		if content.Type == openai.ChatCompletionAssistantMessageParamContentTypeText {
			if content.Text != nil {
				contentBlocks = append(contentBlocks, &awsbedrock.ContentBlock{Text: content.Text})
			}
		}
	}

	bedrockMessage.Content = contentBlocks
	return bedrockMessage, nil
}

func (o *ToAWSBedrockTranslatorV1Tokenize) openAIMessageToBedrockMessageRoleSystem(
	openAiMessage *openai.ChatCompletionSystemMessageParam, bedrockSystem *[]*awsbedrock.SystemContentBlock,
) error {
	if v, ok := openAiMessage.Content.Value.(string); ok {
		*bedrockSystem = append(*bedrockSystem, &awsbedrock.SystemContentBlock{
			Text: v,
		})
	} else if contents, ok := openAiMessage.Content.Value.([]openai.ChatCompletionContentPartTextParam); ok {
		for i := range contents {
			contentPart := &contents[i]
			textContentPart := contentPart.Text
			*bedrockSystem = append(*bedrockSystem, &awsbedrock.SystemContentBlock{
				Text: textContentPart,
			})
		}
	} else {
		return fmt.Errorf("unexpected content type for system message")
	}
	return nil
}

func (o *ToAWSBedrockTranslatorV1Tokenize) openAIMessageToBedrockMessageRoleTool(
	openAiMessage *openai.ChatCompletionToolMessageParam, role string,
) (*awsbedrock.Message, error) {
	content := make([]*awsbedrock.ToolResultContentBlock, 0)

	switch v := openAiMessage.Content.Value.(type) {
	case string:
		content = []*awsbedrock.ToolResultContentBlock{
			{
				Text: &v,
			},
		}
	case []openai.ChatCompletionContentPartTextParam:
		for _, part := range v {
			content = append(content, &awsbedrock.ToolResultContentBlock{
				Text: &part.Text,
			})
		}
	default:
		return nil, fmt.Errorf("unexpected content type for tool message: %T", openAiMessage.Content.Value)
	}

	return &awsbedrock.Message{
		Role: role,
		Content: []*awsbedrock.ContentBlock{
			{
				ToolResult: &awsbedrock.ToolResultBlock{
					Content:   content,
					ToolUseID: &openAiMessage.ToolCallID,
				},
			},
		},
	}, nil
}

func (o *ToAWSBedrockTranslatorV1Tokenize) openAIToolsToBedrockToolConfiguration(tokenizeChatReq *tokenize.TokenizeChatRequest,
	bedrockReq *awsbedrock.CountTokensInput,
) error {
	bedrockReq.ToolConfig = &awsbedrock.ToolConfiguration{}
	tools := make([]*awsbedrock.Tool, 0, len(tokenizeChatReq.Tools))
	for i := range tokenizeChatReq.Tools {
		toolDefinition := &tokenizeChatReq.Tools[i]
		if toolDefinition.Function != nil {
			toolName := toolDefinition.Function.Name
			var toolDesc *string
			if toolDefinition.Function.Description != "" {
				toolDesc = &toolDefinition.Function.Description
			}
			tool := &awsbedrock.Tool{
				ToolSpec: &awsbedrock.ToolSpecification{
					Name:        &toolName,
					Description: toolDesc,
					InputSchema: &awsbedrock.ToolInputSchema{
						JSON: toolDefinition.Function.Parameters,
					},
				},
			}
			tools = append(tools, tool)
		}
	}
	bedrockReq.ToolConfig.Tools = tools
	return nil
}

// bedrockCountTokensToTokenizeResponse converts an AWS Bedrock CountTokens response to OpenAI tokenize format.
// Extracts the input token count from the CountTokens response.
func (o *ToAWSBedrockTranslatorV1Tokenize) bedrockCountTokensToTokenizeResponse(bedrockResp *awsbedrock.CountTokensResponse) (*tokenize.TokenizeResponse, error) {
	tokenizeResp := &tokenize.TokenizeResponse{
		Count: bedrockResp.InputTokens,
	}

	return tokenizeResp, nil
}

// RequestBody implements [TokenizeTranslator.RequestBody] for AWS Bedrock.
// This method translates an OpenAI tokenize request to AWS Bedrock CountTokens format.
func (o *ToAWSBedrockTranslatorV1Tokenize) RequestBody(_ []byte, tokenizeReq *tokenize.TokenizeRequestUnion, _ bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	// Validate that the union has exactly one request type set
	if err = tokenizeReq.Validate(); err != nil {
		return nil, nil, fmt.Errorf("invalid tokenize request: %w", err)
	}

	// Store the request model to use as fallback for response model
	if tokenizeReq.TokenizeChatRequest != nil {
		o.requestModel = tokenizeReq.TokenizeChatRequest.Model
	} else {
		return nil, nil, fmt.Errorf("only TokenizeChatRequest is supported for AWS Bedrock models")
	}

	if o.modelNameOverride != "" {
		// Use modelName override if set.
		o.requestModel = o.modelNameOverride
	}

	// Build the correct path for AWS Bedrock CountTokens API
	// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_CountTokens.html
	pathTemplate := "/model/%s/count-tokens"
	// URL encode the model name for the path to handle ARNs with special characters
	encodedModelName := url.PathEscape(o.requestModel)
	path := fmt.Sprintf(pathTemplate, encodedModelName)

	bedrockReq, err := o.tokenizeToBedrockCountTokens(tokenizeReq.TokenizeChatRequest)
	if err != nil {
		return nil, nil, fmt.Errorf("error converting to Bedrock request: %w", err)
	}

	newBody, err = json.Marshal(bedrockReq)
	if err != nil {
		return nil, nil, fmt.Errorf("error marshaling Bedrock count tokens request: %w", err)
	}

	newHeaders = []internalapi.Header{
		{pathHeaderName, path},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// ResponseError implements [TokenizeTranslator.ResponseError] for AWS Bedrock.
// Translate AWS Bedrock exceptions to OpenAI error type.
func (o *ToAWSBedrockTranslatorV1Tokenize) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	var openaiError openai.Error
	if v, ok := respHeaders[contentTypeHeaderName]; ok && strings.Contains(v, jsonContentType) {
		var bedrockError awsbedrock.BedrockException
		if err = json.NewDecoder(body).Decode(&bedrockError); err != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal error body: %w", err)
		}
		openaiError = openai.Error{
			Type: "error",
			Error: openai.ErrorType{
				Type:    respHeaders[awsErrorTypeHeaderName],
				Message: bedrockError.Message,
				Code:    &statusCode,
			},
		}
	} else {
		var buf []byte
		buf, err = io.ReadAll(body)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read error body: %w", err)
		}
		openaiError = openai.Error{
			Type: "error",
			Error: openai.ErrorType{
				Type:    awsBedrockBackendError,
				Message: string(buf),
				Code:    &statusCode,
			},
		}
	}
	newBody, err = json.Marshal(openaiError)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal error body: %w", err)
	}
	newHeaders = []internalapi.Header{
		{contentTypeHeaderName, jsonContentType},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// ResponseHeaders implements [TokenizeTranslator.ResponseHeaders].
func (o *ToAWSBedrockTranslatorV1Tokenize) ResponseHeaders(map[string]string) (newHeaders []internalapi.Header, err error) {
	return nil, nil
}

// ResponseBody implements [TokenizeTranslator.ResponseBody] for AWS Bedrock.
// This method translates an AWS Bedrock CountTokens response to OpenAI tokenize format.
// AWS Bedrock uses static model execution without virtualization, where the requested model
// is exactly what gets executed. We extract token count from the CountTokens response.
func (o *ToAWSBedrockTranslatorV1Tokenize) ResponseBody(_ map[string]string, body io.Reader, _ bool, span tracing.TokenizeSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	bedrockResp := &awsbedrock.CountTokensResponse{}
	if err = json.NewDecoder(body).Decode(&bedrockResp); err != nil {
		return nil, nil, tokenUsage, responseModel, fmt.Errorf("failed to unmarshal body: %w", err)
	}

	responseModel = o.requestModel

	// Convert to OpenAI format.
	openAIResp, err := o.bedrockCountTokensToTokenizeResponse(bedrockResp)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("error converting Bedrock response to OpenAI format: %w", err)
	}

	// Marshal the OpenAI response.
	newBody, err = json.Marshal(openAIResp)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("error marshaling OpenAI response: %w", err)
	}

	if span != nil {
		span.RecordResponse(openAIResp)
	}
	newHeaders = []internalapi.Header{{contentLengthHeaderName, strconv.Itoa(len(newBody))}}
	return
}
