package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
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

// newOpenAIToAWSBedrockTranslator implements [TranslatorFactory] for OpenAI to AWS Bedrock translation.
func newOpenAIToAWSBedrockTranslator(path string) (Translator, error) {
	if path == "/v1/chat/completions" {
		return &openAIToAWSBedrockTranslatorV1ChatCompletion{}, nil
	} else {
		return nil, fmt.Errorf("unsupported path: %s", path)
	}
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
	bedrockReq.InferenceConfig = &awsbedrock.InferenceConfiguration{}
	if openAIReq.MaxTokens != nil {
		bedrockReq.InferenceConfig.MaxTokens = openAIReq.MaxTokens
	}
	if openAIReq.Stop != nil {
		bedrockReq.InferenceConfig.StopSequences = openAIReq.Stop
	}
	if openAIReq.Temperature != nil {
		bedrockReq.InferenceConfig.Temperature = openAIReq.Temperature
	}
	if openAIReq.TopP != nil {
		bedrockReq.InferenceConfig.TopP = openAIReq.TopP
	}
	// Convert Chat Completion messages.
	err = o.OpenAIMessageToBedrockMessage(openAIReq, &bedrockReq)
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
	if body, err := json.Marshal(bedrockReq); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal body: %w", err)
	} else {
		mut.Body = body
	}
	setContentLength(headerMutation, mut.Body)
	return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, override, nil
}

