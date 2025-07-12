// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicParam "github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	anthropicVertex "github.com/anthropics/anthropic-sdk-go/vertex"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	openAIconstant "github.com/openai/openai-go/shared/constant"
	"github.com/santhosh-tekuri/jsonschema/v5"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// currently a requirement for GCP Vertex / Anthropic API https://docs.anthropic.com/en/api/claude-on-vertex-ai
const (
	anthropicVersionKey   = "anthropic_version"
	gcpBackendError       = "GCPBackendError"
	defaultMaxTokens      = int64(100)
	tempNotSupportedError = "temperature %.2f is not supported by Anthropic (must be between 0.0 and 1.0)"
)

var errStreamingNotSupported = errors.New("streaming is not yet supported for GCP Anthropic translation")

// openAIToGCPAnthropicTranslatorV1ChatCompletion where we can store information for streaming requests.
type openAIToGCPAnthropicTranslatorV1ChatCompletion struct{}

// Option defines a function that configures the translator.
type Option func(*openAIToGCPAnthropicTranslatorV1ChatCompletion)

// AnthropicContent Anthropic request/response structs.
type AnthropicContent struct {
	Type   string                            `json:"type"`
	Text   string                            `json:"text"`
	Source *anthropic.Base64ImageSourceParam `json:"source,omitempty"`
}

// NewChatCompletionOpenAIToGCPAnthropicTranslator creates a new translator.
func NewChatCompletionOpenAIToGCPAnthropicTranslator() OpenAIChatCompletionTranslator {
	return &openAIToGCPAnthropicTranslatorV1ChatCompletion{}
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

func isDataURI(uri string) bool {
	return strings.HasPrefix(uri, "data:")
}

func isSupportedImageMediaType(mediaType string) bool {
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

// validateFunctionParameters checks if the given parameters object is a valid JSON schema.
func validateFunctionParameters(params any) error {
	// If there are no parameters, it's valid.
	if params == nil {
		return nil
	}

	// Marshal the parameters to JSON to prepare for validation.
	schemaBytes, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("failed to marshal tool parameters to JSON: %w", err)
	}

	// Attempt to compile the bytes as a JSON Schema to validate its structure.
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource(schemaJSON, strings.NewReader(string(schemaBytes))); err != nil {
		return fmt.Errorf("invalid JSON schema for tool parameters: %w", err)
	}
	if _, err = compiler.Compile(schemaJSON); err != nil {
		return fmt.Errorf("invalid JSON schema for tool parameters: %w", err)
	}

	return nil
}

// translateToolChoice converts the OpenAI tool_choice parameter to the Anthropic format.
func translateToolChoice(openAIToolChoice any, disableParallelToolUse anthropicParam.Opt[bool]) (anthropic.ToolChoiceUnionParam, error) {
	var toolChoice anthropic.ToolChoiceUnionParam

	if openAIToolChoice == nil {
		return toolChoice, nil
	}

	switch choice := openAIToolChoice.(type) {
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
			// this is how anthropic forces tool use
			// TODO: should we check if strict true in openAI request, and if so, use this?
			toolChoice = anthropic.ToolChoiceUnionParam{OfTool: &anthropic.ToolChoiceToolParam{Name: choice}}
			toolChoice.OfTool.DisableParallelToolUse = disableParallelToolUse
		default:
			return toolChoice, fmt.Errorf("invalid tool choice type '%s'", choice)
		}
	case openai.ToolChoice:
		if choice.Type == openai.ToolTypeFunction && choice.Function.Name != "" {
			toolChoice = anthropic.ToolChoiceUnionParam{
				OfTool: &anthropic.ToolChoiceToolParam{
					Type:                   constant.Tool(choice.Type),
					Name:                   choice.Function.Name,
					DisableParallelToolUse: disableParallelToolUse,
				},
			}
		}
	}
	return toolChoice, nil
}

