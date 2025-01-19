package translator

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"regexp"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/awsbedrock"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/extproc/router"
)

const (
	anthropic = "anthropic"
	claude    = "claude"
)

var supportedMediumType = map[string]string{
	"image/png":  "png",
	"image/jpeg": "jpeg",
	"image/gif":  "gif",
	"image/webp": "webp",
}

// newOpenAIToAWSBedrockTranslator implements [TranslatorFactory] for OpenAI to AWS Bedrock translation.
func newOpenAIToAWSBedrockTranslator(path string) (Translator, error) {
	if path == "/v1/chat/completions" {
		return &openAIToAWSBedrockTranslatorV1ChatCompletion{}, nil
	} else {
		return nil, fmt.Errorf("unsupported path: %s", path)
	}
}

func isAnthropicClaude(modelID string) bool {
	return strings.Contains(modelID, anthropic) && strings.Contains(modelID, claude)
}

// openAIToAWSBedrockTranslator implements [Translator] for /v1/chat/completions.
type openAIToAWSBedrockTranslatorV1ChatCompletion struct {
	stream       bool
	bufferedBody []byte
	events       []awsbedrock.ConverseStreamEvent
}

// RequestBody implements [Translator.RequestBody].
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) RequestBody(body router.RequestBody) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, override *extprocv3http.ProcessingMode, err error,
) {
	openAIReq, ok := body.(*openai.ChatCompletionRequest)
	if !ok {
		return nil, nil, nil, fmt.Errorf("unexpected body type: %T", body)
	}

	var pathTemplate string
	if openAIReq.Stream {
		o.stream = true
		// We need to change the processing mode for streaming requests.
		override = &extprocv3http.ProcessingMode{
			ResponseHeaderMode: extprocv3http.ProcessingMode_SEND,
			ResponseBodyMode:   extprocv3http.ProcessingMode_STREAMED,
		}
		pathTemplate = "/model/%s/converse-stream"
	} else {
		pathTemplate = "/model/%s/converse"
	}

	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{
				Key:      ":path",
				RawValue: []byte(fmt.Sprintf(pathTemplate, openAIReq.Model)),
			}},
		},
	}

	var bedrockReq awsbedrock.ConverseInput
	// Convert InferenceConfiguration.
	o.openAIToBedrockInferenceConfiguration(openAIReq, &bedrockReq)
	// Convert Chat Completion messages.
	err = o.openAIMessageToBedrockMessage(openAIReq, &bedrockReq)
	if err != nil {
		return nil, nil, nil, err
	}
	// Convert ToolConfiguration.
	if len(openAIReq.Tools) > 0 {
		err = o.openAIToolsToBedrockToolConfiguration(openAIReq, &bedrockReq)
		if err != nil {
			return nil, nil, nil, err
		}
	}

	mut := &extprocv3.BodyMutation_Body{}
	if b, err := json.Marshal(bedrockReq); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal body: %w", err)
	} else {
		mut.Body = b
	}
	setContentLength(headerMutation, mut.Body)
	return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, override, nil
}

func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) openAIToBedrockInferenceConfiguration(
	openAIReq *openai.ChatCompletionRequest, bedrockReq *awsbedrock.ConverseInput,
) {
	bedrockReq.InferenceConfig = &awsbedrock.InferenceConfiguration{}
	bedrockReq.InferenceConfig.MaxTokens = openAIReq.MaxTokens
	bedrockReq.InferenceConfig.StopSequences = openAIReq.Stop
	bedrockReq.InferenceConfig.Temperature = openAIReq.Temperature
	bedrockReq.InferenceConfig.TopP = openAIReq.TopP
}

