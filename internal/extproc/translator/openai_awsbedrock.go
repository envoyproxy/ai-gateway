// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"cmp"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/awsbedrock"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// NewChatCompletionOpenAIToAWSBedrockTranslator implements [Factory] for OpenAI to AWS Bedrock translation.
func NewChatCompletionOpenAIToAWSBedrockTranslator(modelNameOverride string) OpenAIChatCompletionTranslator {
	return &openAIToAWSBedrockTranslatorV1ChatCompletion{modelNameOverride: modelNameOverride}
}

// openAIToAWSBedrockTranslator implements [Translator] for /v1/chat/completions.
type openAIToAWSBedrockTranslatorV1ChatCompletion struct {
	modelNameOverride string
	stream            bool
	bufferedBody      []byte
	events            []awsbedrock.ConverseStreamEvent
	// role is from MessageStartEvent in chunked messages, and used for all openai chat completion chunk choices.
	// Translator is created for each request/response stream inside external processor, accordingly the role is not reused by multiple streams.
	role string
}

// RequestBody implements [Translator.RequestBody].
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) RequestBody(_ []byte, openAIReq *openai.ChatCompletionRequest, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	var pathTemplate string
	if openAIReq.Stream {
		o.stream = true
		pathTemplate = "/model/%s/converse-stream"
	} else {
		pathTemplate = "/model/%s/converse"
	}

	modelName := openAIReq.Model
	if o.modelNameOverride != "" {
		// Use modelName override if set.
		modelName = o.modelNameOverride
	}

	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{
				Key:      ":path",
				RawValue: []byte(fmt.Sprintf(pathTemplate, modelName)),
			}},
		},
	}

	var bedrockReq awsbedrock.ConverseInput
	// Convert InferenceConfiguration.
	bedrockReq.InferenceConfig = &awsbedrock.InferenceConfiguration{}
	bedrockReq.InferenceConfig.Temperature = openAIReq.Temperature
	bedrockReq.InferenceConfig.TopP = openAIReq.TopP

	bedrockReq.InferenceConfig.MaxTokens = cmp.Or(openAIReq.MaxCompletionTokens, openAIReq.MaxTokens)

	stopSequence, err := processStop(openAIReq.Stop)
	if err != nil {
		return
	}
	if len(stopSequence) > 0 {
		bedrockReq.InferenceConfig.StopSequences = stopSequence
	}

	// Convert Chat Completion messages.
	err = o.openAIMessageToBedrockMessage(openAIReq, &bedrockReq)
	if err != nil {
		return nil, nil, err
	}
	// Convert ToolConfiguration.
	if len(openAIReq.Tools) > 0 {
		err = o.openAIToolsToBedrockToolConfiguration(openAIReq, &bedrockReq)
		if err != nil {
			return nil, nil, err
		}
	}

	mut := &extprocv3.BodyMutation_Body{}
	if mut.Body, err = json.Marshal(bedrockReq); err != nil {
		return nil, nil, fmt.Errorf("failed to marshal body: %w", err)
	}
	setContentLength(headerMutation, mut.Body)
	return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, nil
}

