// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicParam "github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	openAIconstant "github.com/openai/openai-go/shared/constant"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/awsbedrock"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

const (
	anthropicVersionKey   = "anthropic_version"
	tempNotSupportedError = "temperature %.2f is not supported by Anthropic (must be between 0.0 and 1.0)"
)

// anthropicInputSchemaKeysToSkip defines the keys from an OpenAI function parameter map
// that are handled explicitly and should not go into the ExtraFields map.
var anthropicInputSchemaKeysToSkip = map[string]struct{}{
	"required":   {},
	"type":       {},
	"properties": {},
}

func anthropicToOpenAIFinishReason(stopReason anthropic.StopReason) (openai.ChatCompletionChoicesFinishReason, error) {
	switch stopReason {
	// The most common stop reason. Indicates Claude finished its response naturally.
	// or Claude encountered one of your custom stop sequences.
	// TODO: A better way to return pause_turn
	// TODO: "pause_turn" Used with server tools like web search when Claude needs to pause a long-running operation.
	case anthropic.StopReasonEndTurn, anthropic.StopReasonStopSequence, anthropic.StopReasonPauseTurn:
		return openai.ChatCompletionChoicesFinishReasonStop, nil
	case anthropic.StopReasonMaxTokens: // Claude stopped because it reached the max_tokens limit specified in your request.
		// TODO: do we want to return an error? see: https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/implement-tool-use#handling-the-max-tokens-stop-reason
		return openai.ChatCompletionChoicesFinishReasonLength, nil
	case anthropic.StopReasonToolUse:
		return openai.ChatCompletionChoicesFinishReasonToolCalls, nil
	case anthropic.StopReasonRefusal:
		return openai.ChatCompletionChoicesFinishReasonContentFilter, nil
	default:
		return "", fmt.Errorf("received invalid stop reason %v", stopReason)
	}
}

// validateTemperatureForAnthropic checks if the temperature is within Anthropic's supported range (0.0 to 1.0).
// Returns an error if the value is greater than 1.0.
func validateTemperatureForAnthropic(temp *float64) error {
	if temp != nil && (*temp < 0.0 || *temp > 1.0) {
		return fmt.Errorf("%w: "+tempNotSupportedError, internalapi.ErrInvalidRequestBody, *temp)
	}
	return nil
}

// translateAnthropicToolChoice converts the OpenAI tool_choice parameter to the Anthropic format.
func translateAnthropicToolChoice(openAIToolChoice *openai.ChatCompletionToolChoiceUnion, disableParallelToolUse anthropicParam.Opt[bool]) (anthropic.ToolChoiceUnionParam, error) {
	var toolChoice anthropic.ToolChoiceUnionParam

	if openAIToolChoice == nil {
		return toolChoice, nil
	}

	switch choice := openAIToolChoice.Value.(type) {
	case string:
		switch choice {
		case string(openAIconstant.ValueOf[openAIconstant.Auto]()):
			toolChoice = anthropic.ToolChoiceUnionParam{OfAuto: &anthropic.ToolChoiceAutoParam{}}
			toolChoice.OfAuto.DisableParallelToolUse = disableParallelToolUse
		case "required", "any":
			toolChoice = anthropic.ToolChoiceUnionParam{OfAny: &anthropic.ToolChoiceAnyParam{}}
			toolChoice.OfAny.DisableParallelToolUse = disableParallelToolUse
		case "none":
			toolChoice = anthropic.ToolChoiceUnionParam{OfNone: &anthropic.ToolChoiceNoneParam{}}
		case string(openAIconstant.ValueOf[openAIconstant.Function]()):
			// This is how anthropic forces tool use.
			// TODO: should we check if strict true in openAI request, and if so, use this?
			toolChoice = anthropic.ToolChoiceUnionParam{OfTool: &anthropic.ToolChoiceToolParam{Name: choice}}
			toolChoice.OfTool.DisableParallelToolUse = disableParallelToolUse
		default:
			return anthropic.ToolChoiceUnionParam{}, fmt.Errorf("unsupported tool_choice value: %s", choice)
		}
	case openai.ChatCompletionNamedToolChoice:
		if choice.Type == openai.ToolTypeFunction && choice.Function.Name != "" {
			toolChoice = anthropic.ToolChoiceUnionParam{
				OfTool: &anthropic.ToolChoiceToolParam{
					Type:                   constant.Tool("tool"),
					Name:                   choice.Function.Name,
					DisableParallelToolUse: disableParallelToolUse,
				},
			}
		}
	default:
		return anthropic.ToolChoiceUnionParam{}, fmt.Errorf("unsupported tool_choice type: %T", openAIToolChoice)
	}
	return toolChoice, nil
}

func isAnthropicSupportedImageMediaType(mediaType string) bool {
	switch anthropic.Base64ImageSourceMediaType(mediaType) {
	case anthropic.Base64ImageSourceMediaTypeImageJPEG,
		anthropic.Base64ImageSourceMediaTypeImagePNG,
		anthropic.Base64ImageSourceMediaTypeImageGIF,
		anthropic.Base64ImageSourceMediaTypeImageWebP:
		return true
	default:
		return false
	}
}