// openAIToolsToBedrockToolConfiguration converts openai ChatCompletion tools to aws bedrock tool configurations
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) openAIToolsToBedrockToolConfiguration(openAIReq *openai.ChatCompletionRequest,
	bedrockReq *awsbedrock.ConverseInput,
) error {
	bedrockReq.ToolConfig = &awsbedrock.ToolConfiguration{}
	tools := make([]*awsbedrock.Tool, 0, len(openAIReq.Tools))
	for _, toolDefinition := range openAIReq.Tools {
		tool := &awsbedrock.Tool{
			ToolSpec: &awsbedrock.ToolSpecification{
				Name:        &toolDefinition.Function.Name,
				Description: &toolDefinition.Function.Description,
				InputSchema: &awsbedrock.ToolInputSchema{
					JSON: toolDefinition.Function.Parameters,
				},
			},
		}
		tools = append(tools, tool)
	}
	bedrockReq.ToolConfig.Tools = tools

	if openAIReq.ToolChoice != nil {
		switch reflect.TypeOf(openAIReq.ToolChoice).Kind() {
		case reflect.String:
			toolChoice := openAIReq.ToolChoice.(string)
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
				// auto: allows Claude to decide whether to call any provided tools or not.
				// any tells Claude that it must use one of the provided tools, but doesn't force a particular tool.
				// tool allows us to force Claude to always use a particular tool.
				// The tool option is only applied to Anthropic Claude
				if isAnthropicClaude(openAIReq.Model) {
					bedrockReq.ToolConfig.ToolChoice = &awsbedrock.ToolChoice{
						Tool: &awsbedrock.SpecificToolChoice{
							Name: &toolChoice,
						},
					}
				}
			}
		case reflect.Struct:
			toolChoice := openAIReq.ToolChoice.(openai.ToolChoice)
			tool := (string)(toolChoice.Type)
			bedrockReq.ToolConfig.ToolChoice = &awsbedrock.ToolChoice{
				Tool: &awsbedrock.SpecificToolChoice{
					Name: &tool,
				},
			}
		default:
			return fmt.Errorf("unexpected type: %s", reflect.TypeOf(openAIReq.ToolChoice).Kind())
		}
	}
	return nil
}

// https://developer.mozilla.org/en-US/docs/Web/URI/Schemes/data#syntax
var regDataURI = regexp.MustCompile(`\Adata:(.+?)?(;base64)?,`)

// parseDataURI parse data uri example: data:image/jpeg;base64,/9j/4AAQSkZJRgABAgAAZABkAAD
func parseDataURI(uri string) (string, []byte, bool) {
	matches := regDataURI.FindStringSubmatch(uri)
	if len(matches) != 3 {
		return "", nil, false
	}
	l := len(matches[0])
	contentType := matches[1]
	bin, _ := base64.StdEncoding.DecodeString(uri[l:])
	return contentType, bin, true
}