// openAIToolsToBedrockToolConfiguration converts openai ChatCompletion tools to aws bedrock tool configurations.
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) openAIToolsToBedrockToolConfiguration(openAIReq *openai.ChatCompletionRequest,
	bedrockReq *awsbedrock.ConverseInput,
) error {
	bedrockReq.ToolConfig = &awsbedrock.ToolConfiguration{}
	tools := make([]*awsbedrock.Tool, 0, len(openAIReq.Tools))
	for i := range openAIReq.Tools {
		toolDefinition := &openAIReq.Tools[i]
		if toolDefinition.Function != nil {
			var toolName, toolDes string
			toolName = toolDefinition.Function.Name
			toolDes = toolDefinition.Function.Description
			tool := &awsbedrock.Tool{
				ToolSpec: &awsbedrock.ToolSpecification{
					Name:        &toolName,
					Description: &toolDes,
					InputSchema: &awsbedrock.ToolInputSchema{
						JSON: toolDefinition.Function.Parameters,
					},
				},
			}
			tools = append(tools, tool)
		}
	}
	bedrockReq.ToolConfig.Tools = tools

	if openAIReq.ToolChoice != nil {
		if toolChoice, ok := openAIReq.ToolChoice.(string); ok {
			switch toolChoice {
			case "auto":
				bedrockReq.ToolConfig.ToolChoice = &awsbedrock.ToolChoice{
					Auto: &awsbedrock.AutoToolChoice{},
				}
			case "required":
				bedrockReq.ToolConfig.ToolChoice = &awsbedrock.ToolChoice{
					Any: &awsbedrock.AnyToolChoice{},
				}
			default:
				// Anthropic Claude supports tool_choice parameter with three options.
				// * `auto` allows Claude to decide whether to call any provided tools or not.
				// * `any` tells Claude that it must use one of the provided tools, but doesn't force a particular tool.
				// * `tool` allows us to force Claude to always use a particular tool.
				// The tool option is only applied to Anthropic Claude.
				if strings.Contains(openAIReq.Model, "anthropic") && strings.Contains(openAIReq.Model, "claude") {
					bedrockReq.ToolConfig.ToolChoice = &awsbedrock.ToolChoice{
						Tool: &awsbedrock.SpecificToolChoice{
							Name: &toolChoice,
						},
					}
				}
			}
		} else if toolChoice, ok := openAIReq.ToolChoice.(openai.ToolChoice); ok {
			tool := string(toolChoice.Type)
			bedrockReq.ToolConfig.ToolChoice = &awsbedrock.ToolChoice{
				Tool: &awsbedrock.SpecificToolChoice{
					Name: &tool,
				},
			}
		} else {
			return fmt.Errorf("unexpected type: %T", openAIReq.ToolChoice)
		}
	}
	return nil
}

// openAIMessageToBedrockMessageRoleUser converts openai user role message.
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) openAIMessageToBedrockMessageRoleUser(
	openAiMessage *openai.ChatCompletionUserMessageParam, role string,
) (*awsbedrock.Message, error) {
	if v, ok := openAiMessage.Content.Value.(string); ok {
		return &awsbedrock.Message{
			Role: role,
			Content: []*awsbedrock.ContentBlock{
				{Text: ptr.To(v)},
			},
		}, nil
	} else if contents, ok := openAiMessage.Content.Value.([]openai.ChatCompletionContentPartUserUnionParam); ok {
		chatMessage := &awsbedrock.Message{Role: role}
		chatMessage.Content = make([]*awsbedrock.ContentBlock, 0, len(contents))
		for i := range contents {
			contentPart := &contents[i]
			if contentPart.TextContent != nil {
				textContentPart := contentPart.TextContent
				chatMessage.Content = append(chatMessage.Content, &awsbedrock.ContentBlock{
					Text: &textContentPart.Text,
				})
			} else if contentPart.ImageContent != nil {
				imageContentPart := contentPart.ImageContent
				contentType, b, err := parseDataURI(imageContentPart.ImageURL.URL)
				if err != nil {
					return nil, fmt.Errorf("failed to parse image URL: %s %w", imageContentPart.ImageURL.URL, err)
				}
				var format string
				switch contentType {
				case mimeTypeImagePNG:
					format = "png"
				case mimeTypeImageJPEG:
					format = "jpeg"
				case mimeTypeImageGIF:
					format = "gif"
				case mimeTypeImageWEBP:
					format = "webp"
				default:
					return nil, fmt.Errorf("unsupported image type: %s please use one of [png, jpeg, gif, webp]",
						contentType)
				}

				chatMessage.Content = append(chatMessage.Content, &awsbedrock.ContentBlock{
					Image: &awsbedrock.ImageBlock{
						Format: format,
						Source: awsbedrock.ImageSource{
							Bytes: b, // Decoded data as bytes.
						},
					},
				})
			}
		}
		return chatMessage, nil
	}
	return nil, fmt.Errorf("unexpected content type")
}

// unmarshalToolCallArguments is a helper method to unmarshal tool call arguments.
func unmarshalToolCallArguments(arguments string) (map[string]interface{}, error) {
	var input map[string]interface{}
	if err := json.Unmarshal([]byte(arguments), &input); err != nil {
		return nil, fmt.Errorf("failed to unmarshal tool call arguments: %w", err)
	}
	return input, nil
}