// translateOpenAItoAnthropicTools translates OpenAI tool and tool_choice parameters
// into the Anthropic format and returns translated tool & tool choice.
func translateOpenAItoAnthropicTools(openAITools []openai.Tool, openAIToolChoice *openai.ChatCompletionToolChoiceUnion, parallelToolCalls *bool) (tools []anthropic.ToolUnionParam, toolChoice anthropic.ToolChoiceUnionParam, err error) {
	if len(openAITools) > 0 {
		anthropicTools := make([]anthropic.ToolUnionParam, 0, len(openAITools))
		for _, openAITool := range openAITools {
			if openAITool.Type != openai.ToolTypeFunction || openAITool.Function == nil {
				// Anthropic only supports 'function' tools, so we skip others.
				continue
			}
			toolParam := anthropic.ToolParam{
				Name:        openAITool.Function.Name,
				Description: anthropic.String(openAITool.Function.Description),
			}

			if isCacheEnabled(openAITool.Function.AnthropicContentFields) {
				toolParam.CacheControl = anthropic.NewCacheControlEphemeralParam()
			}

			// The parameters for the function are expected to be a JSON Schema object.
			// We can pass them through as-is.
			if openAITool.Function.Parameters != nil {
				paramsMap, ok := openAITool.Function.Parameters.(map[string]any)
				if !ok {
					err = fmt.Errorf("failed to cast tool parameters to map[string]interface{}")
					return
				}

				inputSchema := anthropic.ToolInputSchemaParam{}

				var typeVal string
				if typeVal, ok = paramsMap["type"].(string); ok {
					inputSchema.Type = constant.Object(typeVal)
				}

				var propsVal map[string]any
				if propsVal, ok = paramsMap["properties"].(map[string]any); ok {
					inputSchema.Properties = propsVal
				}

				var requiredVal []any
				if requiredVal, ok = paramsMap["required"].([]any); ok {
					requiredSlice := make([]string, len(requiredVal))
					for i, v := range requiredVal {
						if s, ok := v.(string); ok {
							requiredSlice[i] = s
						}
					}
					inputSchema.Required = requiredSlice
				}

				// ExtraFieldsMap to construct
				ExtraFieldsMap := make(map[string]any)

				// Iterate over the original map from openai
				for key, value := range paramsMap {
					// Check if the current key should be skipped
					if _, found := anthropicInputSchemaKeysToSkip[key]; found {
						continue
					}

					// If not skipped, add the key-value pair to extra field map
					ExtraFieldsMap[key] = value
				}
				inputSchema.ExtraFields = ExtraFieldsMap

				toolParam.InputSchema = inputSchema
			}

			anthropicTools = append(anthropicTools, anthropic.ToolUnionParam{OfTool: &toolParam})
			if len(anthropicTools) > 0 {
				tools = anthropicTools
			}
		}

		// 2. Handle the tool_choice parameter.
		// disable parallel tool use default value is false
		// see: https://docs.anthropic.com/en/docs/agents-and-tools/tool-use/implement-tool-use#parallel-tool-use
		disableParallelToolUse := anthropic.Bool(false)
		if parallelToolCalls != nil {
			// OpenAI variable checks to allow parallel tool calls.
			// Anthropic variable checks to disable, so need to use the inverse.
			disableParallelToolUse = anthropic.Bool(!*parallelToolCalls)
		}

		toolChoice, err = translateAnthropicToolChoice(openAIToolChoice, disableParallelToolUse)
		if err != nil {
			return
		}

	}
	return
}

// convertImageContentToAnthropic translates an OpenAI image URL into the corresponding Anthropic content block.
// It handles data URIs for various image types and PDFs, as well as remote URLs.
func convertImageContentToAnthropic(imageURL string, fields *openai.AnthropicContentFields) (anthropic.ContentBlockParamUnion, error) {
	var cacheControlParam anthropic.CacheControlEphemeralParam
	if isCacheEnabled(fields) {
		cacheControlParam = fields.CacheControl
	}

	switch {
	case strings.HasPrefix(imageURL, "data:"):
		contentType, data, err := parseDataURI(imageURL)
		if err != nil {
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf("failed to parse image URL: %w", err)
		}
		base64Data := base64.StdEncoding.EncodeToString(data)
		if contentType == string(constant.ValueOf[constant.ApplicationPDF]()) {
			pdfSource := anthropic.Base64PDFSourceParam{Data: base64Data}
			docBlock := anthropic.NewDocumentBlock(pdfSource)
			docBlock.OfDocument.CacheControl = cacheControlParam
			return docBlock, nil
		}
		if isAnthropicSupportedImageMediaType(contentType) {
			imgBlock := anthropic.NewImageBlockBase64(contentType, base64Data)
			imgBlock.OfImage.CacheControl = cacheControlParam
			return imgBlock, nil
		}
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("invalid media_type for image '%s'", contentType)
	case strings.HasSuffix(strings.ToLower(imageURL), ".pdf"):
		docBlock := anthropic.NewDocumentBlock(anthropic.URLPDFSourceParam{URL: imageURL})
		docBlock.OfDocument.CacheControl = cacheControlParam
		return docBlock, nil
	default:
		imgBlock := anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: imageURL})
		imgBlock.OfImage.CacheControl = cacheControlParam
		return imgBlock, nil
	}
}

func isCacheEnabled(fields *openai.AnthropicContentFields) bool {
	return fields != nil && fields.CacheControl.Type == constant.ValueOf[constant.Ephemeral]()
}

// convertContentPartsToAnthropic iterates over a slice of OpenAI content parts
// and converts each into an Anthropic content block.
func convertContentPartsToAnthropic(parts []openai.ChatCompletionContentPartUserUnionParam) ([]anthropic.ContentBlockParamUnion, error) {
	resultContent := make([]anthropic.ContentBlockParamUnion, 0, len(parts))
	for _, contentPart := range parts {
		switch {
		case contentPart.OfText != nil:
			textBlock := anthropic.NewTextBlock(contentPart.OfText.Text)
			if isCacheEnabled(contentPart.OfText.AnthropicContentFields) {
				textBlock.OfText.CacheControl = contentPart.OfText.CacheControl
			}
			resultContent = append(resultContent, textBlock)

		case contentPart.OfImageURL != nil:
			block, err := convertImageContentToAnthropic(contentPart.OfImageURL.ImageURL.URL, contentPart.OfImageURL.AnthropicContentFields)
			if err != nil {
				return nil, err
			}
			resultContent = append(resultContent, block)

		case contentPart.OfInputAudio != nil:
			return nil, fmt.Errorf("input audio content not supported yet")
		case contentPart.OfFile != nil:
			return nil, fmt.Errorf("file content not supported yet")
		}
	}
	return resultContent, nil
}

