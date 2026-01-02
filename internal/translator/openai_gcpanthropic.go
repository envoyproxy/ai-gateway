// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"cmp"
	"encoding/base64"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicParam "github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	anthropicVertex "github.com/anthropics/anthropic-sdk-go/vertex"
	openAIconstant "github.com/openai/openai-go/shared/constant"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/awsbedrock"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// currently a requirement for GCP Vertex / Anthropic API https://docs.anthropic.com/en/api/claude-on-vertex-ai
const (
	anthropicVersionKey   = "anthropic_version"
	gcpBackendError       = "GCPBackendError"
	tempNotSupportedError = "temperature %.2f is not supported by Anthropic (must be between 0.0 and 1.0)"
)

// NewChatCompletionOpenAIToGCPAnthropicTranslator implements [Factory] for OpenAI to GCP Anthropic translation.
// This translator converts OpenAI ChatCompletion API requests to GCP Anthropic API format.
func NewChatCompletionOpenAIToGCPAnthropicTranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) OpenAIChatCompletionTranslator {
	return &openAIToGCPAnthropicTranslatorV1ChatCompletion{
		apiVersion:        apiVersion,
		modelNameOverride: modelNameOverride,
	}
}

// openAIToGCPAnthropicTranslatorV1ChatCompletion translates OpenAI Chat Completions API to GCP Anthropic Claude API.
// This uses the Claude rawPredict and streamRawPredict APIs on Vertex AI:
// https://cloud.google.com/vertex-ai/generative-ai/docs/partner-models/claude
type openAIToGCPAnthropicTranslatorV1ChatCompletion struct {
	apiVersion        string
	modelNameOverride internalapi.ModelNameOverride
	streamParser      *anthropicStreamParser
	requestModel      internalapi.RequestModel
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
		return fmt.Errorf(tempNotSupportedError, *temp)
	}
	return nil
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

				// Dereference json schema
				// If the paramsMap contains $refs we need to dereference them
				var dereferencedParamsMap any
				if dereferencedParamsMap, err = jsonSchemaDereference(paramsMap); err != nil {
					return nil, anthropic.ToolChoiceUnionParam{}, fmt.Errorf("failed to dereference tool parameters: %w", err)
				}
				if paramsMap, ok = dereferencedParamsMap.(map[string]any); !ok {
					return nil, anthropic.ToolChoiceUnionParam{}, fmt.Errorf("failed to cast dereferenced tool parameters to map[string]interface{}")
				}

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

// processAssistantContent processes a single ChatCompletionAssistantMessageParamContent and returns the corresponding Anthropic content block.
func processAssistantContent(content openai.ChatCompletionAssistantMessageParamContent) (*anthropic.ContentBlockParamUnion, error) {
	switch content.Type {
	case openai.ChatCompletionAssistantMessageParamContentTypeRefusal:
		if content.Refusal != nil {
			block := anthropic.NewTextBlock(*content.Refusal)
			return &block, nil
		}
	case openai.ChatCompletionAssistantMessageParamContentTypeText:
		if content.Text != nil {
			textBlock := anthropic.NewTextBlock(*content.Text)
			if isCacheEnabled(content.AnthropicContentFields) {
				textBlock.OfText.CacheControl = content.CacheControl
			}
			return &textBlock, nil
		}
	case openai.ChatCompletionAssistantMessageParamContentTypeThinking:
		// thinking can not be cached: https://platform.claude.com/docs/en/build-with-claude/prompt-caching
		if content.Text != nil && content.Signature != nil {
			thinkBlock := anthropic.NewThinkingBlock(*content.Text, *content.Signature)
			return &thinkBlock, nil
		}
	case openai.ChatCompletionAssistantMessageParamContentTypeRedactedThinking:
		if content.RedactedContent != nil {
			switch v := content.RedactedContent.Value.(type) {
			case string:
				redactedThinkingBlock := anthropic.NewRedactedThinkingBlock(v)
				return &redactedThinkingBlock, nil
			case []byte:
				return nil, fmt.Errorf("GCP Anthropic does not support []byte format for RedactedContent, expected string")
			default:
				return nil, fmt.Errorf("unsupported RedactedContent type: %T, expected string", v)
			}
		}
	default:
		return nil, fmt.Errorf("content type not supported: %v", content.Type)
	}
	return nil, nil
}

