// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package otelgenai

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

func TestChatCompletionRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name         string
		model        string
		expectedName string
	}{
		{name: "names the span operation and model", model: "gpt-5-nano", expectedName: "chat gpt-5-nano"},
		// An empty model must not leave a trailing space in the span name.
		{name: "omits an unknown model", model: "", expectedName: "chat"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewChatCompletionRecorder(NewConfig())
			spanName, opts := r.StartParams(&openai.ChatCompletionRequest{Model: tc.model}, nil)
			require.Equal(t, tc.expectedName, spanName)

			span := testotel.RecordNewSpan(t, spanName, opts...)
			require.Equal(t, tc.expectedName, span.Name)
			// The conventions specify CLIENT for inference spans.
			require.Equal(t, oteltrace.SpanKindClient, span.SpanKind)
		})
	}
}

func TestChatCompletionRecorder_RecordRequest(t *testing.T) {
	r := NewChatCompletionRecorder(NewConfig())

	span := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		r.RecordRequest(span, &openai.ChatCompletionRequest{Model: "gpt-5-nano"}, []byte(`{"model":"gpt-5-nano"}`))
		return false
	})

	testotel.RequireAttributesEqual(t, []attribute.KeyValue{
		attribute.String(OperationName, "chat"),
		attribute.String(RequestModel, "gpt-5-nano"),
	}, span.Attributes)
}

// TestChatCompletionRecorder_RecordRequest_noContentByDefault pins that the raw
// request body is not copied onto the span, which is the whole point of the
// opt-in content default.
func TestChatCompletionRecorder_RecordRequest_noContentByDefault(t *testing.T) {
	const secret = "SENSITIVE-PROMPT-TEXT"
	body := []byte(`{"model":"gpt-5-nano","messages":[{"role":"user","content":"` + secret + `"}]}`)

	r := NewChatCompletionRecorder(NewConfig())
	span := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		r.RecordRequest(span, &openai.ChatCompletionRequest{Model: "gpt-5-nano"}, body)
		return false
	})

	for _, attr := range span.Attributes {
		require.NotContains(t, attr.Value.AsString(), secret, "attribute %s", attr.Key)
	}
}

func TestChatCompletionRecorder_RecordResponse(t *testing.T) {
	r := NewChatCompletionRecorder(NewConfig())

	resp := &openai.ChatCompletionResponse{
		ID:    "chatcmpl-123",
		Model: "gpt-5-nano-2025-08-07",
		Usage: openai.Usage{PromptTokens: 11, CompletionTokens: 7, TotalTokens: 18},
		Choices: []openai.ChatCompletionResponseChoice{
			{FinishReason: "stop"},
		},
	}

	span := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		r.RecordResponse(span, resp)
		return false
	})

	testotel.RequireAttributesEqual(t, []attribute.KeyValue{
		attribute.String(ResponseID, "chatcmpl-123"),
		attribute.String(ResponseModel, "gpt-5-nano-2025-08-07"),
		attribute.Int(UsageInputTokens, 11),
		attribute.Int(UsageOutputTokens, 7),
		attribute.StringSlice(ResponseFinishReasons, []string{"stop"}),
	}, span.Attributes)
	require.Equal(t, codes.Ok, span.Status.Code)
}

// TestChatCompletionRecorder_RecordResponse_omitsAbsent pins that absent values
// are omitted rather than emitted as zero, which would pollute backends with
// meaningless "0 tokens" datapoints.
func TestChatCompletionRecorder_RecordResponse_omitsAbsent(t *testing.T) {
	r := NewChatCompletionRecorder(NewConfig())

	span := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		r.RecordResponse(span, &openai.ChatCompletionResponse{})
		return false
	})

	require.Empty(t, span.Attributes)
	require.Equal(t, codes.Ok, span.Status.Code)
}

func TestChatCompletionRecorder_RecordResponseChunks(t *testing.T) {
	r := NewChatCompletionRecorder(NewConfig())

	finish := openai.ChatCompletionChoicesFinishReason("stop")
	chunks := []*openai.ChatCompletionResponseChunk{
		{ID: "chatcmpl-123", Model: "gpt-5-nano-2025-08-07"},
		{ID: "chatcmpl-123", Model: "gpt-5-nano-2025-08-07", Choices: []openai.ChatCompletionResponseChunkChoice{{FinishReason: finish}}},
		{ID: "chatcmpl-123", Model: "gpt-5-nano-2025-08-07", Usage: &openai.Usage{PromptTokens: 11, CompletionTokens: 7}},
	}

	span := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		r.RecordResponseChunks(span, chunks)
		return false
	})

	// Streaming must produce the same attributes as the equivalent unary response.
	testotel.RequireAttributesEqual(t, []attribute.KeyValue{
		attribute.String(ResponseID, "chatcmpl-123"),
		attribute.String(ResponseModel, "gpt-5-nano-2025-08-07"),
		attribute.Int(UsageInputTokens, 11),
		attribute.Int(UsageOutputTokens, 7),
		attribute.StringSlice(ResponseFinishReasons, []string{"stop"}),
	}, span.Attributes)
	require.Equal(t, codes.Ok, span.Status.Code)
}

