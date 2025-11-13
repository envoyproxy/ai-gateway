// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package cohere

import (
	"encoding/json"
	"testing"

	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"

	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

func TestRerankRecorder_RecordRequest(t *testing.T) {
	req := &cohereschema.RerankV2Request{
		Model:     "rerank-english-v3",
		Query:     "reset password",
		TopN:      ptr(2),
		Documents: []string{"d1", "d2"},
	}
	reqBody, _ := json.Marshal(req)

	recorder := NewRerankRecorder(&openinference.TraceConfig{})

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordRequest(span, req, reqBody)
		return false
	})

	expected := []attribute.KeyValue{
		attribute.String(openinference.LLMSystem, openinference.LLMSystemCohere),
		attribute.String(openinference.SpanKind, openinference.SpanKindReranker),
		attribute.String(openinference.RerankerModelName, "rerank-english-v3"),
		attribute.Int(openinference.RerankerTopK, 2),
		attribute.String(openinference.RerankerQuery, "reset password"),
		attribute.String(openinference.InputValue, string(reqBody)),
		attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
		attribute.String(openinference.RerankerInputDocumentAttribute(0, openinference.DocumentContent), "d1"),
		attribute.String(openinference.RerankerInputDocumentAttribute(1, openinference.DocumentContent), "d2"),
	}
	openinference.RequireAttributesEqual(t, expected, actualSpan.Attributes)
}

func TestRerankRecorder_RecordResponse(t *testing.T) {
	resp := &cohereschema.RerankV2Response{
		Results: []*cohereschema.RerankV2Result{{Index: 1, RelevanceScore: 0.9}},
		Meta: &cohereschema.RerankV2Meta{
			Tokens: &cohereschema.RerankV2Tokens{
				InputTokens:  fptr(25),
				OutputTokens: fptr(0),
			},
		},
	}

	recorder := NewRerankRecorder(&openinference.TraceConfig{})

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponse(span, resp)
		return false
	})

	respJSON, _ := json.Marshal(resp)
	expected := []attribute.KeyValue{
		attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
		attribute.Float64(openinference.RerankerOutputDocumentAttribute(0, openinference.DocumentScore), 0.9),
		attribute.Int(openinference.LLMTokenCountPrompt, 25),
		attribute.Int(openinference.LLMTokenCountTotal, 25),
		attribute.String(openinference.OutputValue, string(respJSON)),
	}
	openinference.RequireAttributesEqual(t, expected, actualSpan.Attributes)
}

func ptr[T any](v T) *T { return &v }
func fptr(v int) *float64 {
	f := float64(v)
	return &f
}
