package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/extproc/router"
)

// newOpenAIToOpenAITranslator implements [TranslatorFactory] for OpenAI to OpenAI translation.
func newOpenAIToOpenAITranslator(path string) (Translator, error) {
	if path == "/v1/chat/completions" {
		return &openAIToOpenAITranslatorV1ChatCompletion{}, nil
	} else {
		return nil, fmt.Errorf("unsupported path: %s", path)
	}
}

// openAIToOpenAITranslatorV1ChatCompletion implements [Translator] for /v1/chat/completions.
type openAIToOpenAITranslatorV1ChatCompletion struct {
	defaultTranslator
	stream        bool
	buffered      []byte
	bufferingDone bool
}

// RequestBody implements [RequestBody].
func (o *openAIToOpenAITranslatorV1ChatCompletion) RequestBody(body router.RequestBody) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, override *extprocv3http.ProcessingMode, err error,
) {
	req, ok := body.(*openai.ChatCompletionRequest)
	if !ok {
		return nil, nil, nil, fmt.Errorf("unexpected body type: %T", body)
	}
	if req.Stream {
		o.stream = true
		override = &extprocv3http.ProcessingMode{
			ResponseHeaderMode: extprocv3http.ProcessingMode_SEND,
			ResponseBodyMode:   extprocv3http.ProcessingMode_STREAMED,
		}
	}
	return nil, nil, override, nil
}

// ResponseBody implements [Translator.ResponseBody].
func (o *openAIToOpenAITranslatorV1ChatCompletion) ResponseBody(body io.Reader, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, usedToken uint32, err error,
) {
	if o.stream {
		if !o.bufferingDone {
			buf, err := io.ReadAll(body)
			if err != nil {
				return nil, nil, 0, fmt.Errorf("failed to read body: %w", err)
			}
			o.buffered = append(o.buffered, buf...)
			usedToken = o.extractUsageFromBufferEvent()
		}
		return
	}
	var resp openai.ChatCompletionResponse
	if err := json.NewDecoder(body).Decode(&resp); err != nil {
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
