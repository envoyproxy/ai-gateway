package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"time"

	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/extproc/router"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

// newOpenAIToOpenAITranslator implements [Factory] for OpenAI to OpenAI translation.
func newOpenAIToOpenAITranslator(path string) (Translator, error) {
	if path == "/v1/chat/completions" {
		return &openAIToOpenAITranslatorV1ChatCompletion{}, nil
	}
	return nil, fmt.Errorf("unsupported path: %s", path)
}

// openAIToOpenAITranslatorV1ChatCompletion implements [Translator] for /v1/chat/completions.
type openAIToOpenAITranslatorV1ChatCompletion struct {
	stream        bool
	buffered      []byte
	bufferingDone bool
	// Add new fields for tracking streaming metrics
	firstTokenSent bool
	requestStart   time.Time
	lastTokenTime  time.Time
	backendName    string
	modelName      string
}

// RequestBody implements [Translator.RequestBody].
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
			// TODO: We can delete this explicit setting of ResponseHeaderMode below as it is the default value we use
			// 	after https://github.com/envoyproxy/envoy/pull/38254 this is released.
			ResponseHeaderMode: extprocv3http.ProcessingMode_SEND,
			ResponseBodyMode:   extprocv3http.ProcessingMode_STREAMED,
		}
	}
	o.requestStart = time.Now()
	return nil, nil, override, nil
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
func (o *openAIToOpenAITranslatorV1ChatCompletion) ResponseBody(respHeaders map[string]string, body io.Reader, _ bool, backendName, modelName string) (
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
		o.backendName = backendName
		o.modelName = modelName
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
		if o.backendName != "" && o.modelName != "" {
			now := time.Now()
			if !o.firstTokenSent {
				o.firstTokenSent = true
				metrics.FirstTokenLatency.WithLabelValues(o.backendName, o.modelName).
					Observe(now.Sub(o.requestStart).Seconds())
			} else {
				// Calculate the time between tokens.
				// Since we are only interested in the time between tokens, and openai streaming is by chunk,
				// we can calculate the time between tokens by the time between the last token and the current token, divided by the number of tokens.
				// And in some cases, the number of tokens can be 0, so we need to check for that.
				div := tokenUsage.OutputTokens
				if div == 0 {
					div = 1
				}
				itl := now.Sub(o.lastTokenTime).Seconds() / float64(div)
				metrics.InterTokenLatency.WithLabelValues(o.backendName, o.modelName).
					Observe(itl)
				o.lastTokenTime = now
			}
		}
	}
}