// Helper: Convert OpenAI message content to Anthropic content.
func openAIToAnthropicContent(content any) ([]anthropic.ContentBlockParamUnion, error) {
	switch v := content.(type) {
	case nil:
		return nil, nil
	case string:
		if v == "" {
			return nil, nil
		}
		return []anthropic.ContentBlockParamUnion{
			anthropic.NewTextBlock(v),
		}, nil
	case []openai.ChatCompletionContentPartUserUnionParam:
		return convertContentPartsToAnthropic(v)
	case openai.ContentUnion:
		switch val := v.Value.(type) {
		case string:
			if val == "" {
				return nil, nil
			}
			return []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock(val),
			}, nil
		case []openai.ChatCompletionContentPartTextParam:
			var contentBlocks []anthropic.ContentBlockParamUnion
			for _, part := range val {
				textBlock := anthropic.NewTextBlock(part.Text)
				// In an array of text parts, each can have its own cache setting.
				if isCacheEnabled(part.AnthropicContentFields) {
					textBlock.OfText.CacheControl = part.CacheControl
				}
				contentBlocks = append(contentBlocks, textBlock)
			}
			return contentBlocks, nil
		default:
			return nil, fmt.Errorf("unsupported ContentUnion value type: %T", val)
		}
	}
	return nil, fmt.Errorf("unsupported OpenAI content type: %T", content)
}

// extractSystemPromptFromDeveloperMsg flattens content and checks for cache flags.
// It returns the combined string and a boolean indicating if any part was cacheable.
func extractSystemPromptFromDeveloperMsg(msg openai.ChatCompletionDeveloperMessageParam) (msgValue string, cacheParam *anthropic.CacheControlEphemeralParam) {
	switch v := msg.Content.Value.(type) {
	case nil:
		return
	case string:
		msgValue = v
		return
	case []openai.ChatCompletionContentPartTextParam:
		// Concatenate all text parts and check for caching.
		var sb strings.Builder
		for _, part := range v {
			sb.WriteString(part.Text)
			if isCacheEnabled(part.AnthropicContentFields) {
				cacheParam = &part.CacheControl
			}
		}
		msgValue = sb.String()
		return
	default:
		return
	}
}

func anthropicRoleToOpenAIRole(role anthropic.MessageParamRole) (string, error) {
	switch role {
	case anthropic.MessageParamRoleAssistant:
		return openai.ChatMessageRoleAssistant, nil
	case anthropic.MessageParamRoleUser:
		return openai.ChatMessageRoleUser, nil
	default:
		return "", fmt.Errorf("invalid anthropic role %v", role)
	}
}

// processAssistantContent processes a single assistant content block and adds it to the content blocks.
func processAssistantContent(contentBlocks []anthropic.ContentBlockParamUnion, content openai.ChatCompletionAssistantMessageParamContent) ([]anthropic.ContentBlockParamUnion, error) {
	switch content.Type {
	case openai.ChatCompletionAssistantMessageParamContentTypeRefusal:
		if content.Refusal != nil {
			contentBlocks = append(contentBlocks, anthropic.NewTextBlock(*content.Refusal))
		}
	case openai.ChatCompletionAssistantMessageParamContentTypeText:
		if content.Text != nil {
			textBlock := anthropic.NewTextBlock(*content.Text)
			if isCacheEnabled(content.AnthropicContentFields) {
				textBlock.OfText.CacheControl = content.CacheControl
			}
			contentBlocks = append(contentBlocks, textBlock)
		}
	case openai.ChatCompletionAssistantMessageParamContentTypeThinking:
		// Thinking content requires both text and signature
		if content.Text != nil && content.Signature != nil {
			contentBlocks = append(contentBlocks, anthropic.NewThinkingBlock(*content.Signature, *content.Text))
		}
	case openai.ChatCompletionAssistantMessageParamContentTypeRedactedThinking:
		if content.RedactedContent != nil {
			switch v := content.RedactedContent.Value.(type) {
			case string:
				contentBlocks = append(contentBlocks, anthropic.NewRedactedThinkingBlock(v))
			default:
				return nil, fmt.Errorf("unsupported RedactedContent type: %T, expected string", v)
			}
		}
	default:
		return nil, fmt.Errorf("content type not supported: %v", content.Type)
	}
	return contentBlocks, nil
}

// openAIMessageToAnthropicMessageRoleAssistant converts an OpenAI assistant message to Anthropic content blocks.
// The tool_use content is appended to the Anthropic message content list if tool_calls are present.
func openAIMessageToAnthropicMessageRoleAssistant(openAiMessage *openai.ChatCompletionAssistantMessageParam) (anthropicMsg anthropic.MessageParam, err error) {
	contentBlocks := make([]anthropic.ContentBlockParamUnion, 0)
	if v, ok := openAiMessage.Content.Value.(string); ok && len(v) > 0 {
		contentBlocks = append(contentBlocks, anthropic.NewTextBlock(v))
	} else if content, ok := openAiMessage.Content.Value.(openai.ChatCompletionAssistantMessageParamContent); ok {
		contentBlocks, err = processAssistantContent(contentBlocks, content)
		if err != nil {
			return
		}
	} else if contents, ok := openAiMessage.Content.Value.([]openai.ChatCompletionAssistantMessageParamContent); ok {
		for _, content := range contents {
			contentBlocks, err = processAssistantContent(contentBlocks, content)
			if err != nil {
				return
			}
		}
	}

	// Handle tool_calls (if any).
	for i := range openAiMessage.ToolCalls {
		toolCall := &openAiMessage.ToolCalls[i]
		var input map[string]any
		if err = json.Unmarshal([]byte(toolCall.Function.Arguments), &input); err != nil {
			err = fmt.Errorf("failed to unmarshal tool call arguments: %w", err)
			return
		}
		toolUse := anthropic.ToolUseBlockParam{
			ID:    *toolCall.ID,
			Type:  "tool_use",
			Name:  toolCall.Function.Name,
			Input: input,
		}

		if isCacheEnabled(toolCall.AnthropicContentFields) {
			toolUse.CacheControl = toolCall.CacheControl
		}

		contentBlocks = append(contentBlocks, anthropic.ContentBlockParamUnion{OfToolUse: &toolUse})
	}

	return anthropic.MessageParam{
		Role:    anthropic.MessageParamRoleAssistant,
		Content: contentBlocks,
	}, nil
}

