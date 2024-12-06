package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"

	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// newOpenAIToOpenAITranslator implements [TranslatorFactory] for OpenAI to OpenAI translation.
func newOpenAIToOpenAITranslator(path string, l *slog.Logger) (Translator, error) {
	if path == "/v1/chat/completions" {
		return &openAIToOpenAITranslatorV1ChatCompletion{l: l}, nil
	} else {
		return nil, fmt.Errorf("unsupported path: %s", path)
	}
}

// openAIToOpenAITranslatorV1ChatCompletion implements [Translator] for /v1/chat/completions.
type openAIToOpenAITranslatorV1ChatCompletion struct {
	defaultTranslator
	l             *slog.Logger
	stream        bool
	buffered      []byte
	bufferingDone bool
}

// RequestBody implements [RequestBody].
func (o *openAIToOpenAITranslatorV1ChatCompletion) RequestBody(body *extprocv3.HttpBody) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, override *extprocv3http.ProcessingMode, modelName string, err error,
) {
	var req openai.ChatCompletionRequest
	if err := json.Unmarshal(body.Body, &req); err != nil {
		return nil, nil, nil, "", fmt.Errorf("failed to unmarshal body: %w", err)
	}

	if req.Stream {
		o.stream = true
		override = &extprocv3http.ProcessingMode{
			ResponseHeaderMode: extprocv3http.ProcessingMode_SEND,
			ResponseBodyMode:   extprocv3http.ProcessingMode_STREAMED,
		}
	}
	return nil, nil, override, req.Model, nil
}

// ResponseBody implements [Translator.ResponseBody].
func (o *openAIToOpenAITranslatorV1ChatCompletion) ResponseBody(body *extprocv3.HttpBody) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, usedToken uint32, err error,
) {
	if o.stream {
		if !o.bufferingDone {
			o.buffered = append(o.buffered, body.Body...)
			usedToken = o.extractUsageFromBufferEvent()
		}
		return
	}
	var resp openai.ChatCompletionResponse
	if err := json.Unmarshal(body.Body, &resp); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	usedToken = uint32(resp.Usage.TotalTokens)
	return
}

var dataPrefix = []byte("data: ")

// extractUsageFromBufferEvent extracts the token usage from the buffered event.
// Once the usage is extracted, it returns the number of tokens used, and bufferingDone is set to true.
func (o *openAIToOpenAITranslatorV1ChatCompletion) extractUsageFromBufferEvent() (usedToken uint32) {
	for {
		i := bytes.IndexByte(o.buffered, '\n')
		if i == -1 {
			return 0
		}
		line := o.buffered[:i]
		o.buffered = o.buffered[i+1:]
		if !bytes.HasPrefix(line, dataPrefix) {
			continue
		}
		var event openai.ChatCompletionResponseChunk
		if err := json.Unmarshal(bytes.TrimPrefix(line, dataPrefix), &event); err != nil {
			o.l.Warn("failed to unmarshal the event", slog.Any("error", err))
			continue
		}
		if usage := event.Usage; usage != nil {
			usedToken = uint32(usage.TotalTokens)
			o.bufferingDone = true
			o.buffered = nil
			return
		}
	}
}