// openAIMessageToBedrockMessageRoleAssistant converts openai assistant role message
// The tool content is appended to the bedrock message content list if tool_call is in openai message.
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) openAIMessageToBedrockMessageRoleAssistant(
	openAiMessage *openai.ChatCompletionAssistantMessageParam, role string,
) (*awsbedrock.Message, error) {
	var bedrockMessage *awsbedrock.Message
	contentBlocks := make([]*awsbedrock.ContentBlock, 0)
	if v, ok := openAiMessage.Content.Value.(string); ok && len(v) > 0 {
		contentBlocks = append(contentBlocks, &awsbedrock.ContentBlock{Text: &v})
	} else if content, ok := openAiMessage.Content.Value.(openai.ChatCompletionAssistantMessageParamContent); ok {
		if content.Type == openai.ChatCompletionAssistantMessageParamContentTypeRefusal {
			contentBlocks = append(contentBlocks, &awsbedrock.ContentBlock{Text: content.Refusal})
		} else if content.Text != nil {
			contentBlocks = append(contentBlocks, &awsbedrock.ContentBlock{Text: content.Text})
		}
	}
	bedrockMessage = &awsbedrock.Message{
		Role:    role,
		Content: contentBlocks,
	}
	for i := range openAiMessage.ToolCalls {
		toolCall := &openAiMessage.ToolCalls[i]
		input, err := unmarshalToolCallArguments(toolCall.Function.Arguments)
		if err != nil {
			return nil, err
		}
		bedrockMessage.Content = append(bedrockMessage.Content,
			&awsbedrock.ContentBlock{
				ToolUse: &awsbedrock.ToolUseBlock{
					Name:      toolCall.Function.Name,
					ToolUseID: toolCall.ID,
					Input:     input,
				},
			})
	}
	return bedrockMessage, nil
}

// openAIMessageToBedrockMessageRoleSystem converts openai system role message.
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) openAIMessageToBedrockMessageRoleSystem(
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

// openAIMessageToBedrockMessageRoleTool converts openai tool role message.
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) openAIMessageToBedrockMessageRoleTool(
	openAiMessage *openai.ChatCompletionToolMessageParam, role string,
) (*awsbedrock.Message, error) {
	// Validate and cast the openai content value into bedrock content block.
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

// openAIMessageToBedrockMessage converts openai ChatCompletion messages to aws bedrock messages.
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) openAIMessageToBedrockMessage(openAIReq *openai.ChatCompletionRequest,
	bedrockReq *awsbedrock.ConverseInput,
) error {
	// Convert Messages.
	bedrockReq.Messages = make([]*awsbedrock.Message, 0, len(openAIReq.Messages))
	openAIReqMessageLen, i := len(openAIReq.Messages), 0
	for i < openAIReqMessageLen {
		msg := &openAIReq.Messages[i]
		switch msg.Type {
		case openai.ChatMessageRoleUser:
			userMessage := msg.Value.(openai.ChatCompletionUserMessageParam)
			bedrockMessage, err := o.openAIMessageToBedrockMessageRoleUser(&userMessage, msg.Type)
			if err != nil {
				return err
			}
			bedrockReq.Messages = append(bedrockReq.Messages, bedrockMessage)
		case openai.ChatMessageRoleAssistant:
			assistantMessage := msg.Value.(openai.ChatCompletionAssistantMessageParam)
			bedrockMessage, err := o.openAIMessageToBedrockMessageRoleAssistant(&assistantMessage, msg.Type)
			if err != nil {
				return err
			}
			bedrockReq.Messages = append(bedrockReq.Messages, bedrockMessage)
		case openai.ChatMessageRoleSystem:
			if bedrockReq.System == nil {
				bedrockReq.System = make([]*awsbedrock.SystemContentBlock, 0)
			}
			systemMessage := msg.Value.(openai.ChatCompletionSystemMessageParam)
			err := o.openAIMessageToBedrockMessageRoleSystem(&systemMessage, &bedrockReq.System)
			if err != nil {
				return err
			}
		case openai.ChatMessageRoleDeveloper:
			message := msg.Value.(openai.ChatCompletionDeveloperMessageParam)
			if bedrockReq.System == nil {
				bedrockReq.System = []*awsbedrock.SystemContentBlock{}
			}

			if text, ok := message.Content.Value.(string); ok {
				bedrockReq.System = append(bedrockReq.System, &awsbedrock.SystemContentBlock{
					Text: text,
				})
			} else {
				if contents, ok := message.Content.Value.([]openai.ChatCompletionContentPartTextParam); ok {
					for i := range contents {
						contentPart := &contents[i]
						textContentPart := contentPart.Text
						bedrockReq.System = append(bedrockReq.System, &awsbedrock.SystemContentBlock{
							Text: textContentPart,
						})
					}
				} else {
					return fmt.Errorf("unexpected content type for developer message")
				}
			}
		case openai.ChatMessageRoleTool:
			toolMessage := msg.Value.(openai.ChatCompletionToolMessageParam)
			// Bedrock does not support tool role, merging to the user role.
			bedrockMessage, err := o.openAIMessageToBedrockMessageRoleTool(&toolMessage, awsbedrock.ConversationRoleUser)
			if err != nil {
				return err
			}
			// Coalesce consecutive tool messages following a user message.
			for i+1 < openAIReqMessageLen {
				nextMessage := &openAIReq.Messages[i+1]
				if nextMessage.Type != openai.ChatMessageRoleTool {
					break
				}

				nextToolMessage, ok := nextMessage.Value.(openai.ChatCompletionToolMessageParam)
				if !ok {
					return fmt.Errorf("expected ChatCompletionToolMessageParam, got %T", nextMessage.Value)
				}
				nextBedrockMessage, err := o.openAIMessageToBedrockMessageRoleTool(&nextToolMessage, awsbedrock.ConversationRoleUser)
				if err != nil {
					return err
				}
				if len(nextBedrockMessage.Content) > 0 {
					bedrockMessage.Content = append(bedrockMessage.Content, nextBedrockMessage.Content[0])
				}
				i++
			}

			bedrockReq.Messages = append(bedrockReq.Messages, bedrockMessage)
		default:
			return fmt.Errorf("unexpected role: %s", msg.Type)
		}

		i++
	}
	return nil
}