// openAIToAnthropicMessages converts OpenAI messages to Anthropic message params type, handling all roles and system/developer logic.
func openAIToAnthropicMessages(openAIMsgs []openai.ChatCompletionMessageParamUnion) (anthropicMessages []anthropic.MessageParam, systemBlocks []anthropic.TextBlockParam, err error) {
	for i := 0; i < len(openAIMsgs); {
		msg := &openAIMsgs[i]
		switch {
		case msg.OfSystem != nil:
			devParam := systemMsgToDeveloperMsg(*msg.OfSystem)
			systemText, cacheControl := extractSystemPromptFromDeveloperMsg(devParam)
			systemBlock := anthropic.TextBlockParam{Text: systemText}
			if cacheControl != nil {
				systemBlock.CacheControl = *cacheControl
			}
			systemBlocks = append(systemBlocks, systemBlock)
			i++
		case msg.OfDeveloper != nil:
			systemText, cacheControl := extractSystemPromptFromDeveloperMsg(*msg.OfDeveloper)
			systemBlock := anthropic.TextBlockParam{Text: systemText}
			if cacheControl != nil {
				systemBlock.CacheControl = *cacheControl
			}
			systemBlocks = append(systemBlocks, systemBlock)
			i++
		case msg.OfUser != nil:
			message := *msg.OfUser
			var content []anthropic.ContentBlockParamUnion
			content, err = openAIToAnthropicContent(message.Content.Value)
			if err != nil {
				return
			}
			anthropicMsg := anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: content,
			}
			anthropicMessages = append(anthropicMessages, anthropicMsg)
			i++
		case msg.OfAssistant != nil:
			assistantMessage := msg.OfAssistant
			var messages anthropic.MessageParam
			messages, err = openAIMessageToAnthropicMessageRoleAssistant(assistantMessage)
			if err != nil {
				return
			}
			anthropicMessages = append(anthropicMessages, messages)
			i++
		case msg.OfTool != nil:
			// Aggregate all consecutive tool messages into a single user message
			// to support parallel tool use.
			var toolResultBlocks []anthropic.ContentBlockParamUnion
			for i < len(openAIMsgs) && openAIMsgs[i].ExtractMessgaeRole() == openai.ChatMessageRoleTool {
				currentMsg := &openAIMsgs[i]
				toolMsg := currentMsg.OfTool

				var contentBlocks []anthropic.ContentBlockParamUnion
				contentBlocks, err = openAIToAnthropicContent(toolMsg.Content)
				if err != nil {
					return
				}

				var toolContent []anthropic.ToolResultBlockParamContentUnion
				var cacheControl *anthropic.CacheControlEphemeralParam

				for _, c := range contentBlocks {
					var trb anthropic.ToolResultBlockParamContentUnion
					// Check if the translated part has caching enabled.
					switch {
					case c.OfText != nil:
						trb.OfText = c.OfText
						cacheControl = &c.OfText.CacheControl
					case c.OfImage != nil:
						trb.OfImage = c.OfImage
						cacheControl = &c.OfImage.CacheControl
					case c.OfDocument != nil:
						trb.OfDocument = c.OfDocument
						cacheControl = &c.OfDocument.CacheControl
					}
					toolContent = append(toolContent, trb)
				}

				isError := false
				if contentStr, ok := toolMsg.Content.Value.(string); ok {
					var contentMap map[string]any
					if json.Unmarshal([]byte(contentStr), &contentMap) == nil {
						if _, ok = contentMap["error"]; ok {
							isError = true
						}
					}
				}

				toolResultBlock := anthropic.ToolResultBlockParam{
					ToolUseID: toolMsg.ToolCallID,
					Type:      "tool_result",
					Content:   toolContent,
					IsError:   anthropic.Bool(isError),
				}

				if cacheControl != nil {
					toolResultBlock.CacheControl = *cacheControl
				}

				toolResultBlockUnion := anthropic.ContentBlockParamUnion{OfToolResult: &toolResultBlock}
				toolResultBlocks = append(toolResultBlocks, toolResultBlockUnion)
				i++
			}
			// Append all aggregated tool results.
			anthropicMsg := anthropic.MessageParam{
				Role:    anthropic.MessageParamRoleUser,
				Content: toolResultBlocks,
			}
			anthropicMessages = append(anthropicMessages, anthropicMsg)
		default:
			err = fmt.Errorf("unsupported OpenAI role type: %s", msg.ExtractMessgaeRole())
			return
		}
	}
	return
}

// NewThinkingConfigParamUnion converts a ThinkingUnion into a ThinkingConfigParamUnion.
func getThinkingConfigParamUnion(tu *openai.ThinkingUnion) *anthropic.ThinkingConfigParamUnion {
	if tu == nil {
		return nil
	}

	result := &anthropic.ThinkingConfigParamUnion{}

	if tu.OfEnabled != nil {
		result.OfEnabled = &anthropic.ThinkingConfigEnabledParam{
			BudgetTokens: tu.OfEnabled.BudgetTokens,
			Type:         constant.Enabled(tu.OfEnabled.Type),
		}
	} else if tu.OfDisabled != nil {
		result.OfDisabled = &anthropic.ThinkingConfigDisabledParam{
			Type: constant.Disabled(tu.OfDisabled.Type),
		}
	}

	return result
}