// openAIToolsToBedrockToolConfiguration converts openai ChatCompletion tools to aws bedrock tool configurations
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) openAIToolsToBedrockToolConfiguration(openAIReq *openai.ChatCompletionRequest,
	bedrockReq *awsbedrock.ConverseInput,
) error {
	bedrockReq.ToolConfig = &awsbedrock.ToolConfiguration{}
	tools := make([]*awsbedrock.Tool, 0, len(openAIReq.Tools))
	for _, toolDefinition := range openAIReq.Tools {
		toolType := (string)(toolDefinition.Type)
		tool := &awsbedrock.Tool{
			ToolSpec: &awsbedrock.ToolSpecification{
				Name:        &toolType,
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
			if openAIReq.ToolChoice.(string) == "auto" {
				bedrockReq.ToolConfig.ToolChoice = &awsbedrock.ToolChoice{
					Auto: &awsbedrock.AutoToolChoice{},
				}
			} else {
				bedrockReq.ToolConfig.ToolChoice = &awsbedrock.ToolChoice{
					Any: &awsbedrock.AnyToolChoice{},
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

// OpenAIMessageToBedrockMessage converts openai ChatCompletion messages to aws bedrock messages
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) OpenAIMessageToBedrockMessage(openAIReq *openai.ChatCompletionRequest,
	bedrockReq *awsbedrock.ConverseInput,
) error {
	// Convert Messages.
	bedrockReq.Messages = make([]*awsbedrock.Message, 0, len(openAIReq.Messages))
	for _, msg := range openAIReq.Messages {
		switch msg.Type {
		case openai.ChatMessageRoleUser:
			message := msg.Value.(openai.ChatCompletionUserMessageParam)
			if _, ok := message.Content.Value.(string); ok {
				bedrockReq.Messages = append(bedrockReq.Messages, &awsbedrock.Message{
					Role: msg.Type,
					Content: []*awsbedrock.ContentBlock{
						{Text: ptr.To(message.Content.Value.(string))},
					},
				})
			} else {
				if contents, ok := message.Content.Value.([]openai.ChatCompletionContentPartUserUnionParam); ok {
					chatMessage := &awsbedrock.Message{Role: msg.Type}
					chatMessage.Content = make([]*awsbedrock.ContentBlock, 0, len(contents))
					for _, contentPart := range contents {
						if contentPart.TextContent != nil {
							textContentPart := contentPart.TextContent
							chatMessage.Content = append(chatMessage.Content, &awsbedrock.ContentBlock{
								Text: &textContentPart.Text,
							})
						} else if contentPart.ImageContent != nil {
							imageContentPart := contentPart.ImageContent
							parts := strings.Split(imageContentPart.ImageURL.URL, ",")
							if len(parts) == 2 {
								formatPart := strings.Split(parts[0], ";")[0]
								format := strings.TrimPrefix(formatPart, "data:image/")
								chatMessage.Content = append(chatMessage.Content, &awsbedrock.ContentBlock{
									Image: &awsbedrock.ImageBlock{
										Format: format,
										Source: awsbedrock.ImageSource{
											Bytes: []byte(parts[1]),
										},
									},
								})
							} else {
								return fmt.Errorf("unexpected image data url")
							}
						}
					}
					bedrockReq.Messages = append(bedrockReq.Messages, chatMessage)
				} else {
					return fmt.Errorf("unexpected content type for user message")
				}
			}
		case openai.ChatMessageRoleAssistant:
			message := msg.Value.(openai.ChatCompletionAssistantMessageParam)
			if message.Content.Type == openai.ChatCompletionAssistantMessageParamContentTypeRefusal {
				bedrockReq.Messages = append(bedrockReq.Messages, &awsbedrock.Message{
					Role: msg.Type,
					Content: []*awsbedrock.ContentBlock{
						{Text: message.Content.Refusal},
					},
				})
			} else {
				bedrockReq.Messages = append(bedrockReq.Messages, &awsbedrock.Message{
					Role: msg.Type,
					Content: []*awsbedrock.ContentBlock{
						{Text: message.Content.Text},
					},
				})
			}
		case openai.ChatMessageRoleSystem:
			message := msg.Value.(openai.ChatCompletionSystemMessageParam)
			if bedrockReq.System == nil {
				bedrockReq.System = []*awsbedrock.SystemContentBlock{}
			}

			if _, ok := message.Content.Value.(string); ok {
				bedrockReq.System = append(bedrockReq.System, &awsbedrock.SystemContentBlock{
					Text: message.Content.Value.(string),
				})
			} else {
				if contents, ok := message.Content.Value.([]openai.ChatCompletionContentPartTextParam); ok {
					for _, contentPart := range contents {
						textContentPart := contentPart.Text
						bedrockReq.System = append(bedrockReq.System, &awsbedrock.SystemContentBlock{
							Text: textContentPart,
						})
					}
				} else {
					return fmt.Errorf("unexpected content type for system message")
				}
			}
		case openai.ChatMessageRoleTool:
			message := msg.Value.(openai.ChatCompletionToolMessageParam)
			bedrockReq.Messages = append(bedrockReq.Messages, &awsbedrock.Message{
				// bedrock does not support tool role, merging to the user role
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
			})
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

// ResponseBody implements [Translator.ResponseBody].
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) ResponseBody(body io.Reader, endOfStream bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, usedToken uint32, err error,
) {
	mut := &extprocv3.BodyMutation_Body{}
	if o.stream {
		buf, err := io.ReadAll(body)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("failed to read body: %w", err)
		}
		o.bufferedBody = append(o.bufferedBody, buf...)
		o.extractAmazonEventStreamEvents()

		for i := range o.events {
			event := &o.events[i]
			if usage := event.Usage; usage != nil {
				usedToken = uint32(usage.TotalTokens)
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
		return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, usedToken, nil
	}

	var bedrockResp awsbedrock.ConverseOutput
	if err := json.NewDecoder(body).Decode(&bedrockResp); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to unmarshal body: %w", err)
	}

	usedToken = uint32(bedrockResp.Usage.TotalTokens)

	openAIResp := openai.ChatCompletionResponse{
		Object:  "chat.completion",
		Choices: make([]openai.ChatCompletionResponseChoice, 0, len(bedrockResp.Output.Message.Content)),
	}
	if bedrockResp.Usage != nil {
		openAIResp.Usage = openai.ChatCompletionResponseUsage{
			TotalTokens:      bedrockResp.Usage.TotalTokens,
			PromptTokens:     bedrockResp.Usage.InputTokens,
			CompletionTokens: bedrockResp.Usage.OutputTokens,
		}
		usedToken = uint32(bedrockResp.Usage.TotalTokens)
	}
	for i, output := range bedrockResp.Output.Message.Content {
		choice := openai.ChatCompletionResponseChoice{
			Index: (int64)(i),
			Message: openai.ChatCompletionResponseChoiceMessage{
				Content: output.Text,
				Role:    bedrockResp.Output.Message.Role,
			},
		}
		if bedrockResp.StopReason != nil {
			switch *bedrockResp.StopReason {
			case awsbedrock.StopReasonStopSequence, awsbedrock.StopReasonEndTurn:
				choice.FinishReason = openai.ChatCompletionChoicesFinishReasonStop
			case awsbedrock.StopReasonMaxTokens:
				choice.FinishReason = openai.ChatCompletionChoicesFinishReasonLength
			case awsbedrock.StopReasonContentFiltered:
				choice.FinishReason = openai.ChatCompletionChoicesFinishReasonContentFilter
			case awsbedrock.StopReasonToolUse:
				choice.FinishReason = openai.ChatCompletionChoicesFinishReasonToolCalls
			}
		}
		openAIResp.Choices = append(openAIResp.Choices, choice)
	}

	if body, err := json.Marshal(openAIResp); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to marshal body: %w", err)
	} else {
		mut.Body = body
	}
	headerMutation = &extprocv3.HeaderMutation{}
	setContentLength(headerMutation, mut.Body)
	return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, usedToken, nil
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
