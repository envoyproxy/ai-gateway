// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"encoding/json"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/tokenize"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

// startOptsTokenize sets trace.SpanKindInternal as that's the span kind used in
// OpenInference.
var startOptsTokenize = []trace.SpanStartOption{trace.WithSpanKind(trace.SpanKindInternal)}

// TokenizeRecorder implements recorders for OpenInference tokenize spans.
type TokenizeRecorder struct {
	traceConfig                         *openinference.TraceConfig
	tracing.NoopChunkRecorder[struct{}] // Tokenize operations don't have streaming chunks
}

// NewTokenizeRecorderFromEnv creates an api.TokenizeRecorder
// from environment variables using the OpenInference configuration specification.
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewTokenizeRecorderFromEnv() tracing.TokenizeRecorder {
	return NewTokenizeRecorder(nil)
}

// NewTokenizeRecorder creates a tracing.TokenizeRecorder with the
// given config using the OpenInference configuration specification.
//
// Parameters:
//   - config: configuration for redaction. Defaults to NewTraceConfigFromEnv().
//
// See: https://github.com/Arize-ai/openinference/blob/main/spec/configuration.md
func NewTokenizeRecorder(config *openinference.TraceConfig) tracing.TokenizeRecorder {
	if config == nil {
		config = openinference.NewTraceConfigFromEnv()
	}
	return &TokenizeRecorder{traceConfig: config}
}

// StartParams implements the same method as defined in tracing.TokenizeRecorder.
func (r *TokenizeRecorder) StartParams(*tokenize.TokenizeRequestUnion, []byte) (spanName string, opts []trace.SpanStartOption) {
	return "Tokenize", startOptsTokenize
}

// RecordRequest implements the same method as defined in tracing.TokenizeRecorder.
func (r *TokenizeRecorder) RecordRequest(span trace.Span, tokenizeReq *tokenize.TokenizeRequestUnion, body []byte) {
	span.SetAttributes(buildTokenizeRequestAttributes(tokenizeReq, string(body), r.traceConfig)...)
}

// RecordResponseOnError implements the same method as defined in tracing.TokenizeRecorder.
func (r *TokenizeRecorder) RecordResponseOnError(span trace.Span, statusCode int, body []byte) {
	openinference.RecordResponseError(span, statusCode, string(body))
}

// RecordResponse implements the same method as defined in tracing.TokenizeRecorder.
func (r *TokenizeRecorder) RecordResponse(span trace.Span, resp *tokenize.TokenizeResponse) {
	// Set output attributes.
	var attrs []attribute.KeyValue
	attrs = buildTokenizeResponseAttributes(resp, r.traceConfig)

	bodyString := openinference.RedactedValue
	if !r.traceConfig.HideOutputs {
		marshaled, err := json.Marshal(resp)
		if err == nil {
			bodyString = string(marshaled)
		}
	}
	attrs = append(attrs, attribute.String(openinference.OutputValue, bodyString))
	span.SetAttributes(attrs...)
	span.SetStatus(codes.Ok, "")
}

// buildTokenizeRequestAttributes builds OpenInference attributes from a tokenize request.
func buildTokenizeRequestAttributes(req *tokenize.TokenizeRequestUnion, body string, config *openinference.TraceConfig) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	// Set span kind to LLM since tokenization is an LLM operation
	attrs = append(attrs, attribute.String(openinference.SpanKind, openinference.SpanKindLLM))
	attrs = append(attrs, attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI))

	// Extract model name from the union
	var model string
	if req.TokenizeCompletionRequest != nil {
		model = req.TokenizeCompletionRequest.Model
	} else if req.TokenizeChatRequest != nil {
		model = req.TokenizeChatRequest.Model
	}
	if model != "" {
		attrs = append(attrs, attribute.String(openinference.LLMModelName, model))
	}

	// Add input value if not hidden
	if !config.HideInputs {
		attrs = append(attrs, attribute.String(openinference.InputValue, body))
		attrs = append(attrs, attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON))
	}

	// Add tokenization-specific attributes
	if req.TokenizeCompletionRequest != nil {
		attrs = append(attrs, attribute.String("tokenize.request_type", "completion"))
		attrs = append(attrs, attribute.Bool("tokenize.add_special_tokens", req.TokenizeCompletionRequest.AddSpecialTokens))
		if req.TokenizeCompletionRequest.ReturnTokenStrs != nil {
			attrs = append(attrs, attribute.Bool("tokenize.return_token_strs", *req.TokenizeCompletionRequest.ReturnTokenStrs))
		}
	} else if req.TokenizeChatRequest != nil {
		attrs = append(attrs, attribute.String("tokenize.request_type", "chat"))
		attrs = append(attrs, attribute.Bool("tokenize.add_generation_prompt", req.TokenizeChatRequest.AddGenerationPrompt))
		attrs = append(attrs, attribute.Bool("tokenize.continue_final_message", req.TokenizeChatRequest.ContinueFinalMessage))
		attrs = append(attrs, attribute.Bool("tokenize.add_special_tokens", req.TokenizeChatRequest.AddSpecialTokens))
		if req.TokenizeChatRequest.ReturnTokenStrs != nil {
			attrs = append(attrs, attribute.Bool("tokenize.return_token_strs", *req.TokenizeChatRequest.ReturnTokenStrs))
		}
		if len(req.TokenizeChatRequest.Messages) > 0 {
			attrs = append(attrs, attribute.Int("tokenize.message_count", len(req.TokenizeChatRequest.Messages)))
		}
	}

	return attrs
}

// buildTokenizeResponseAttributes builds OpenInference attributes from a tokenize response.
func buildTokenizeResponseAttributes(resp *tokenize.TokenizeResponse, _ *openinference.TraceConfig) []attribute.KeyValue {
	var attrs []attribute.KeyValue

	// Add tokenization results
	attrs = append(attrs, attribute.Int("tokenize.token_count", resp.Count))
	if resp.MaxModelLen > 0 {
		attrs = append(attrs, attribute.Int("tokenize.max_model_len", resp.MaxModelLen))
	}
	if len(resp.Tokens) > 0 {
		attrs = append(attrs, attribute.Int("tokenize.tokens_returned", len(resp.Tokens)))
	}
	if len(resp.TokenStrs) > 0 {
		attrs = append(attrs, attribute.Int("tokenize.token_strings_returned", len(resp.TokenStrs)))
	}

	// Output MIME type
	attrs = append(attrs, attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON))

	return attrs
}