// outputConfigAvailable checks if the model supports structured outputs (OutputConfig).
// Structured outputs are available on Claude Opus 4.6, Claude Sonnet 4.5, Claude Opus 4.5, and Claude Haiku 4.5.
// See: https://platform.claude.com/docs/en/build-with-claude/structured-outputs
func outputConfigAvailable(model internalapi.RequestModel) bool {
	modelLower := strings.ToLower(model)
	return strings.Contains(modelLower, "4-5") ||
		strings.Contains(modelLower, "4-6")
}

// buildAnthropicParams is a helper function that translates an OpenAI request
// into the parameter struct required by the Anthropic SDK.
// The apiSchema parameter indicates the backend API schema (e.g., "AWSAnthropic", "GCPAnthropic").
func buildAnthropicParams(openAIReq *openai.ChatCompletionRequest, apiSchema string) (params *anthropic.MessageNewParams, err error) {
	// 1. Handle simple parameters and defaults.
	maxTokens := cmp.Or(openAIReq.MaxCompletionTokens, openAIReq.MaxTokens)
	if maxTokens == nil {
		err = fmt.Errorf("%w: max_tokens or max_completion_tokens is required", internalapi.ErrInvalidRequestBody)
		return
	}

	// Translate openAI contents to anthropic params.
	// 2. Translate messages and system prompts.
	messages, systemBlocks, err := openAIToAnthropicMessages(openAIReq.Messages)
	if err != nil {
		return
	}

	// 3. Translate tools and tool choice.
	tools, toolChoice, err := translateOpenAItoAnthropicTools(openAIReq.Tools, openAIReq.ToolChoice, openAIReq.ParallelToolCalls)
	if err != nil {
		return
	}

	// 4. Construct the final struct in one place.
	params = &anthropic.MessageNewParams{
		Messages:   messages,
		MaxTokens:  *maxTokens,
		System:     systemBlocks,
		Tools:      tools,
		ToolChoice: toolChoice,
	}

	// 5. Handle structured outputs (ResponseFormat -> OutputConfig).
	// See: https://platform.claude.com/docs/en/build-with-claude/structured-outputs
	// Currently, GCP Vertex AI does not support output_config.
	isGCPBackend := strings.HasPrefix(apiSchema, "GCP")
	if !isGCPBackend && openAIReq.ResponseFormat != nil && openAIReq.ResponseFormat.OfJSONSchema != nil && outputConfigAvailable(openAIReq.Model) {
		// Convert OpenAI JSON schema to Anthropic OutputConfig format
		var schemaMap map[string]any
		if err = json.Unmarshal(openAIReq.ResponseFormat.OfJSONSchema.JSONSchema.Schema, &schemaMap); err != nil {
			return nil, fmt.Errorf("failed to parse JSON schema: %w", err)
		}
		params.OutputConfig = anthropic.OutputConfigParam{
			Format: anthropic.JSONOutputFormatParam{
				Type:   constant.JSONSchema("json_schema"),
				Schema: schemaMap,
			},
		}
	}

	if openAIReq.Temperature != nil {
		if err = validateTemperatureForAnthropic(openAIReq.Temperature); err != nil {
			return nil, err
		}
		params.Temperature = anthropic.Float(*openAIReq.Temperature)
	}
	if openAIReq.TopP != nil {
		params.TopP = anthropic.Float(*openAIReq.TopP)
	}
	if openAIReq.Stop.OfString.Valid() {
		params.StopSequences = []string{openAIReq.Stop.OfString.String()}
	} else if openAIReq.Stop.OfStringArray != nil {
		params.StopSequences = openAIReq.Stop.OfStringArray
	}

	// 5. Handle Vendor specific fields.
	// Since GCPAnthropic follows the Anthropic API, we also check for Anthropic vendor fields.
	if openAIReq.Thinking != nil {
		params.Thinking = *getThinkingConfigParamUnion(openAIReq.Thinking)
	}

	return params, nil
}

// anthropicToolUseToOpenAICalls converts Anthropic tool_use content blocks to OpenAI tool calls.
func anthropicToolUseToOpenAICalls(block *anthropic.ContentBlockUnion) ([]openai.ChatCompletionMessageToolCallParam, error) {
	var toolCalls []openai.ChatCompletionMessageToolCallParam
	if block.Type != string(constant.ValueOf[constant.ToolUse]()) {
		return toolCalls, nil
	}
	argsBytes, err := json.Marshal(block.Input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tool_use input: %w", err)
	}
	toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
		ID:   &block.ID,
		Type: openai.ChatCompletionMessageToolCallTypeFunction,
		Function: openai.ChatCompletionMessageToolCallFunctionParam{
			Name:      block.Name,
			Arguments: string(argsBytes),
		},
	})

	return toolCalls, nil
}

// following are streaming part

var (
	sseEventPrefix = []byte("event: ")
	emptyStrPtr    = ptr.To("")
)

// streamingToolCall holds the state for a single tool call that is being streamed.
type streamingToolCall struct {
	id        string
	name      string
	inputJSON string
}

// anthropicStreamParser manages the stateful translation of an Anthropic SSE stream
// to an OpenAI-compatible SSE stream.
type anthropicStreamParser struct {
	buffer          bytes.Buffer
	activeMessageID string
	activeToolCalls map[int64]*streamingToolCall
	toolIndex       int64
	tokenUsage      metrics.TokenUsage
	stopReason      anthropic.StopReason
	requestModel    internalapi.RequestModel
	sentFirstChunk  bool
	created         openai.JSONUNIXTime
}

// newAnthropicStreamParser creates a new parser for a streaming request.
func newAnthropicStreamParser(requestModel string) *anthropicStreamParser {
	toolIdx := int64(-1)
	return &anthropicStreamParser{
		requestModel:    requestModel,
		activeToolCalls: make(map[int64]*streamingToolCall),
		toolIndex:       toolIdx,
	}
}

