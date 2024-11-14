package translators

import (
	"encoding/json"
	"fmt"
	"log/slog"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/tetratelabs/ai-gateway/internal/apischema/openai"
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
	l      *slog.Logger
	stream bool
}

// RequestBody implements [RequestBody].
func (o *openAIToOpenAITranslatorV1ChatCompletion) RequestBody(body *extprocv3.HttpBody) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, modelName string, err error,
) {
	var req openai.ChatCompletionRequest
	if err := json.Unmarshal(body.Body, &req); err != nil {
		return nil, nil, "", fmt.Errorf("failed to unmarshal body: %w", err)
	}

	headerMutation = &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{{Header: &corev3.HeaderValue{Key: "host", Value: "api.openai.com"}}},
	}
	return headerMutation, nil, req.Model, nil
}

// ResponseBody implements [Translator.ResponseBody].
func (o *openAIToOpenAITranslatorV1ChatCompletion) ResponseBody(body *extprocv3.HttpBody) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, usedToken uint32, err error,
) {
	if o.stream {
		panic("TODO")
	}
	var resp openai.ChatCompletionResponse
	if err := json.Unmarshal(body.Body, &resp); err != nil {
		return nil, nil, 0, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	usedToken = uint32(resp.Usage.TotalTokens)
	return
}