// ResponseHeaders implements [Translator.ResponseHeaders].
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) ResponseHeaders(headers map[string]string) (
	headerMutation *extprocv3.HeaderMutation, err error,
) {
	if o.stream {
		contentType := headers["content-type"]
		if contentType == "application/vnd.amazon.eventstream" {
			// We need to change the content-type to text/event-stream for streaming responses.
			return &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{Header: &corev3.HeaderValue{Key: "content-type", Value: "text/event-stream"}},
				},
			}, nil
		}
	}
	return nil, nil
}

func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) bedrockStopReasonToOpenAIStopReason(
	stopReason *string,
) openai.ChatCompletionChoicesFinishReason {
	if stopReason == nil {
		return openai.ChatCompletionChoicesFinishReasonStop
	}

	switch *stopReason {
	case awsbedrock.StopReasonStopSequence, awsbedrock.StopReasonEndTurn:
		return openai.ChatCompletionChoicesFinishReasonStop
	case awsbedrock.StopReasonMaxTokens:
		return openai.ChatCompletionChoicesFinishReasonLength
	case awsbedrock.StopReasonContentFiltered:
		return openai.ChatCompletionChoicesFinishReasonContentFilter
	case awsbedrock.StopReasonToolUse:
		return openai.ChatCompletionChoicesFinishReasonToolCalls
	default:
		return openai.ChatCompletionChoicesFinishReasonStop
	}
}

func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) bedrockToolUseToOpenAICalls(
	toolUse *awsbedrock.ToolUseBlock,
) *openai.ChatCompletionMessageToolCallParam {
	if toolUse == nil {
		return nil
	}
	arguments, err := json.Marshal(toolUse.Input)
	if err != nil {
		return nil
	}
	return &openai.ChatCompletionMessageToolCallParam{
		ID: toolUse.ToolUseID,
		Function: openai.ChatCompletionMessageToolCallFunctionParam{
			Name:      toolUse.Name,
			Arguments: string(arguments),
		},
		Type: openai.ChatCompletionMessageToolCallTypeFunction,
	}
}