// translateOpenAItoAnthropicTools translates OpenAI tool and tool_choice parameters
// into the Anthropic format and returns translated tool & tool choice.
func translateOpenAItoAnthropicTools(openAITools []openai.Tool, openAIToolChoice any, parallelToolCalls *bool) (tools []anthropic.ToolUnionParam, toolChoice anthropic.ToolChoiceUnionParam, err error) {
	if len(openAITools) > 0 {
		anthropicTools := make([]anthropic.ToolUnionParam, 0, len(openAITools))
		for _, openAITool := range openAITools {
			toolParam := anthropic.ToolParam{
				Name:        openAITool.Function.Name,
				Description: anthropic.String(openAITool.Function.Description),
			}
			if openAITool.Type != openai.ToolTypeFunction {
				// Anthropic only supports 'function' tools, so we skip others.
				continue
			}

			// The parameters for the function are expected to be a JSON Schema object.
			// We can pass them through as-is.
			if err = validateFunctionParameters(openAITool.Function.Parameters); err != nil {
				return
			}

			toolParam.InputSchema = anthropic.ToolInputSchemaParam{
				Properties: openAITool.Function.Parameters,
				// TODO: support extra fields.
				ExtraFields: nil,
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

		toolChoice, err = translateToolChoice(openAIToolChoice, disableParallelToolUse)
		if err != nil {
			return
		}
	}
	return
}

// convertImageContentToAnthropic translates an OpenAI image URL into the corresponding Anthropic content block.
// It handles data URIs for various image types and PDFs, as well as remote URLs.
func convertImageContentToAnthropic(imageURL string) (anthropic.ContentBlockParamUnion, error) {
	switch {
	case isDataURI(imageURL):
		contentType, data, err := parseDataURI(imageURL)
		if err != nil {
			return anthropic.ContentBlockParamUnion{}, fmt.Errorf("failed to parse image URL: %w", err)
		}
		base64Data := base64.StdEncoding.EncodeToString(data)
		if contentType == string(constant.ValueOf[constant.ApplicationPDF]()) {
			pdfSource := anthropic.Base64PDFSourceParam{Data: base64Data}
			return anthropic.NewDocumentBlock(pdfSource), nil
		}
		if isSupportedImageMediaType(contentType) {
			return anthropic.NewImageBlockBase64(contentType, base64Data), nil
		}
		return anthropic.ContentBlockParamUnion{}, fmt.Errorf("invalid media_type for image '%s'", contentType)
	case strings.HasSuffix(strings.ToLower(imageURL), ".pdf"):
		return anthropic.NewDocumentBlock(anthropic.URLPDFSourceParam{URL: imageURL}), nil
	default:
		return anthropic.NewImageBlock(anthropic.URLImageSourceParam{URL: imageURL}), nil
	}
}

// convertContentPartsToAnthropic iterates over a slice of OpenAI content parts
// and converts each into an Anthropic content block.
func convertContentPartsToAnthropic(parts []openai.ChatCompletionContentPartUserUnionParam) ([]anthropic.ContentBlockParamUnion, error) {
	resultContent := make([]anthropic.ContentBlockParamUnion, 0, len(parts))
	for _, contentPart := range parts {
		switch {
		case contentPart.TextContent != nil:
			resultContent = append(resultContent, anthropic.NewTextBlock(contentPart.TextContent.Text))

		case contentPart.ImageContent != nil:
			block, err := convertImageContentToAnthropic(contentPart.ImageContent.ImageURL.URL)
			if err != nil {
				return nil, err
			}
			resultContent = append(resultContent, block)

		case contentPart.InputAudioContent != nil:
			return nil, fmt.Errorf("input audio content not supported yet")
		}
	}
	return resultContent, nil
}

// Helper: Convert OpenAI message content to Anthropic content.
func openAIToAnthropicContent(content interface{}) ([]anthropic.ContentBlockParamUnion, error) {
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
	case openai.StringOrArray:
		switch val := v.Value.(type) {
		case string:
			if val == "" {
				return nil, nil
			}
			return []anthropic.ContentBlockParamUnion{
				anthropic.NewTextBlock(val),
			}, nil
		case []openai.ChatCompletionContentPartUserUnionParam:
			return openAIToAnthropicContent(val)
		default:
			return nil, fmt.Errorf("unsupported StringOrArray value type: %T", val)
		}
	}
	return nil, fmt.Errorf("unsupported OpenAI content type: %T", content)
}

func extractSystemPromptFromDeveloperMsg(msg openai.ChatCompletionDeveloperMessageParam) string {
	switch v := msg.Content.Value.(type) {
	case nil:
		return ""
	case string:
		return v
	case []openai.ChatCompletionContentPartUserUnionParam:
		// Concatenate all text parts for completeness.
		var sb strings.Builder
		for _, part := range v {
			if part.TextContent != nil {
				sb.WriteString(part.TextContent.Text)
			}
		}
		return sb.String()
	case openai.StringOrArray:
		switch val := v.Value.(type) {
		case string:
			return val
		case []openai.ChatCompletionContentPartUserUnionParam:
			var sb strings.Builder
			for _, part := range val {
				if part.TextContent != nil {
					sb.WriteString(part.TextContent.Text)
				}
			}
			return sb.String()
		}
	default:
		return ""
	}
	return ""
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

// openAIMessageToAnthropicMessageRoleAssistant converts an OpenAI assistant message to Anthropic content blocks.
// The tool_use content is appended to the Anthropic message content list if tool_calls are present.
func openAIMessageToAnthropicMessageRoleAssistant(openAiMessage *openai.ChatCompletionAssistantMessageParam) (anthropicMsg *anthropic.MessageParam, err error) {
	contentBlocks := make([]anthropic.ContentBlockParamUnion, 0)
	if v, ok := openAiMessage.Content.Value.(string); ok && len(v) > 0 {
		contentBlocks = append(contentBlocks, anthropic.NewTextBlock(v))
	} else if content, ok := openAiMessage.Content.Value.(openai.ChatCompletionAssistantMessageParamContent); ok {
		switch content.Type {
		case openai.ChatCompletionAssistantMessageParamContentTypeRefusal:
			if content.Refusal != nil {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(*content.Refusal))
			}
		case openai.ChatCompletionAssistantMessageParamContentTypeText:
			if content.Text != nil {
				contentBlocks = append(contentBlocks, anthropic.NewTextBlock(*content.Text))
			}
		default:
			err = fmt.Errorf("content type not supported: %v", content.Type)
			return
		}
	}

	// Handle tool_calls (if any).
	for i := range openAiMessage.ToolCalls {
		toolCall := &openAiMessage.ToolCalls[i]
		var input map[string]interface{}
		if err = json.Unmarshal([]byte(toolCall.Function.Arguments), &input); err != nil {
			err = fmt.Errorf("failed to unmarshal tool call arguments: %w", err)
			return
		}
		toolUse := anthropic.ToolUseBlockParam{
			ID:    toolCall.ID,
			Type:  "tool_use",
			Name:  toolCall.Function.Name,
			Input: input,
		}
		contentBlocks = append(contentBlocks, anthropic.ContentBlockParamUnion{OfToolUse: &toolUse})
	}

	return &anthropic.MessageParam{
		Role:    anthropic.MessageParamRoleAssistant,
		Content: contentBlocks,
	}, nil
}

// openAIToAnthropicMessages converts OpenAI messages to Anthropic message params type, handling all roles and system/developer logic.
func openAIToAnthropicMessages(openAIMsgs []openai.ChatCompletionMessageParamUnion) (anthropicMessages []anthropic.MessageParam, systemBlocks []anthropic.TextBlockParam, err error) {
	for i := range openAIMsgs {
		msg := openAIMsgs[i]
		switch msg.Type {
		case openai.ChatMessageRoleSystem:
			if param, ok := msg.Value.(openai.ChatCompletionSystemMessageParam); ok {
				devParam := systemMsgToDeveloperMsg(param)
				systemBlocks = append(systemBlocks, anthropic.TextBlockParam{Text: extractSystemPromptFromDeveloperMsg(devParam)})
			}
		case openai.ChatMessageRoleDeveloper:
			if param, ok := msg.Value.(openai.ChatCompletionDeveloperMessageParam); ok {
				systemBlocks = append(systemBlocks, anthropic.TextBlockParam{Text: extractSystemPromptFromDeveloperMsg(param)})
			}
		case openai.ChatMessageRoleUser:
			message := msg.Value.(openai.ChatCompletionUserMessageParam)
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
		case openai.ChatMessageRoleAssistant:
			assistantMessage := msg.Value.(openai.ChatCompletionAssistantMessageParam)

			var messages *anthropic.MessageParam
			messages, err = openAIMessageToAnthropicMessageRoleAssistant(&assistantMessage)
			if err != nil {
				return
			}
			anthropicMessages = append(anthropicMessages, *messages)
		case openai.ChatMessageRoleTool:
			toolMsg := msg.Value.(openai.ChatCompletionToolMessageParam)
			var content []anthropic.ContentBlockParamUnion
			content, err = openAIToAnthropicContent(toolMsg.Content)
			if err != nil {
				return
			}
			var toolContent []anthropic.ToolResultBlockParamContentUnion
			var trb anthropic.ToolResultBlockParamContentUnion
			for _, c := range content {
				if c.OfText != nil {
					trb.OfText = c.OfText
				} else if c.OfImage != nil {
					trb.OfImage = c.OfImage
				}
				toolContent = append(toolContent, trb)
			}

			toolResultBlock := anthropic.ToolResultBlockParam{
				ToolUseID: toolMsg.ToolCallID,
				Type:      "tool_result",
				Content:   toolContent,
				// IsError:  anthropic.Bool(false), TODO: Should we support isError from openAI.
			}
			anthropicMsg := anthropic.MessageParam{
				Role: anthropic.MessageParamRoleUser,
				Content: []anthropic.ContentBlockParamUnion{
					{OfToolResult: &toolResultBlock},
				},
			}
			anthropicMessages = append(anthropicMessages, anthropicMsg)
		default:
			err = fmt.Errorf("unsupported OpenAI role type: %s", msg.Type)
			return
		}
	}
	return
}

// buildAnthropicParams is a helper function that translates an OpenAI request
// into the parameter struct required by the Anthropic SDK.
func buildAnthropicParams(openAIReq *openai.ChatCompletionRequest) (params *anthropic.MessageNewParams, err error) {
	// 1. Handle simple parameters and defaults.
	maxTokens := coalesce(defaultMaxTokens, openAIReq.MaxCompletionTokens, openAIReq.MaxTokens)

	// Translate openAI contents to anthropic params.
	// 2. Translate messages and system prompts.
	messages, systemBlocks, err := openAIToAnthropicMessages(openAIReq.Messages)
	if err != nil {
		return
	}

	// Translate tools and tool choice.
	tools, toolChoice, err := translateOpenAItoAnthropicTools(openAIReq.Tools, openAIReq.ToolChoice, openAIReq.ParallelToolCalls)
	if err != nil {
		return
	}

	// 4. Construct the final struct in one place.
	params = &anthropic.MessageNewParams{
		Messages:   messages,
		MaxTokens:  maxTokens,
		System:     systemBlocks,
		Tools:      tools,
		ToolChoice: toolChoice,
	}

	if openAIReq.Temperature != nil {
		if err = validateTemperatureForAnthropic(openAIReq.Temperature); err != nil {
			return &anthropic.MessageNewParams{}, err
		}
		params.Temperature = anthropic.Float(*openAIReq.Temperature)
	}
	if openAIReq.TopP != nil {
		params.TopP = anthropic.Float(*openAIReq.TopP)
	}

	// Handle stop sequences.
	stopSequences, err := processStop(openAIReq.Stop)
	if err != nil {
		return &anthropic.MessageNewParams{}, err
	}
	if len(stopSequences) > 0 {
		var stops []string
		for _, s := range stopSequences {
			if s != nil {
				stops = append(stops, *s)
			}
		}
		params.StopSequences = stops
	}

	return params, nil
}

// RequestBody implements [Translator.RequestBody] for GCP.
func (o *openAIToGCPAnthropicTranslatorV1ChatCompletion) RequestBody(_ []byte, openAIReq *openai.ChatCompletionRequest, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	params, err := buildAnthropicParams(openAIReq)
	if err != nil {
		return
	}

	body, err := json.Marshal(params)
	if err != nil {
		return
	}

	// TODO: add stream support.

	// GCP VERTEX PATH.
	specifier := "rawPredict"
	if openAIReq.Stream {
		// TODO: specifier = "streamRawPredict" - use this when implementing streaming.
		err = errStreamingNotSupported
		return
	}

	pathSuffix := buildGCPModelPathSuffix(GCPModelPublisherAnthropic, openAIReq.Model, specifier)
	// b. Set the "anthropic_version" key in the JSON body
	// Using same logic as anthropic go SDK: https://github.com/anthropics/anthropic-sdk-go/blob/main/vertex/vertex.go#L78
	body, _ = sjson.SetBytes(body, anthropicVersionKey, anthropicVertex.DefaultVersion)

	headerMutation, bodyMutation = buildRequestMutations(pathSuffix, body)
	return
}

// ResponseError implements [Translator.ResponseError].
func (o *openAIToGCPAnthropicTranslatorV1ChatCompletion) ResponseError(respHeaders map[string]string, body io.Reader) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
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
	mut := &extprocv3.BodyMutation_Body{}
	mut.Body, err = json.Marshal(openaiError)
	if err != nil {
		// This is an internal failure to create the response.
		return nil, nil, fmt.Errorf("failed to marshal OpenAI error body: %w", err)
	}

	headerMutation = &extprocv3.HeaderMutation{}
	setContentLength(headerMutation, mut.Body)
	bodyMutation = &extprocv3.BodyMutation{Mutation: mut}

	return headerMutation, bodyMutation, nil
}

