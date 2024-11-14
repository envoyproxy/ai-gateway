package translators

import (
	"encoding/json"
	"fmt"
	"log/slog"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/tetratelabs/ai-gateway/internal/apischema/awsbedrock"
	"github.com/tetratelabs/ai-gateway/internal/apischema/openai"
)

// newOpenAIToAWSBedrockTranslator implements [TranslatorFactory] for OpenAI to AWS Bedrock translation.
func newOpenAIToAWSBedrockTranslator(path string, l *slog.Logger) (Translator, error) {
	if path == "/v1/chat/completions" {
		return &openAIToAWSBedrockTranslatorV1ChatCompletion{l: l}, nil
	} else {
		return nil, fmt.Errorf("unsupported path: %s", path)
	}
}

// openAIToAWSBedrockTranslator implements [Translator] for /v1/chat/completions.
type openAIToAWSBedrockTranslatorV1ChatCompletion struct {
	defaultTranslator
	l      *slog.Logger
	stream bool
}

// RequestBody implements [Translator.RequestBody].
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) RequestBody(body *extprocv3.HttpBody) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, modelName string, err error,
) {
	if o.stream {
		panic("TODO")
	}

	var openAIReq openai.ChatCompletionRequest
	if err := json.Unmarshal(body.Body, &openAIReq); err != nil {
		return nil, nil, "", fmt.Errorf("failed to unmarshal body: %w", err)
	}

	var pathTemplate string
	if st := openAIReq.Stream; st != nil {
		o.stream = *st
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
			return nil, nil, "", fmt.Errorf("unexpected role: %s", msg.Role)
		}

		text, ok := msg.Content.(string)
		if ok {
			awsReq.Messages = append(awsReq.Messages, awsbedrock.Message{
				Role:    role,
				Content: []awsbedrock.ContentBlock{{Text: text}},
			})
		} else {
			return nil, nil, "", fmt.Errorf("unexpected content: %v", msg.Content)
		}
	}

	mut := &extprocv3.BodyMutation_Body{}
	if body, err := json.Marshal(awsReq); err != nil {
		return nil, nil, "", fmt.Errorf("failed to marshal body: %w", err)
	} else {
		mut.Body = body
	}
	setContentLength(headerMutation, mut.Body)
	return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, openAIReq.Model, nil
}

// ResponseHeaders implements [Translator.ResponseHeaders].
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) ResponseHeaders(map[string]string) (
	headerMutation *extprocv3.HeaderMutation, err error,
) {
	if o.stream {
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
func (o *openAIToAWSBedrockTranslatorV1ChatCompletion) ResponseBody(body *extprocv3.HttpBody) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, usedToken uint32, err error,
) {
	if o.stream {
		panic("TODO")
	}

	var awsResp awsbedrock.ConverseResponse
	if err := json.Unmarshal(body.Body, &awsResp); err != nil {
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

	mut := &extprocv3.BodyMutation_Body{}
	if body, err := json.Marshal(openAIResp); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to marshal body: %w", err)
	} else {
		mut.Body = body
	}
	headerMutation = &extprocv3.HeaderMutation{}
	setContentLength(headerMutation, mut.Body)
	return headerMutation, &extprocv3.BodyMutation{Mutation: mut}, usedToken, nil
}
