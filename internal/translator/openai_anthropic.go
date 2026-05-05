// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.
package translator

import (
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/redaction"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

const (
	DefaultAnthropicVersion = "2023-06-01"
	anthropicBackendError   = "AnthropicBackendError"
)

func NewChatCompletionOpenAIToAnthropicTranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) OpenAIChatCompletionTranslator {
	return &openAIToAnthropicTranslatorV1ChatCompletion{
		apiVersion:        apiVersion,
		modelNameOverride: modelNameOverride,
	}
}

type openAIToAnthropicTranslatorV1ChatCompletion struct {
	apiVersion        string
	modelNameOverride internalapi.ModelNameOverride
	streamParser      *anthropicStreamParser
	requestModel      internalapi.RequestModel
	debugLogEnabled   bool
	enableRedaction   bool
	logger            *slog.Logger
}

func (o *openAIToAnthropicTranslatorV1ChatCompletion) RequestBody(_ []byte, openAIReq *openai.ChatCompletionRequest, _ bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	params, err := buildAnthropicParams(openAIReq, "Anthropic", o.modelNameOverride)
	if err != nil {
		return
	}
	o.requestModel = openAIReq.Model

	if o.modelNameOverride != "" {
		o.requestModel = o.modelNameOverride
	}

	newBody, err = json.Marshal(params)
	if err != nil {
		return
	}

	if openAIReq.Stream {
		newBody, err = sjson.SetBytesOptions(newBody, "stream", true, sjsonOptions)
		if err != nil {
			return
		}

		o.streamParser = newAnthropicStreamParser(o.requestModel)
	}

	anthropicVersion := DefaultAnthropicVersion
	if o.apiVersion != "" {
		anthropicVersion = o.apiVersion
	}

	newHeaders = []internalapi.Header{
		{pathHeaderName, "/v1/messages"},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
		{"anthropic-version", anthropicVersion},
	}

	return
}

func (o *openAIToAnthropicTranslatorV1ChatCompletion) ResponseError(respHeaders map[string]string, body io.Reader) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	var openaiError openai.Error
	var decodeErr error
	if v, ok := respHeaders[contentTypeHeaderName]; ok && strings.Contains(v, jsonContentType) {
		var anthropicErr anthropic.ErrorResponse
		if decodeErr = json.NewDecoder(body).Decode(&anthropicErr); decodeErr != nil {
			return nil, nil, fmt.Errorf("failed to unmarshal JSON error body: %w", decodeErr)
		}

		openaiError = openai.Error{
			Type: "error",
			Error: openai.ErrorType{
				Type:    anthropicErr.Error.Type,
				Message: anthropicErr.Error.Message,
				Code:    &statusCode,
			},
		}
	} else {
		var buf []byte

		buf, decodeErr = io.ReadAll(body)
		if decodeErr != nil {
			return nil, nil, fmt.Errorf("failed to read raw error body: %w", decodeErr)
		}

		openaiError = openai.Error{
			Type: "error",
			Error: openai.ErrorType{
				Type:    anthropicBackendError,
				Message: string(buf),
				Code:    &statusCode,
			},
		}
	}

	newBody, err = json.Marshal(openaiError)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to marshal OpenAI error body: %w", err)
	}

	newHeaders = []internalapi.Header{
		{contentTypeHeaderName, jsonContentType},
		{contentLengthHeaderName, strconv.Itoa(len(newBody))},
	}

	return
}

func (o *openAIToAnthropicTranslatorV1ChatCompletion) SetRedactionConfig(debugLogEnabled, enableRedaction bool, logger *slog.Logger) {
	o.debugLogEnabled = debugLogEnabled
	o.enableRedaction = enableRedaction
	o.logger = logger
}

func (o *openAIToAnthropicTranslatorV1ChatCompletion) RedactBody(resp *openai.ChatCompletionResponse) *openai.ChatCompletionResponse {
	if resp == nil {
		return nil
	}

	redacted := *resp

	if len(resp.Choices) > 0 {
		redacted.Choices = make([]openai.ChatCompletionResponseChoice, len(resp.Choices))

		for i := range resp.Choices {
			redactedChoice := resp.Choices[i]
			redactedChoice.Message = redactAnthropicResponseMessage(&resp.Choices[i].Message)
			redacted.Choices[i] = redactedChoice
		}
	}

	return &redacted
}

func redactAnthropicResponseMessage(msg *openai.ChatCompletionResponseChoiceMessage) openai.ChatCompletionResponseChoiceMessage {
	redactedMsg := *msg

	if msg.Content != nil {
		redactedContent := redaction.RedactString(*msg.Content)
		redactedMsg.Content = &redactedContent
	}

	if len(msg.ToolCalls) > 0 {
		redactedMsg.ToolCalls = make([]openai.ChatCompletionMessageToolCallParam, len(msg.ToolCalls))
		for i, tc := range msg.ToolCalls {
			redactedToolCall := tc
			redactedToolCall.Function.Name = redaction.RedactString(tc.Function.Name)
			redactedToolCall.Function.Arguments = redaction.RedactString(tc.Function.Arguments)
			redactedMsg.ToolCalls[i] = redactedToolCall
		}
	}

	if msg.Audio != nil {
		redactedAudio := *msg.Audio
		redactedAudio.Data = redaction.RedactString(msg.Audio.Data)
		redactedAudio.Transcript = redaction.RedactString(msg.Audio.Transcript)
		redactedMsg.Audio = &redactedAudio
	}

	if msg.ReasoningContent != nil {
		redactedMsg.ReasoningContent = redactReasoningContent(msg.ReasoningContent)
	}

	return redactedMsg
}

func (o *openAIToAnthropicTranslatorV1ChatCompletion) ResponseHeaders(_ map[string]string) (
	newHeaders []internalapi.Header, err error,
) {
	if o.streamParser != nil {
		newHeaders = []internalapi.Header{{contentTypeHeaderName, eventStreamContentType}}
	}

	return
}

func (o *openAIToAnthropicTranslatorV1ChatCompletion) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool, span tracingapi.ChatCompletionSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	if o.streamParser != nil {
		return o.streamParser.Process(body, endOfStream, span)
	}

	var anthropicResp anthropic.Message

	if err = json.NewDecoder(body).Decode(&anthropicResp); err != nil {
		return nil, nil, tokenUsage, "", fmt.Errorf("failed to unmarshal body: %w", err)
	}

	responseModel = o.requestModel

	if anthropicResp.Model != "" {
		responseModel = anthropicResp.Model
	}

	openAIResp, tokenUsage, err := messageToChatCompletion(&anthropicResp, responseModel)
	if err != nil {
		return nil, nil, metrics.TokenUsage{}, "", err
	}

	if o.debugLogEnabled && o.enableRedaction && o.logger != nil {
		redactedResp := o.RedactBody(openAIResp)
		if jsonBody, marshalErr := json.Marshal(redactedResp); marshalErr == nil {
			o.logger.Debug("response body processing", slog.Any("response", string(jsonBody)))
		}
	}

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