func (p *anthropicStreamParser) writeChunk(eventBlock []byte, buf *[]byte) error {
	chunk, err := p.parseAndHandleEvent(eventBlock)
	if err != nil {
		return err
	}
	if chunk != nil {
		err := serializeOpenAIChatCompletionChunk(chunk, buf)
		if err != nil {
			return err
		}
	}
	return nil
}

// Process reads from the Anthropic SSE stream, translates events to OpenAI chunks,
// and returns the mutations for Envoy.
func (p *anthropicStreamParser) Process(body io.Reader, endOfStream bool, span tracingapi.ChatCompletionSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	newBody = make([]byte, 0)
	_ = span // TODO: add support for streaming chunks in tracing.
	responseModel = p.requestModel
	if _, err = p.buffer.ReadFrom(body); err != nil {
		err = fmt.Errorf("failed to read from stream body: %w", err)
		return
	}

	for {
		eventBlock, remaining, found := bytes.Cut(p.buffer.Bytes(), []byte("\n\n"))
		if !found {
			break
		}

		if err = p.writeChunk(eventBlock, &newBody); err != nil {
			return
		}

		p.buffer.Reset()
		p.buffer.Write(remaining)
	}

	if endOfStream && p.buffer.Len() > 0 {
		finalEventBlock := p.buffer.Bytes()
		p.buffer.Reset()

		if err = p.writeChunk(finalEventBlock, &newBody); err != nil {
			return
		}
	}

	if endOfStream {
		inputTokens, _ := p.tokenUsage.InputTokens()
		outputTokens, _ := p.tokenUsage.OutputTokens()
		p.tokenUsage.SetTotalTokens(inputTokens + outputTokens)
		totalTokens, _ := p.tokenUsage.TotalTokens()
		cachedTokens, _ := p.tokenUsage.CachedInputTokens()
		cacheCreationTokens, _ := p.tokenUsage.CacheCreationInputTokens()
		finalChunk := openai.ChatCompletionResponseChunk{
			ID:      p.activeMessageID,
			Created: p.created,
			Object:  "chat.completion.chunk",
			Choices: []openai.ChatCompletionResponseChunkChoice{},
			Usage: &openai.Usage{
				PromptTokens:     int(inputTokens),
				CompletionTokens: int(outputTokens),
				TotalTokens:      int(totalTokens),
				PromptTokensDetails: &openai.PromptTokensDetails{
					CachedTokens:        int(cachedTokens),
					CacheCreationTokens: int(cacheCreationTokens),
				},
			},
			Model: p.requestModel,
		}

		// Add active tool calls to the final chunk.
		var toolCalls []openai.ChatCompletionChunkChoiceDeltaToolCall
		for toolIndex, tool := range p.activeToolCalls {
			toolCalls = append(toolCalls, openai.ChatCompletionChunkChoiceDeltaToolCall{
				ID:   &tool.id,
				Type: openai.ChatCompletionMessageToolCallTypeFunction,
				Function: openai.ChatCompletionMessageToolCallFunctionParam{
					Name:      tool.name,
					Arguments: tool.inputJSON,
				},
				Index: toolIndex,
			})
		}

		if len(toolCalls) > 0 {
			delta := openai.ChatCompletionResponseChunkChoiceDelta{
				ToolCalls: toolCalls,
			}
			finalChunk.Choices = append(finalChunk.Choices, openai.ChatCompletionResponseChunkChoice{
				Delta: &delta,
			})
		}

		if finalChunk.Usage.PromptTokens > 0 || finalChunk.Usage.CompletionTokens > 0 || len(finalChunk.Choices) > 0 {
			err := serializeOpenAIChatCompletionChunk(&finalChunk, &newBody)
			if err != nil {
				return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("failed to marshal final stream chunk: %w", err)
			}
		}
		// Add the final [DONE] message to indicate the end of the stream.
		newBody = append(newBody, sseDataPrefix...)
		newBody = append(newBody, sseDoneMessage...)
		newBody = append(newBody, '\n', '\n')
	}
	tokenUsage = p.tokenUsage
	return
}

func (p *anthropicStreamParser) parseAndHandleEvent(eventBlock []byte) (*openai.ChatCompletionResponseChunk, error) {
	var eventType []byte
	var eventData []byte

	lines := bytes.SplitSeq(eventBlock, []byte("\n"))
	for line := range lines {
		if after, ok := bytes.CutPrefix(line, sseEventPrefix); ok {
			eventType = bytes.TrimSpace(after)
		} else if after, ok := bytes.CutPrefix(line, sseDataPrefix); ok {
			// This handles JSON data that might be split across multiple 'data:' lines
			// by concatenating them (Anthropic's format).
			data := bytes.TrimSpace(after)
			eventData = append(eventData, data...)
		}
	}

	if len(eventType) > 0 && len(eventData) > 0 {
		return p.handleAnthropicStreamEvent(eventType, eventData)
	}

	return nil, nil
}

