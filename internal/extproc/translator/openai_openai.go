// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/tidwall/sjson"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// NewChatCompletionOpenAIToOpenAITranslator implements [Factory] for OpenAI to OpenAI translation.
func NewChatCompletionOpenAIToOpenAITranslator(modelNameOverride string) OpenAIChatCompletionTranslator {
	return &openAIToOpenAITranslatorV1ChatCompletion{modelNameOverride: modelNameOverride}
}

// openAIToOpenAITranslatorV1ChatCompletion implements [Translator] for /v1/chat/completions.
type openAIToOpenAITranslatorV1ChatCompletion struct {
	modelNameOverride string
	stream            bool
	buffered          []byte
	bufferingDone     bool
}

// RequestBody implements [OpenAIChatCompletionTranslator.RequestBody].
func (o *openAIToOpenAITranslatorV1ChatCompletion) RequestBody(raw []byte, req *openai.ChatCompletionRequest, onRetry bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	if req.Stream {
		o.stream = true
	}
	var newBody []byte
	if o.modelNameOverride != "" {
		// If modelName is set we override the model to be used for the request.
		out, err := sjson.SetBytesOptions(raw, "model", o.modelNameOverride, &sjson.Options{
			Optimistic:     true,
			ReplaceInPlace: true,
		})
		if err != nil {
			return nil, nil, fmt.Errorf("failed to set model name: %w", err)
		}
		newBody = out
	}
	// On retry, the path might have changed to a different provider. So, this will ensure that the path is always set to OpenAI.
	if onRetry {
		headerMutation = &extprocv3.HeaderMutation{
			SetHeaders: []*corev3.HeaderValueOption{
				{Header: &corev3.HeaderValue{
					Key:      ":path",
					RawValue: []byte("/v1/chat/completions"),
				}},
				{Header: &corev3.HeaderValue{
					Key:      "content-length",
					RawValue: []byte(strconv.Itoa(len(raw))),
				}},
			},
		}
		newBody = raw
	}

	if len(newBody) > 0 {
		bodyMutation = &extprocv3.BodyMutation{
			Mutation: &extprocv3.BodyMutation_Body{Body: newBody},
		}
	}

	return
}

// ResponseError implements [Translator.ResponseError]
// For OpenAI based backend we return the OpenAI error type as is.
// If connection fails the error body is translated to OpenAI error type for events such as HTTP 503 or 504.
func (o *openAIToOpenAITranslatorV1ChatCompletion) ResponseError(respHeaders map[string]string, body io.Reader) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	statusCode := respHeaders[statusHeaderName]
	if v, ok := respHeaders[contentTypeHeaderName]; ok && v != jsonContentType {
		var openaiError openai.Error
		buf, err := io.ReadAll(body)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to read error body: %w", err)
		}
		openaiError = openai.Error{
			Type: "error",
			Error: openai.ErrorType{
				Type:    openAIBackendError,
				Message: string(buf),
				Code:    &statusCode,
			},
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
	return nil, nil, nil
}

// ResponseHeaders implements [Translator.ResponseHeaders].
func (o *openAIToOpenAITranslatorV1ChatCompletion) ResponseHeaders(map[string]string) (headerMutation *extprocv3.HeaderMutation, err error) {
	return nil, nil
}

// ResponseBody implements [Translator.ResponseBody].
func (o *openAIToOpenAITranslatorV1ChatCompletion) ResponseBody(respHeaders map[string]string, body io.Reader, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, tokenUsage LLMTokenUsage, err error,
) {
	if v, ok := respHeaders[statusHeaderName]; ok {
		if v, err := strconv.Atoi(v); err == nil {
			if !isGoodStatusCode(v) {
				headerMutation, bodyMutation, err = o.ResponseError(respHeaders, body)
				return headerMutation, bodyMutation, LLMTokenUsage{}, err
			}
		}
	}
	if o.stream {
		if !o.bufferingDone {
			buf, err := io.ReadAll(body)
			if err != nil {
				return nil, nil, tokenUsage, fmt.Errorf("failed to read body: %w", err)
			}
			o.buffered = append(o.buffered, buf...)
			tokenUsage = o.extractUsageFromBufferEvent()
		}
		return
	}
	var resp openai.ChatCompletionResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
		return nil, nil, tokenUsage, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	tokenUsage = LLMTokenUsage{
		InputTokens:  uint32(resp.Usage.PromptTokens),     //nolint:gosec
		OutputTokens: uint32(resp.Usage.CompletionTokens), //nolint:gosec
		TotalTokens:  uint32(resp.Usage.TotalTokens),      //nolint:gosec
	}
	return
}

var dataPrefix = []byte("data: ")

// extractUsageFromBufferEvent extracts the token usage from the buffered event.
// Once the usage is extracted, it returns the number of tokens used, and bufferingDone is set to true.
func (o *openAIToOpenAITranslatorV1ChatCompletion) extractUsageFromBufferEvent() (tokenUsage LLMTokenUsage) {
	for {
		i := bytes.IndexByte(o.buffered, '\n')
		if i == -1 {
			return
		}
		line := o.buffered[:i]
		o.buffered = o.buffered[i+1:]
		if !bytes.HasPrefix(line, dataPrefix) {
			continue
		}
		var event openai.ChatCompletionResponseChunk
		if err := json.Unmarshal(bytes.TrimPrefix(line, dataPrefix), &event); err != nil {
			continue
		}
		if usage := event.Usage; usage != nil {
			tokenUsage = LLMTokenUsage{
				InputTokens:  uint32(usage.PromptTokens),     //nolint:gosec
				OutputTokens: uint32(usage.CompletionTokens), //nolint:gosec
				TotalTokens:  uint32(usage.TotalTokens),      //nolint:gosec
			}
			o.bufferingDone = true
			o.buffered = nil
			return
		}
	}
}
