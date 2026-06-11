// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package anthropic

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

func TestCountTokensRecorder_StartParams(t *testing.T) {
	recorder := NewCountTokensRecorderFromEnv()
	spanName, opts := recorder.StartParams(&anthropicschema.MessagesRequest{}, nil)
	actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

	require.Equal(t, "CountTokens", actualSpan.Name)
	require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
}

func TestCountTokensRecorder_RecordRequest(t *testing.T) {
	recorder := NewCountTokensRecorder(&openinference.TraceConfig{})

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordRequest(span, &anthropicschema.MessagesRequest{Model: "claude-opus-4-1"}, []byte(`{"model":"claude-opus-4-1"}`))
		return false
	})

	openinference.RequireAttributesEqual(t, []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemAnthropic),
		attribute.String(openinference.LLMModelName, "claude-opus-4-1"),
		attribute.String(openinference.InputValue, `{"model":"claude-opus-4-1"}`),
		attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
	}, actualSpan.Attributes)
}

func TestCountTokensRecorder_RecordRequestNilRequest(t *testing.T) {
	recorder := NewCountTokensRecorder(&openinference.TraceConfig{})

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordRequest(span, nil, []byte(`{"model":"claude-opus-4-1"}`))
		return false
	})

	require.Empty(t, actualSpan.Attributes)
}

func TestCountTokensRecorder_RecordRequestHideInputs(t *testing.T) {
	recorder := NewCountTokensRecorder(&openinference.TraceConfig{HideInputs: true})

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordRequest(span, &anthropicschema.MessagesRequest{Model: "claude-opus-4-1"}, []byte(`{"model":"claude-opus-4-1"}`))
		return false
	})

	openinference.RequireAttributesEqual(t, []attribute.KeyValue{
		attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
		attribute.String(openinference.LLMSystem, openinference.LLMSystemAnthropic),
		attribute.String(openinference.LLMModelName, "claude-opus-4-1"),
		attribute.String(openinference.InputValue, openinference.RedactedValue),
	}, actualSpan.Attributes)
}

func TestCountTokensRecorder_RecordResponse(t *testing.T) {
	recorder := NewCountTokensRecorder(&openinference.TraceConfig{})

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponse(span, &anthropicschema.CountTokensResponse{InputTokens: 42})
		return false
	})

	openinference.RequireAttributesEqual(t, []attribute.KeyValue{
		attribute.Int(openinference.LLMTokenCountPrompt, 42),
	}, actualSpan.Attributes)
	require.Equal(t, codes.Ok, actualSpan.Status.Code)
}

func TestCountTokensRecorder_RecordResponseNilResponse(t *testing.T) {
	recorder := NewCountTokensRecorder(&openinference.TraceConfig{})

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponse(span, nil)
		return false
	})

	require.Empty(t, actualSpan.Attributes)
	require.Equal(t, codes.Unset, actualSpan.Status.Code)
}

func TestCountTokensRecorder_RecordResponseOnError(t *testing.T) {
	recorder := NewCountTokensRecorder(&openinference.TraceConfig{})

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponseOnError(span, 400, []byte(`{"error":"bad"}`))
		return false
	})

	require.Equal(t, codes.Error, actualSpan.Status.Code)
	require.Contains(t, actualSpan.Status.Description, "400")
}

func TestCountTokensRecorder_RecordResponseChunksNoop(t *testing.T) {
	recorder := NewCountTokensRecorder(&openinference.TraceConfig{})

	require.NotPanics(t, func() {
		testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
			recorder.RecordResponseChunks(span, []*struct{}{{}})
			return false
		})
	})
}
