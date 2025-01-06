package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

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

	var awsReq awsbedrock.ConverseRequest
	awsReq.Messages = make([]awsbedrock.Message, 0, len(openAIReq.Messages))
	for _, msg := range openAIReq.Messages {
		var role string
		switch msg.Role {
		case "user", "assistant":
			role = msg.Role
		case "system":
			role = "assistant"
		default:
			return nil, nil, nil, fmt.Errorf("unexpected role: %s", msg.Role)
		}

		contents, ok := msg.Content.([]any)
		if !ok {
			return nil, nil, nil, fmt.Errorf("unexpected content: %[1]T:%[1]v", msg.Content)
		}
		for _, contentAny := range contents {
			content, ok := contentAny.(map[string]any)
			if !ok {
				return nil, nil, nil, fmt.Errorf("unexpected content: %[1]T:%[1]v", contentAny)
			}
			textAny, ok := content["text"]
			if !ok {
				return nil, nil, nil, fmt.Errorf("missing text in content: %v", contents)
			}

			text, ok := textAny.(string)
			if !ok {
				return nil, nil, nil, fmt.Errorf("unexpected text: %[1]T:%[1]v", textAny)
			}
			awsReq.Messages = append(awsReq.Messages, awsbedrock.Message{
				Role:    role,
				Content: []awsbedrock.ContentBlock{{Text: text}},
			})
		}
	}

	mut := &extprocv3.BodyMutation_Body{}
	if body, err := json.Marshal(awsReq); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to marshal body: %w", err)
	} else {
		mut.Body = body
	}
	setContentLength(headerMutation, mut.Body)
	return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, override, nil
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

	var awsResp awsbedrock.ConverseResponse
	if err := json.NewDecoder(body).Decode(&awsResp); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to unmarshal body: %w", err)
	}

	usedToken = uint32(awsResp.Usage.TotalTokens)

	openAIResp := openai.ChatCompletionResponse{
		Usage: openai.ChatCompletionResponseUsage{
			TotalTokens:      awsResp.Usage.TotalTokens,
			PromptTokens:     awsResp.Usage.InputTokens,
			CompletionTokens: awsResp.Usage.OutputTokens,
		},
		Object:  "chat.completion",
		Choices: make([]openai.ChatCompletionResponseChoice, 0, len(awsResp.Output.Message.Content)),
	}

	for _, output := range awsResp.Output.Message.Content {
		t := output.Text
		openAIResp.Choices = append(openAIResp.Choices, openai.ChatCompletionResponseChoice{Message: openai.ChatCompletionResponseChoiceMessage{
			Content: &t,
			Role:    awsResp.Output.Message.Role,
		}})
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