// openAIMessageToBedrockMessageRoleUser converts openai user role message
// returns a list of bedrock message and error
// the tool content is appended to the list of bedrock message list if tool_call is in openai message.
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) openAIMessageToBedrockMessageRoleUser(
	openAiMessage *openai.ChatCompletionMessageParamUnion,
) (*awsbedrock.Message, error) {
	message := openAiMessage.Value.(openai.ChatCompletionUserMessageParam)
	if v, ok := message.Content.Value.(string); ok {
		return &awsbedrock.Message{
			Role: openAiMessage.Type,
			Content: []*awsbedrock.ContentBlock{
				{Text: ptr.To(v)},
			},
		}, nil
	} else if contents, ok := message.Content.Value.([]openai.ChatCompletionContentPartUserUnionParam); ok {
		chatMessage := &awsbedrock.Message{Role: openAiMessage.Type}
		chatMessage.Content = make([]*awsbedrock.ContentBlock, 0, len(contents))
		for _, contentPart := range contents {
			if contentPart.TextContent != nil {
				textContentPart := contentPart.TextContent
				chatMessage.Content = append(chatMessage.Content, &awsbedrock.ContentBlock{
					Text: &textContentPart.Text,
				})
			} else if contentPart.ImageContent != nil {
				imageContentPart := contentPart.ImageContent
				contentType, b, valid := parseDataURI(imageContentPart.ImageURL.URL)
				if !valid {
					return nil, fmt.Errorf("failed to parse image URL: %s", imageContentPart.ImageURL.URL)
				}
				if format, supported := supportedMediumType[contentType]; !supported {
					return nil, fmt.Errorf("unsupported image type: %s please use one of [png, jpeg, gif, webp]",
						contentType)
				} else {
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
		}
		return chatMessage, nil
	} else {
		return nil, fmt.Errorf("unexpected content type")
	}
}

// openAIMessageToBedrockMessageRoleAssistant converts openai assistant role message
// returns a list of bedrock message and error
// the tool content is appended to the list of bedrock message list if tool_call is in openai message
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) openAIMessageToBedrockMessageRoleAssistant(
	openAiMessage *openai.ChatCompletionMessageParamUnion,
) (*awsbedrock.Message, error) {
	var bedrockMessage *awsbedrock.Message
	message := openAiMessage.Value.(openai.ChatCompletionAssistantMessageParam)

	if message.Content.Type == openai.ChatCompletionAssistantMessageParamContentTypeRefusal {
		bedrockMessage = &awsbedrock.Message{
			Role:    openAiMessage.Type,
			Content: []*awsbedrock.ContentBlock{{Text: message.Content.Refusal}},
		}
	} else {
		bedrockMessage = &awsbedrock.Message{
			Role:    openAiMessage.Type,
			Content: []*awsbedrock.ContentBlock{{Text: message.Content.Text}},
		}
	}
	// Process tool_calls.
	for _, toolCall := range message.ToolCalls {
		bedrockMessage.Content = append(bedrockMessage.Content,
			&awsbedrock.ContentBlock{
				ToolUse: &awsbedrock.ToolUseBlock{
					Name:      toolCall.Function.Name,
					ToolUseID: toolCall.ID,
					Input:     toolCall.Function.Arguments,
				},
			})
	}
	return bedrockMessage, nil
}

// openAIMessageToBedrockMessageRoleSystem converts openai system role message
// returns a list of bedrock system content and error
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) openAIMessageToBedrockMessageRoleSystem(
	openAiMessage *openai.ChatCompletionMessageParamUnion, bedrockSystem *[]*awsbedrock.SystemContentBlock,
) error {
	message := openAiMessage.Value.(openai.ChatCompletionSystemMessageParam)
	if v, ok := message.Content.Value.(string); ok {
		*bedrockSystem = append(*bedrockSystem, &awsbedrock.SystemContentBlock{
			Text: v,
		})
	} else if contents, ok := message.Content.Value.([]openai.ChatCompletionContentPartTextParam); ok {
		for _, contentPart := range contents {
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

// openAIMessageToBedrockMessageRoleTool converts openai tool role message
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) openAIMessageToBedrockMessageRoleTool(
	openAiMessage *openai.ChatCompletionMessageParamUnion,
) (*awsbedrock.Message, error) {
	message := openAiMessage.Value.(openai.ChatCompletionToolMessageParam)
	return &awsbedrock.Message{
		// Bedrock does not support tool role, merging to the user role.
		Role: awsbedrock.ConversationRoleUser,
		Content: []*awsbedrock.ContentBlock{
			{
				ToolResult: &awsbedrock.ToolResultBlock{
					Content: []*awsbedrock.ToolResultContentBlock{
						{
							Text: message.Content.Value.(*string),
						},
					},
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
	for _, msg := range openAIReq.Messages {
		switch msg.Type {
		case openai.ChatMessageRoleUser:
			bedrockMessage, err := o.openAIMessageToBedrockMessageRoleUser(&msg)
			if err != nil {
				return err
			}
			bedrockReq.Messages = append(bedrockReq.Messages, bedrockMessage)
		case openai.ChatMessageRoleAssistant:
			bedrockMessage, err := o.openAIMessageToBedrockMessageRoleAssistant(&msg)
			if err != nil {
				return err
			}
			bedrockReq.Messages = append(bedrockReq.Messages, bedrockMessage)
		case openai.ChatMessageRoleSystem:
			if bedrockReq.System == nil {
				bedrockReq.System = []*awsbedrock.SystemContentBlock{}
			}
			err := o.openAIMessageToBedrockMessageRoleSystem(&msg, &bedrockReq.System)
			if err != nil {
				return err
			}
		case openai.ChatMessageRoleTool:
			bedrockMessage, err := o.openAIMessageToBedrockMessageRoleTool(&msg)
			if err != nil {
				return err
			}
			bedrockReq.Messages = append(bedrockReq.Messages, bedrockMessage)
		default:
			return fmt.Errorf("unexpected role: %s", msg.Type)
		}
	}
	return nil
}

// ResponseHeaders implements [Translator.ResponseHeaders].
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) ResponseHeaders(headers map[string]string) (
	headerMutation *extprocv3.HeaderMutation, err error,
) {
	if o.stream {
		contentType := headers["content-type"]
		if contentType != "application/vnd.amazon.eventstream" {
			return nil, fmt.Errorf("unexpected content-type for streaming: %s", contentType)
		}

		// We need to change the content-type to text/event-stream for streaming responses.
		return &extprocv3.HeaderMutation{
			SetHeaders: []*corev3.HeaderValueOption{
				{Header: &corev3.HeaderValue{Key: "content-type", Value: "text/event-stream"}},
			},
		}, nil
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
	return &openai.ChatCompletionMessageToolCallParam{
		ID: toolUse.ToolUseID,
		Function: openai.ChatCompletionMessageToolCallFunctionParam{
			Name:      toolUse.Name,
			Arguments: toolUse.Input,
		},
		Type: openai.ChatCompletionMessageToolCallTypeFunction,
	}
}

// ResponseBody implements [Translator.ResponseBody].
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) ResponseBody(body io.Reader, endOfStream bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, err error,
) {
	mut := &extprocv3.BodyMutation_Body{}
	if o.stream {
		buf, err := io.ReadAll(body)
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
			oaiEventBytes, err := json.Marshal(oaiEvent)
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

	var bedrockResp awsbedrock.ConverseOutput
	if err := json.NewDecoder(body).Decode(&bedrockResp); err != nil {
		return nil, nil, tokenUsage, fmt.Errorf("failed to unmarshal body: %w", err)
	}

	openAIResp := openai.ChatCompletionResponse{
		Object:  "chat.completion",
		Choices: make([]openai.ChatCompletionResponseChoice, 0, len(bedrockResp.Output.Message.Content)),
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
	for i, output := range bedrockResp.Output.Message.Content {
		choice := openai.ChatCompletionResponseChoice{
			Index: (int64)(i),
			Message: openai.ChatCompletionResponseChoiceMessage{
				Content: output.Text,
				Role:    bedrockResp.Output.Message.Role,
			},
			FinishReason: o.bedrockStopReasonToOpenAIStopReason(bedrockResp.StopReason),
		}
		if toolCall := o.bedrockToolUseToOpenAICalls(output.ToolUse); toolCall != nil {
			choice.Message.ToolCalls = []openai.ChatCompletionMessageToolCallParam{*toolCall}
		}
		openAIResp.Choices = append(openAIResp.Choices, choice)
	}

	if b, err := json.Marshal(openAIResp); err != nil {
		return nil, nil, tokenUsage, fmt.Errorf("failed to marshal body: %w", err)
	} else {
		mut.Body = b
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
				Role:    event.Role,
				Content: &emptyString,
			},
		})
	case event.Delta != nil:
		chunk.Choices = append(chunk.Choices, openai.ChatCompletionResponseChunkChoice{
			Delta: &openai.ChatCompletionResponseChunkChoiceDelta{
				Content: &event.Delta.Text,
			},
		})
	default:
		return chunk, false
	}
	return chunk, true
}