// ResponseError implements [Translator.ResponseError]
// Translate AWS Bedrock exceptions to OpenAI error type.
// The error type is stored in the "x-amzn-errortype" HTTP header for AWS error responses.
// If AWS Bedrock connection fails the error body is translated to OpenAI error type for events such as HTTP 503 or 504.
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) ResponseError(respHeaders map[string]string, body io.Reader) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	var openaiError openai.Error
	if v, ok := respHeaders[contentTypeHeaderName]; ok && v == jsonContentType {
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
	mut := &extprocv3.BodyMutation_Body{}
	mut.Body, err = json.Marshal(openaiError)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal error body: %w", err)
	}
	headerMutation = &extprocv3.HeaderMutation{}
	setContentLength(headerMutation, mut.Body)
	return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, nil
}

// ResponseBody implements [Translator.ResponseBody].
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) ResponseBody(respHeaders map[string]string, body io.Reader, endOfStream bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, err error,
) {
	if statusStr, ok := respHeaders[statusHeaderName]; ok {
		var status int
		if status, err = strconv.Atoi(statusStr); err == nil {
			if !isGoodStatusCode(status) {
				headerMutation, bodyMutation, err = o.ResponseError(respHeaders, body)
				return headerMutation, bodyMutation, LLMTokenUsage{}, err
			}
		}
	}
	mut := &extprocv3.BodyMutation_Body{}
	if o.stream {
		var buf []byte
		buf, err = io.ReadAll(body)
		if err != nil {
			return nil, nil, tokenUsage, fmt.Errorf("failed to read body: %w", err)
		}
		o.bufferedBody = append(o.bufferedBody, buf...)
		o.extractAmazonEventStreamEvents()

		for i := range o.events {
			event := &o.events[i]
			if usage := event.Usage; usage != nil {
				tokenUsage = LLMTokenUsage{
					InputTokens:  uint32(usage.InputTokens),  //nolint:gosec
					OutputTokens: uint32(usage.OutputTokens), //nolint:gosec
					TotalTokens:  uint32(usage.TotalTokens),  //nolint:gosec
				}
			}
			oaiEvent, ok := o.convertEvent(event)
			if !ok {
				continue
			}
			var oaiEventBytes []byte
			oaiEventBytes, err = json.Marshal(oaiEvent)
			if err != nil {
				panic(fmt.Errorf("failed to marshal event: %w", err))
			}
			mut.Body = append(mut.Body, []byte("data: ")...)
			mut.Body = append(mut.Body, oaiEventBytes...)
			mut.Body = append(mut.Body, []byte("\n\n")...)
		}

		if endOfStream {
			mut.Body = append(mut.Body, []byte("data: [DONE]\n")...)
		}
		return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, tokenUsage, nil
	}

	var bedrockResp awsbedrock.ConverseResponse
	if err = json.NewDecoder(body).Decode(&bedrockResp); err != nil {
		return nil, nil, tokenUsage, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	openAIResp := openai.ChatCompletionResponse{
		Object:  "chat.completion",
		Choices: make([]openai.ChatCompletionResponseChoice, 0),
	}
	// Convert token usage.
	if bedrockResp.Usage != nil {
		tokenUsage = LLMTokenUsage{
			InputTokens:  uint32(bedrockResp.Usage.InputTokens),  //nolint:gosec
			OutputTokens: uint32(bedrockResp.Usage.OutputTokens), //nolint:gosec
			TotalTokens:  uint32(bedrockResp.Usage.TotalTokens),  //nolint:gosec
		}
		openAIResp.Usage = openai.ChatCompletionResponseUsage{
			TotalTokens:      bedrockResp.Usage.TotalTokens,
			PromptTokens:     bedrockResp.Usage.InputTokens,
			CompletionTokens: bedrockResp.Usage.OutputTokens,
		}
	}

	// AWS Bedrock does not support N(multiple choices) > 0, so there could be only one choice.
	choice := openai.ChatCompletionResponseChoice{
		Index: (int64)(0),
		Message: openai.ChatCompletionResponseChoiceMessage{
			Role: bedrockResp.Output.Message.Role,
		},
		FinishReason: o.bedrockStopReasonToOpenAIStopReason(bedrockResp.StopReason),
	}
	for _, output := range bedrockResp.Output.Message.Content {
		if toolCall := o.bedrockToolUseToOpenAICalls(output.ToolUse); toolCall != nil {
			choice.Message.ToolCalls = []openai.ChatCompletionMessageToolCallParam{*toolCall}
		} else if output.Text != nil {
			// For the converse response the assumption is that there is only one text content block, we take the first one.
			if choice.Message.Content == nil {
				choice.Message.Content = output.Text
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

// extractAmazonEventStreamEvents extracts [awsbedrock.ConverseStreamEvent] from the buffered body.
// The extracted events are stored in the processor's events field.
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) extractAmazonEventStreamEvents() {
	// TODO: Maybe reuse the reader and decoder.
	r := bytes.NewReader(o.bufferedBody)
	dec := eventstream.NewDecoder()
	o.events = o.events[:0]
	var lastRead int64
	for {
		msg, err := dec.Decode(r, nil)
		if err != nil {
			// When failed, we stop processing the events.
			// Copy the unread bytes to the beginning of the buffer.
			copy(o.bufferedBody, o.bufferedBody[lastRead:])
			o.bufferedBody = o.bufferedBody[:len(o.bufferedBody)-int(lastRead)]
			return
		}
		var event awsbedrock.ConverseStreamEvent
		if err := json.Unmarshal(msg.Payload, &event); err == nil {
			o.events = append(o.events, event)
		}
		lastRead = r.Size() - int64(r.Len())
	}
}

var emptyString = ""

// convertEvent converts an [awsbedrock.ConverseStreamEvent] to an [openai.ChatCompletionResponseChunk].
// This is a static method and does not require a receiver, but defined as a method for namespacing.
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) convertEvent(event *awsbedrock.ConverseStreamEvent) (openai.ChatCompletionResponseChunk, bool) {
	const object = "chat.completion.chunk"
	chunk := openai.ChatCompletionResponseChunk{Object: object}

	switch {
	case event.Usage != nil:
		chunk.Usage = &openai.ChatCompletionResponseUsage{
			TotalTokens:      event.Usage.TotalTokens,
			PromptTokens:     event.Usage.InputTokens,
			CompletionTokens: event.Usage.OutputTokens,
		}
	case event.Role != nil:
		chunk.Choices = append(chunk.Choices, openai.ChatCompletionResponseChunkChoice{
			Delta: &openai.ChatCompletionResponseChunkChoiceDelta{
				Role:    *event.Role,
				Content: &emptyString,
			},
		})
		o.role = *event.Role
	case event.Delta != nil:
		if event.Delta.Text != nil {
			chunk.Choices = append(chunk.Choices, openai.ChatCompletionResponseChunkChoice{
				Delta: &openai.ChatCompletionResponseChunkChoiceDelta{
					Role:    o.role,
					Content: event.Delta.Text,
				},
			})
		} else if event.Delta.ToolUse != nil {
			chunk.Choices = append(chunk.Choices, openai.ChatCompletionResponseChunkChoice{
				Delta: &openai.ChatCompletionResponseChunkChoiceDelta{
					Role: o.role,
					ToolCalls: []openai.ChatCompletionMessageToolCallParam{
						{
							Function: openai.ChatCompletionMessageToolCallFunctionParam{
								Arguments: event.Delta.ToolUse.Input,
							},
							Type: openai.ChatCompletionMessageToolCallTypeFunction,
						},
					},
				},
			})
		}
	case event.Start != nil:
		if event.Start.ToolUse != nil {
			chunk.Choices = append(chunk.Choices, openai.ChatCompletionResponseChunkChoice{
				Delta: &openai.ChatCompletionResponseChunkChoiceDelta{
					Role: o.role,
					ToolCalls: []openai.ChatCompletionMessageToolCallParam{
						{
							ID: event.Start.ToolUse.ToolUseID,
							Function: openai.ChatCompletionMessageToolCallFunctionParam{
								Name: event.Start.ToolUse.Name,
							},
							Type: openai.ChatCompletionMessageToolCallTypeFunction,
						},
					},
				},
			})
		}
	case event.StopReason != nil:
		chunk.Choices = append(chunk.Choices, openai.ChatCompletionResponseChunkChoice{
			Delta: &openai.ChatCompletionResponseChunkChoiceDelta{
				Role:    o.role,
				Content: ptr.To(emptyString),
			},
			FinishReason: o.bedrockStopReasonToOpenAIStopReason(event.StopReason),
		})
	default:
		return chunk, false
	}
	return chunk, true
}