func (p *anthropicStreamParser) handleAnthropicStreamEvent(eventType []byte, data []byte) (*openai.ChatCompletionResponseChunk, error) {
	switch string(eventType) {
	case string(constant.ValueOf[constant.MessageStart]()):
		var event anthropic.MessageStartEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal message_start: %w", err)
		}
		p.activeMessageID = event.Message.ID
		p.created = openai.JSONUNIXTime(time.Now())
		u := event.Message.Usage
		usage := metrics.ExtractTokenUsageFromExplicitCaching(
			u.InputTokens,
			u.OutputTokens,
			&u.CacheReadInputTokens,
			&u.CacheCreationInputTokens,
		)
		// For message_start, we store the initial usage but don't add to the accumulated
		// The message_delta event will contain the final totals
		if input, ok := usage.InputTokens(); ok {
			p.tokenUsage.SetInputTokens(input)
		}
		if cached, ok := usage.CachedInputTokens(); ok {
			p.tokenUsage.SetCachedInputTokens(cached)
		}
		if cacheCreation, ok := usage.CacheCreationInputTokens(); ok {
			p.tokenUsage.SetCacheCreationInputTokens(cacheCreation)
		}

		// reset the toolIndex for each message
		p.toolIndex = -1
		return nil, nil

	case string(constant.ValueOf[constant.ContentBlockStart]()):
		var event anthropic.ContentBlockStartEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("failed to unmarshal content_block_start: %w", err)
		}
		if event.ContentBlock.Type == string(constant.ValueOf[constant.ToolUse]()) || event.ContentBlock.Type == string(constant.ValueOf[constant.ServerToolUse]()) {
			p.toolIndex++
			var argsJSON string
			// Check if the input field is provided directly in the start event.
			if event.ContentBlock.Input != nil {
				switch input := event.ContentBlock.Input.(type) {
				case map[string]any:
					// for case where "input":{}, skip adding it to arguments.
					if len(input) > 0 {
						argsBytes, err := json.Marshal(input)
						if err != nil {
							return nil, fmt.Errorf("failed to marshal tool use input: %w", err)
						}
						argsJSON = string(argsBytes)
					}
				default:
					// although golang sdk defines type of Input to be any,
					// python sdk requires the type of Input to be Dict[str, object]:
					// https://github.com/anthropics/anthropic-sdk-python/blob/main/src/anthropic/types/tool_use_block.py#L14.
					return nil, fmt.Errorf("unexpected tool use input type: %T", input)
				}
			}

			// Store the complete input JSON in our state.
			p.activeToolCalls[p.toolIndex] = &streamingToolCall{
				id:        event.ContentBlock.ID,
				name:      event.ContentBlock.Name,
				inputJSON: argsJSON,
			}

			delta := openai.ChatCompletionResponseChunkChoiceDelta{
				ToolCalls: []openai.ChatCompletionChunkChoiceDeltaToolCall{
					{
						Index: p.toolIndex,
						ID:    &event.ContentBlock.ID,
						Type:  openai.ChatCompletionMessageToolCallTypeFunction,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name: event.ContentBlock.Name,
							// Include the arguments if they are available.
							Arguments: argsJSON,
						},
					},
				},
			}
			return p.constructOpenAIChatCompletionChunk(delta, ""), nil
		}
		if event.ContentBlock.Type == string(constant.ValueOf[constant.Thinking]()) {
			delta := openai.ChatCompletionResponseChunkChoiceDelta{Content: emptyStrPtr}
			return p.constructOpenAIChatCompletionChunk(delta, ""), nil
		}

		if event.ContentBlock.Type == string(constant.ValueOf[constant.RedactedThinking]()) {
			// This is a latency-hiding event, ignore it.
			return nil, nil
		}

		return nil, nil

	case string(constant.ValueOf[constant.MessageDelta]()):
		var event anthropic.MessageDeltaEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal message_delta: %w", err)
		}
		u := event.Usage
		usage := metrics.ExtractTokenUsageFromExplicitCaching(
			u.InputTokens,
			u.OutputTokens,
			&u.CacheReadInputTokens,
			&u.CacheCreationInputTokens,
		)
		// For message_delta, accumulate the incremental output tokens
		if output, ok := usage.OutputTokens(); ok {
			p.tokenUsage.AddOutputTokens(output)
		}
		// Update input tokens to include any cache tokens from delta
		if cached, ok := usage.CachedInputTokens(); ok {
			p.tokenUsage.AddInputTokens(cached)
			// Accumulate any additional cache tokens from delta
			p.tokenUsage.AddCachedInputTokens(cached)
		}
		if cacheCreation, ok := usage.CacheCreationInputTokens(); ok {
			p.tokenUsage.AddInputTokens(cacheCreation)
			// Accumulate cache creation tokens
			p.tokenUsage.AddCacheCreationInputTokens(cacheCreation)
		}
		if event.Delta.StopReason != "" {
			p.stopReason = event.Delta.StopReason
		}
		return nil, nil

	case string(constant.ValueOf[constant.ContentBlockDelta]()):
		var event anthropic.ContentBlockDeltaEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal content_block_delta: %w", err)
		}
		switch event.Delta.Type {
		case string(constant.ValueOf[constant.TextDelta]()), string(constant.ValueOf[constant.ThinkingDelta]()):
			// Treat thinking_delta just like a text_delta.
			delta := openai.ChatCompletionResponseChunkChoiceDelta{Content: &event.Delta.Text}
			return p.constructOpenAIChatCompletionChunk(delta, ""), nil
		case string(constant.ValueOf[constant.InputJSONDelta]()):
			tool, ok := p.activeToolCalls[p.toolIndex]
			if !ok {
				return nil, fmt.Errorf("received input_json_delta for unknown tool at index %d", p.toolIndex)
			}
			delta := openai.ChatCompletionResponseChunkChoiceDelta{
				ToolCalls: []openai.ChatCompletionChunkChoiceDeltaToolCall{
					{
						Index: p.toolIndex,
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Arguments: event.Delta.PartialJSON,
						},
					},
				},
			}
			tool.inputJSON += event.Delta.PartialJSON
			return p.constructOpenAIChatCompletionChunk(delta, ""), nil
		}

	case string(constant.ValueOf[constant.ContentBlockStop]()):
		// This event is for state cleanup, no chunk is sent.
		var event anthropic.ContentBlockStopEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal content_block_stop: %w", err)
		}
		delete(p.activeToolCalls, p.toolIndex)
		return nil, nil

	case string(constant.ValueOf[constant.MessageStop]()):
		var event anthropic.MessageStopEvent
		if err := json.Unmarshal(data, &event); err != nil {
			return nil, fmt.Errorf("unmarshal message_stop: %w", err)
		}

		if p.stopReason == "" {
			p.stopReason = anthropic.StopReasonEndTurn
		}

		finishReason, err := anthropicToOpenAIFinishReason(p.stopReason)
		if err != nil {
			return nil, err
		}
		return p.constructOpenAIChatCompletionChunk(openai.ChatCompletionResponseChunkChoiceDelta{}, finishReason), nil

	case string(constant.ValueOf[constant.Error]()):
		var errEvent anthropic.ErrorResponse
		if err := json.Unmarshal(data, &errEvent); err != nil {
			return nil, fmt.Errorf("unparsable error event: %s", string(data))
		}
		return nil, fmt.Errorf("anthropic stream error: %s - %s", errEvent.Error.Type, errEvent.Error.Message)

	case "ping":
		// Per documentation, ping events can be ignored.
		return nil, nil
	}
	return nil, nil
}