// openAIMessageToAnthropicMessageRoleAssistant converts an OpenAI assistant message to Anthropic content blocks.
// The tool_use content is appended to the Anthropic message content list if tool_calls are present.
func openAIMessageToAnthropicMessageRoleAssistant(openAiMessage *openai.ChatCompletionAssistantMessageParam) (anthropicMsg anthropic.MessageParam, err error) {
	contentBlocks := make([]anthropic.ContentBlockParamUnion, 0)
	if v, ok := openAiMessage.Content.Value.(string); ok && len(v) > 0 {
		contentBlocks = append(contentBlocks, anthropic.NewTextBlock(v))
	} else if content, ok := openAiMessage.Content.Value.(openai.ChatCompletionAssistantMessageParamContent); ok {
		// Handle single content object
		var block *anthropic.ContentBlockParamUnion
		block, err = processAssistantContent(content)
		if err != nil {
			return anthropicMsg, err
		} else if block != nil {
			contentBlocks = append(contentBlocks, *block)
		}
	} else if contents, ok := openAiMessage.Content.Value.([]openai.ChatCompletionAssistantMessageParamContent); ok {
		// Handle array of content objects
		for _, content := range contents {
			var block *anthropic.ContentBlockParamUnion
			block, err = processAssistantContent(content)
			if err != nil {
				return anthropicMsg, err
			} else if block != nil {
				contentBlocks = append(contentBlocks, *block)
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

// buildAnthropicParams is a helper function that translates an OpenAI request
// into the parameter struct required by the Anthropic SDK.
func buildAnthropicParams(openAIReq *openai.ChatCompletionRequest) (params *anthropic.MessageNewParams, err error) {
	// 1. Handle simple parameters and defaults.
	maxTokens := cmp.Or(openAIReq.MaxCompletionTokens, openAIReq.MaxTokens)
	if maxTokens == nil {
		err = fmt.Errorf("the maximum number of tokens must be set for Anthropic, got nil instead")
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

// RequestBody implements [OpenAIChatCompletionTranslator.RequestBody] for GCP.
func (o *openAIToGCPAnthropicTranslatorV1ChatCompletion) RequestBody(_ []byte, openAIReq *openai.ChatCompletionRequest, _ bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	params, err := buildAnthropicParams(openAIReq)
	if err != nil {
		return
	}

	body, err := json.Marshal(params)
	if err != nil {
		return
	}

	o.requestModel = openAIReq.Model
	if o.modelNameOverride != "" {
		// Use modelName override if set.
		o.requestModel = o.modelNameOverride
	}

	// GCP VERTEX PATH.
	specifier := "rawPredict"
	if openAIReq.Stream {
		specifier = "streamRawPredict"
		body, err = sjson.SetBytes(body, "stream", true)
		if err != nil {
			return
		}
		o.streamParser = newAnthropicStreamParser(o.requestModel)
	}

	path := buildGCPModelPathSuffix(gcpModelPublisherAnthropic, o.requestModel, specifier)
	// b. Set the "anthropic_version" key in the JSON body
	// Using same logic as anthropic go SDK: https://github.com/anthropics/anthropic-sdk-go/blob/e252e284244755b2b2f6eef292b09d6d1e6cd989/bedrock/bedrock.go#L167
	anthropicVersion := anthropicVertex.DefaultVersion
	if o.apiVersion != "" {
		anthropicVersion = o.apiVersion
	}
	body, err = sjson.SetBytes(body, anthropicVersionKey, anthropicVersion)
	if err != nil {
		return
	}
	newBody = body
	newHeaders = []internalapi.Header{{pathHeaderName, path}, {contentLengthHeaderName, strconv.Itoa(len(newBody))}}
	return
}

// ResponseError implements [OpenAIChatCompletionTranslator.ResponseError].
func (o *openAIToGCPAnthropicTranslatorV1ChatCompletion) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	var openaiError openai.Error
	var decodeErr error

	// Check for a JSON content type to decide how to parse the error.
	if v, ok := respHeaders[contentTypeHeaderName]; ok && strings.Contains(v, jsonContentType) {
		var gcpError anthropic.ErrorResponse
		if decodeErr = json.NewDecoder(body).Decode(&gcpError); decodeErr != nil {
			// If we expect JSON but fail to decode, it's an internal translator error.
			return nil, nil, fmt.Errorf("failed to unmarshal JSON error body: %w", decodeErr)
		}
		openaiError = openai.Error{
			Type: "error",
			Error: openai.ErrorType{
				Type:    gcpError.Error.Type,
				Message: gcpError.Error.Message,
				Code:    &statusCode,
			},
		}
	} else {
		// If not JSON, read the raw body as the error message.
		var buf []byte
		buf, decodeErr = io.ReadAll(body)
		if decodeErr != nil {
			return nil, nil, fmt.Errorf("failed to read raw error body: %w", decodeErr)
		}
		openaiError = openai.Error{
			Type: "error",
			Error: openai.ErrorType{
				Type:    gcpBackendError,
				Message: string(buf),
				Code:    &statusCode,
			},
		}
	}

	// Marshal the translated OpenAI error.
	newBody, err = json.Marshal(openaiError)
	if err != nil {
		// This is an internal failure to create the response.
		return nil, nil, fmt.Errorf("failed to marshal OpenAI error body: %w", err)
	}
	newHeaders = []internalapi.Header{
		{contentTypeHeaderName, jsonContentType},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}
	return
}

// ResponseHeaders implements [OpenAIChatCompletionTranslator.ResponseHeaders].
func (o *openAIToGCPAnthropicTranslatorV1ChatCompletion) ResponseHeaders(_ map[string]string) (
	newHeaders []internalapi.Header, err error,
) {
	if o.streamParser != nil {
		newHeaders = []internalapi.Header{{contentTypeHeaderName, eventStreamContentType}}
	}
	return
}

// ResponseBody implements [OpenAIChatCompletionTranslator.ResponseBody] for GCP Anthropic.
// GCP Anthropic uses deterministic model mapping without virtualization, where the requested model
// is exactly what gets executed. The response does not contain a model field, so we return
// the request model that was originally sent.
func (o *openAIToGCPAnthropicTranslatorV1ChatCompletion) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool, span tracing.ChatCompletionSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	// If a stream parser was initialized, this is a streaming request.
	if o.streamParser != nil {
		return o.streamParser.Process(body, endOfStream, span)
	}

	var anthropicResp anthropic.Message
	if err = json.NewDecoder(body).Decode(&anthropicResp); err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to unmarshal body: %w", err)
	}

	responseModel = o.requestModel
	if anthropicResp.Model != "" {
		responseModel = string(anthropicResp.Model)
	}

	openAIResp := &openai.ChatCompletionResponse{
		ID:      anthropicResp.ID,
		Model:   responseModel,
		Object:  string(openAIconstant.ValueOf[openAIconstant.ChatCompletion]()),
		Choices: make([]openai.ChatCompletionResponseChoice, 0),
		Created: openai.JSONUNIXTime(time.Now()),
	}
	usage := anthropicResp.Usage
	tokenUsage = metrics.ExtractTokenUsageFromAnthropic(
		usage.InputTokens,
		usage.OutputTokens,
		usage.CacheReadInputTokens,
		usage.CacheCreationInputTokens,
	)
	inputTokens, _ := tokenUsage.InputTokens()
	outputTokens, _ := tokenUsage.OutputTokens()
	totalTokens, _ := tokenUsage.TotalTokens()
	cachedTokens, _ := tokenUsage.CachedInputTokens()
	cacheWriteTokens, _ := tokenUsage.CachedWriteInputTokens()
	openAIResp.Usage = openai.Usage{
		CompletionTokens: int(outputTokens),
		PromptTokens:     int(inputTokens),
		TotalTokens:      int(totalTokens),
		PromptTokensDetails: &openai.PromptTokensDetails{
			CachedTokens:      int(cachedTokens),
			CachedWriteTokens: int(cacheWriteTokens),
		},
	}

	finishReason, err := anthropicToOpenAIFinishReason(anthropicResp.StopReason)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", err
	}

	role, err := anthropicRoleToOpenAIRole(anthropic.MessageParamRole(anthropicResp.Role))
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", err
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
					return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("failed to convert anthropic tool use to openai tool call: %w", toolErr)
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

	newBody, err = json.Marshal(openAIResp)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", fmt.Errorf("failed to marshal body: %w", err)
	}

	if span != nil {
		span.RecordResponse(openAIResp)
	}
	newHeaders = []internalapi.Header{{contentLengthHeaderName, strconv.Itoa(len(newBody))}}
	return
}