// anthropicToolUseToOpenAICalls converts Anthropic tool_use content blocks to OpenAI tool calls.
func anthropicToolUseToOpenAICalls(block anthropic.ContentBlockUnion) ([]openai.ChatCompletionMessageToolCallParam, error) {
	var toolCalls []openai.ChatCompletionMessageToolCallParam
	if block.Type != string(constant.ValueOf[constant.ToolUse]()) {
		return toolCalls, nil
	}
	argsBytes, err := json.Marshal(block.Input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tool_use input: %w", err)
	}
	toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
		ID:   block.ID,
		Type: openai.ChatCompletionMessageToolCallTypeFunction,
		Function: openai.ChatCompletionMessageToolCallFunctionParam{
			Name:      block.Name,
			Arguments: string(argsBytes),
		},
	})

	return toolCalls, nil
}

// ResponseHeaders implements [Translator.ResponseHeaders].
func (o *openAIToGCPAnthropicTranslatorV1ChatCompletion) ResponseHeaders(headers map[string]string) (
	headerMutation *extprocv3.HeaderMutation, err error,
) {
	// TODO: Implement if needed.
	_ = headers
	return nil, nil
}

// ResponseBody implements [Translator.ResponseBody] for GCP Anthropic.
func (o *openAIToGCPAnthropicTranslatorV1ChatCompletion) ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, err error,
) {
	_ = endOfStream
	if statusStr, ok := respHeaders[statusHeaderName]; ok {
		var status int
		// Use the outer 'err' to catch parsing errors.
		if status, err = strconv.Atoi(statusStr); err == nil {
			if !isGoodStatusCode(status) {
				// Let ResponseError handle the translation. It returns its own internal error status.
				headerMutation, bodyMutation, err = o.ResponseError(respHeaders, body)
				return headerMutation, bodyMutation, LLMTokenUsage{}, err
			}
		} else {
			// Fail if the status code isn't a valid integer.
			return nil, nil, LLMTokenUsage{}, fmt.Errorf("failed to parse status code '%s': %w", statusStr, err)
		}
	}

	mut := &extprocv3.BodyMutation_Body{}
	var anthropicResp anthropic.Message
	if err = json.NewDecoder(body).Decode(&anthropicResp); err != nil {
		return nil, nil, tokenUsage, fmt.Errorf("failed to unmarshal body: %w", err)
	}

	openAIResp := openai.ChatCompletionResponse{
		Object:  string(openAIconstant.ValueOf[openAIconstant.ChatCompletion]()),
		Choices: make([]openai.ChatCompletionResponseChoice, 0),
	}
	tokenUsage = LLMTokenUsage{
		InputTokens:  uint32(anthropicResp.Usage.InputTokens),                                    //nolint:gosec
		OutputTokens: uint32(anthropicResp.Usage.OutputTokens),                                   //nolint:gosec
		TotalTokens:  uint32(anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens), //nolint:gosec
	}
	openAIResp.Usage = openai.ChatCompletionResponseUsage{
		CompletionTokens: int(anthropicResp.Usage.OutputTokens),
		PromptTokens:     int(anthropicResp.Usage.InputTokens),
		TotalTokens:      int(anthropicResp.Usage.InputTokens + anthropicResp.Usage.OutputTokens),
	}

	finishReason, err := anthropicToOpenAIFinishReason(anthropicResp.StopReason)
	if err != nil {
		return nil, nil, LLMTokenUsage{}, err
	}

	role, err := anthropicRoleToOpenAIRole(anthropic.MessageParamRole(anthropicResp.Role))
	if err != nil {
		return nil, nil, LLMTokenUsage{}, err
	}

	choice := openai.ChatCompletionResponseChoice{
		Index:        0,
		Message:      openai.ChatCompletionResponseChoiceMessage{Role: role},
		FinishReason: finishReason,
	}

	for _, output := range anthropicResp.Content {
		if output.Type == string(constant.ValueOf[constant.ToolUse]()) && output.ID != "" {
			toolCalls, toolErr := anthropicToolUseToOpenAICalls(output)
			if toolErr != nil {
				return nil, nil, tokenUsage, fmt.Errorf("failed to convert anthropic tool use to openai tool call: %w", toolErr)
			}
			choice.Message.ToolCalls = append(choice.Message.ToolCalls, toolCalls...)
		} else if output.Type == string(constant.ValueOf[constant.Text]()) && output.Text != "" {
			if choice.Message.Content == nil {
				choice.Message.Content = &output.Text
			}
		}
	}
	openAIResp.Choices = append(openAIResp.Choices, choice)

	mut.Body, err = json.Marshal(openAIResp)
	if err != nil {
		return nil, nil, tokenUsage, fmt.Errorf("failed to marshal body: %w", err)
	}

	headerMutation = &extprocv3.HeaderMutation{}
	setContentLength(headerMutation, mut.Body)

	return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, tokenUsage, nil
}
