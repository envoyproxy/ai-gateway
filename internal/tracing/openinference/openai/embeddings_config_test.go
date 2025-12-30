// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

func TestEmbeddingsRecorder_WithConfig_HideInputs(t *testing.T) {
	tests := []struct {
		name          string
		config        *openinference.TraceConfig
		req           *openai.EmbeddingRequest
		reqBody       []byte
		expectedAttrs []attribute.KeyValue
	}{
		{
			name: "hide input value",
			config: &openinference.TraceConfig{
				HideInputs: true,
			},
			req:     basicEmbeddingReq,
			reqBody: basicEmbeddingReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindEmbedding),
				attribute.String(openinference.InputValue, openinference.RedactedValue),
				attribute.String(openinference.EmbeddingInvocationParameters, `{"model":"text-embedding-3-small"}`),
			},
		},
		{
			name: "hide embeddings text",
			config: &openinference.TraceConfig{
				HideEmbeddingsText: true,
			},
			req:     basicEmbeddingReq,
			reqBody: basicEmbeddingReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindEmbedding),
				attribute.String(openinference.InputValue, string(basicEmbeddingReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.EmbeddingInvocationParameters, `{"model":"text-embedding-3-small"}`),
			},
		},
		{
			name: "hide invocation parameters",
			config: &openinference.TraceConfig{
				HideLLMInvocationParameters: true,
			},
			req:     basicEmbeddingReq,
			reqBody: basicEmbeddingReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindEmbedding),
				attribute.String(openinference.InputValue, string(basicEmbeddingReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
				attribute.String(openinference.EmbeddingTextAttribute(0), "How do I reset my password?"),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewEmbeddingsRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, tt.req, tt.reqBody)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
		})
	}
}

func TestEmbeddingsRecorder_WithConfig_HideOutputs(t *testing.T) {
	tests := []struct {
		name           string
		config         *openinference.TraceConfig
		req            *openai.EmbeddingRequest
		reqBody        []byte
		resp           *openai.EmbeddingResponse
		expectedAttrs  []attribute.KeyValue
		expectedStatus trace.Status
	}{
		{
			name: "hide output value",
			config: &openinference.TraceConfig{
				HideOutputs: true,
			},
			req:     basicEmbeddingReq,
			reqBody: basicEmbeddingReqBody,
			resp:    basicEmbeddingResp,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.EmbeddingModelName, "text-embedding-3-small"),
				attribute.Int(openinference.LLMTokenCountPrompt, 10),
				attribute.Int(openinference.LLMTokenCountTotal, 10),
				attribute.String(openinference.OutputValue, openinference.RedactedValue),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name: "hide embeddings vectors",
			config: &openinference.TraceConfig{
				HideEmbeddingsVectors: true,
			},
			req:     basicEmbeddingReq,
			reqBody: basicEmbeddingReqBody,
			resp:    basicEmbeddingResp,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.EmbeddingModelName, "text-embedding-3-small"),
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.Int(openinference.LLMTokenCountPrompt, 10),
				attribute.Int(openinference.LLMTokenCountTotal, 10),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewEmbeddingsRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, tt.req, tt.reqBody)
				recorder.RecordResponse(span, tt.resp)
				return false
			})

			var responseAttrs []attribute.KeyValue
			for _, attr := range actualSpan.Attributes {
				key := string(attr.Key)
				if key == openinference.SpanKind ||
					key == openinference.InputValue ||
					key == openinference.InputMimeType ||
					key == openinference.EmbeddingInvocationParameters ||
					key == openinference.EmbeddingTextAttribute(0) {
					continue
				}
				responseAttrs = append(responseAttrs, attr)
			}

			var outputValueAttr *attribute.KeyValue
			var expectedOutputValueAttr *attribute.KeyValue

			filteredResponseAttrs := []attribute.KeyValue{}
			for _, attr := range responseAttrs {
				if string(attr.Key) == openinference.OutputValue {
					outputValueAttr = &attr
				} else {
					filteredResponseAttrs = append(filteredResponseAttrs, attr)
				}
			}

			filteredExpectedAttrs := []attribute.KeyValue{}
			for _, attr := range tt.expectedAttrs {
				if string(attr.Key) == openinference.OutputValue {
					expectedOutputValueAttr = &attr
				} else {
					filteredExpectedAttrs = append(filteredExpectedAttrs, attr)
				}
			}

			openinference.RequireAttributesEqual(t, filteredExpectedAttrs, filteredResponseAttrs)

			if expectedOutputValueAttr != nil {
				require.NotNil(t, outputValueAttr, "expected output value attribute")
				if expectedOutputValueAttr.Value.AsString() == openinference.RedactedValue {
					require.Equal(t, openinference.RedactedValue, outputValueAttr.Value.AsString())
				} else {
					respJSON, _ := json.Marshal(tt.resp)
					require.JSONEq(t, string(respJSON), outputValueAttr.Value.AsString())
				}
			} else if outputValueAttr != nil {
				respJSON, _ := json.Marshal(tt.resp)
				require.JSONEq(t, string(respJSON), outputValueAttr.Value.AsString())
			}

			require.Empty(t, actualSpan.Events)
			require.Equal(t, tt.expectedStatus, actualSpan.Status)
		})
	}
}

func TestEmbeddingsRecorder_ConfigFromEnvironment(t *testing.T) {
	t.Setenv(openinference.EnvHideInputs, "true")
	t.Setenv(openinference.EnvHideOutputs, "true")

	recorder := NewEmbeddingsRecorderFromEnv()

	reqSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordRequest(span, basicEmbeddingReq, basicEmbeddingReqBody)
		return false
	})

	attrs := make(map[string]attribute.Value)
	for _, kv := range reqSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value
	}
	require.Equal(t, openinference.RedactedValue, attrs[openinference.InputValue].AsString())

	respSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponse(span, basicEmbeddingResp)
		return false
	})

	attrs = make(map[string]attribute.Value)
	for _, kv := range respSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value
	}
	require.Equal(t, openinference.RedactedValue, attrs[openinference.OutputValue].AsString())
}