// constructOpenAIChatCompletionChunk builds the stream chunk.
func (p *anthropicStreamParser) constructOpenAIChatCompletionChunk(delta openai.ChatCompletionResponseChunkChoiceDelta, finishReason openai.ChatCompletionChoicesFinishReason) *openai.ChatCompletionResponseChunk {
	// Add the 'assistant' role to the very first chunk of the response.
	if !p.sentFirstChunk {
		// Only add the role if the delta actually contains content or a tool call.
		if delta.Content != nil || len(delta.ToolCalls) > 0 {
			delta.Role = openai.ChatMessageRoleAssistant
			p.sentFirstChunk = true
		}
	}

	return &openai.ChatCompletionResponseChunk{
		ID:      p.activeMessageID,
		Created: p.created,
		Object:  "chat.completion.chunk",
		Choices: []openai.ChatCompletionResponseChunkChoice{
			{
				Delta:        &delta,
				FinishReason: finishReason,
			},
		},
		Model: p.requestModel,
	}
}

// messageToChatCompletion is to translate from anthropic API's response Message into OpenAI API's response ChatCompletion
func messageToChatCompletion(anthropicResp *anthropic.Message, responseModel internalapi.RequestModel) (openAIResp *openai.ChatCompletionResponse, tokenUsage metrics.TokenUsage, err error) {
	openAIResp = &openai.ChatCompletionResponse{
		ID:      anthropicResp.ID,
		Model:   responseModel,
		Object:  string(openAIconstant.ValueOf[openAIconstant.ChatCompletion]()),
		Choices: make([]openai.ChatCompletionResponseChoice, 0),
		Created: openai.JSONUNIXTime(time.Now()),
	}
	usage := anthropicResp.Usage
	tokenUsage = metrics.ExtractTokenUsageFromExplicitCaching(
		usage.InputTokens,
		usage.OutputTokens,
		&usage.CacheReadInputTokens,
		&usage.CacheCreationInputTokens,
	)
	inputTokens, _ := tokenUsage.InputTokens()
	outputTokens, _ := tokenUsage.OutputTokens()
	totalTokens, _ := tokenUsage.TotalTokens()
	cachedTokens, _ := tokenUsage.CachedInputTokens()
	cacheCreationTokens, _ := tokenUsage.CacheCreationInputTokens()
	openAIResp.Usage = openai.Usage{
		CompletionTokens: int(outputTokens),
		PromptTokens:     int(inputTokens),
		TotalTokens:      int(totalTokens),
		PromptTokensDetails: &openai.PromptTokensDetails{
			CachedTokens:        int(cachedTokens),
			CacheCreationTokens: int(cacheCreationTokens),
		},
	}

	finishReason, err := anthropicToOpenAIFinishReason(anthropicResp.StopReason)
	if err != nil {
		return nil, metrics.TokenUsage{}, err
	}

	role, err := anthropicRoleToOpenAIRole(anthropic.MessageParamRole(anthropicResp.Role))
	if err != nil {
		return nil, metrics.TokenUsage{}, err
	}

	choice := openai.ChatCompletionResponseChoice{
		Index:        0,
		Message:      openai.ChatCompletionResponseChoiceMessage{Role: role},
		FinishReason: finishReason,
	}

	for i := range anthropicResp.Content { // NOTE: Content structure is massive, do not range over values.
		output := &anthropicResp.Content[i]
		switch output.Type {
		case string(constant.ValueOf[constant.ToolUse]()):
			if output.ID != "" {
				toolCalls, toolErr := anthropicToolUseToOpenAICalls(output)
				if toolErr != nil {
					return nil, metrics.TokenUsage{}, fmt.Errorf("failed to convert anthropic tool use to openai tool call: %w", toolErr)
				}
				choice.Message.ToolCalls = append(choice.Message.ToolCalls, toolCalls...)
			}
		case string(constant.ValueOf[constant.Text]()):
			if output.Text != "" {
				if choice.Message.Content == nil {
					choice.Message.Content = &output.Text
				}
			}
		case string(constant.ValueOf[constant.Thinking]()):
			if output.Thinking != "" {
				choice.Message.ReasoningContent = &openai.ReasoningContentUnion{
					Value: &openai.ReasoningContent{
						ReasoningContent: &awsbedrock.ReasoningContentBlock{
							ReasoningText: &awsbedrock.ReasoningTextBlock{
								Text:      output.Thinking,
								Signature: output.Signature,
							},
						},
					},
				}
			}
		case string(constant.ValueOf[constant.RedactedThinking]()):
			if output.Data != "" {
				choice.Message.ReasoningContent = &openai.ReasoningContentUnion{
					Value: &openai.ReasoningContent{
						ReasoningContent: &awsbedrock.ReasoningContentBlock{
							RedactedContent: []byte(output.Data),
						},
					},
				}
			}
		}
	}
	openAIResp.Choices = append(openAIResp.Choices, choice)
	return openAIResp, tokenUsage, nil
}