func TestChatCompletionRecorder_RecordResponseChunks_boundaries(t *testing.T) {
	tests := []struct {
		name   string
		chunks []*openai.ChatCompletionResponseChunk
	}{
		{name: "zero chunks", chunks: nil},
		{name: "empty slice", chunks: []*openai.ChatCompletionResponseChunk{}},
		{name: "one empty chunk", chunks: []*openai.ChatCompletionResponseChunk{{}}},
		{name: "nil chunk is skipped", chunks: []*openai.ChatCompletionResponseChunk{nil}},
		{name: "nil among valid", chunks: []*openai.ChatCompletionResponseChunk{nil, {ID: "x"}, nil}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewChatCompletionRecorder(NewConfig())
			require.NotPanics(t, func() {
				testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
					r.RecordResponseChunks(span, tc.chunks)
					return false
				})
			})
		})
	}
}

// TestMessageRecorder_RecordResponseChunks_boundaries covers the same
// boundaries for the recorder that folds chunks rather than mapping them
// directly, since that path reaches a different implementation.
func TestMessageRecorder_RecordResponseChunks_boundaries(t *testing.T) {
	tests := []struct {
		name   string
		chunks []*anthropic.MessagesStreamChunk
	}{
		{name: "zero chunks", chunks: nil},
		{name: "empty slice", chunks: []*anthropic.MessagesStreamChunk{}},
		{name: "one empty chunk", chunks: []*anthropic.MessagesStreamChunk{{}}},
		{name: "nil chunk is skipped", chunks: []*anthropic.MessagesStreamChunk{nil}},
		{name: "nil among valid", chunks: []*anthropic.MessagesStreamChunk{nil, {}, nil}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := NewMessageRecorder(NewConfig())
			require.NotPanics(t, func() {
				testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
					r.RecordResponseChunks(span, tc.chunks)
					return false
				})
			})
		})
	}
}

// TestRecorders_operations pins the operation each endpoint reports, since these
// values are what users group and filter spans by.
func TestRecorders_operations(t *testing.T) {
	cfg := NewConfig()

	tests := []struct {
		name              string
		spanName          string
		expectedOperation string
	}{
		{name: "chat", spanName: mustStartName(t, NewChatCompletionRecorder(cfg), &openai.ChatCompletionRequest{Model: "m"}), expectedOperation: "chat m"},
		{name: "text_completion", spanName: mustStartName(t, NewCompletionRecorder(cfg), &openai.CompletionRequest{Model: "m"}), expectedOperation: "text_completion m"},
		{name: "embeddings", spanName: mustStartName(t, NewEmbeddingsRecorder(cfg), &openai.EmbeddingRequest{
			EmbeddingBaseRequest: openai.EmbeddingBaseRequest{Model: "m"},
		}), expectedOperation: "embeddings m"},
		{name: "image_generation", spanName: mustStartName(t, NewImageGenerationRecorder(cfg), &openai.ImageGenerationRequest{Model: "m"}), expectedOperation: "image_generation m"},
		{name: "speech", spanName: mustStartName(t, NewSpeechRecorder(cfg), &openai.SpeechRequest{Model: "m"}), expectedOperation: "speech m"},
		{name: "transcription", spanName: mustStartName(t, NewTranscriptionRecorder(cfg), &openai.TranscriptionRequest{Model: "m"}), expectedOperation: "transcription m"},
		{name: "translation", spanName: mustStartName(t, NewTranslationRecorder(cfg), &openai.TranslationRequest{Model: "m"}), expectedOperation: "translation m"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expectedOperation, tc.spanName)
		})
	}
}

func mustStartName[ReqT, RespT, ChunkT any](t *testing.T, r tracingapi.SpanRecorder[ReqT, RespT, ChunkT], req *ReqT) string {
	t.Helper()
	name, opts := r.StartParams(req, nil)
	require.NotEmpty(t, opts)
	return name
}
