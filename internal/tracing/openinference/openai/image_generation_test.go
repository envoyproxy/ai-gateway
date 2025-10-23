// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"encoding/json"
	"strconv"
	"testing"

	openaisdk "github.com/openai/openai-go/v2"
	openaiparam "github.com/openai/openai-go/v2/packages/param"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
)

var (
	// Test data constants following chat completion pattern
	basicImageReq = &openaisdk.ImageGenerateParams{
		Model:          openaisdk.ImageModelGPTImage1,
		Prompt:         "a hummingbird",
		Size:           openaisdk.ImageGenerateParamsSize1024x1024,
		Quality:        openaisdk.ImageGenerateParamsQualityHigh,
		ResponseFormat: openaisdk.ImageGenerateParamsResponseFormatB64JSON,
		N:              openaiparam.NewOpt[int64](1),
	}
	basicImageReqBody = mustJSON(basicImageReq)

	basicImageResp = &openaisdk.ImagesResponse{
		Data: []openaisdk.Image{{URL: "https://example.com/img.png"}},
		Size: openaisdk.ImagesResponseSize1024x1024,
		Usage: openaisdk.ImagesResponseUsage{
			InputTokens:  8,
			OutputTokens: 1056,
			TotalTokens:  1064,
		},
	}
	basicImageRespBody = mustJSON(basicImageResp)
)

func TestImageGenerationRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name             string
		req              *openaisdk.ImageGenerateParams
		reqBody          []byte
		expectedSpanName string
	}{
		{
			name:             "basic request",
			req:              basicImageReq,
			reqBody:          basicImageReqBody,
			expectedSpanName: "ImageGeneration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewImageGenerationRecorder(nil)

			spanName, opts := recorder.StartParams(tt.req, tt.reqBody)
			actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
		})
	}
}

func TestImageGenerationRecorder_RecordRequest(t *testing.T) {
	tests := []struct {
		name          string
		req           *openaisdk.ImageGenerateParams
		reqBody       []byte
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:    "basic request",
			req:     basicImageReq,
			reqBody: basicImageReqBody,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
				attribute.String(openinference.LLMModelName, openaisdk.ImageModelGPTImage1),
				attribute.String(openinference.InputValue, string(basicImageReqBody)),
				attribute.String(openinference.InputMimeType, openinference.MimeTypeJSON),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewImageGenerationRecorder(nil)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, tt.req, tt.reqBody)
				return false
			})

			// Check that key attributes are present
			attrs := attributesToMap(actualSpan.Attributes)
			require.Equal(t, openinference.SpanKindLLM, attrs[openinference.SpanKind])
			require.Equal(t, openinference.LLMSystemOpenAI, attrs[openinference.LLMSystem])
			require.Equal(t, openaisdk.ImageModelGPTImage1, attrs[openinference.LLMModelName])
			require.Equal(t, string(basicImageReqBody), attrs[openinference.InputValue])
			require.Equal(t, openinference.MimeTypeJSON, attrs[openinference.InputMimeType])
		})
	}
}

func TestImageGenerationRecorder_RecordResponse(t *testing.T) {
	tests := []struct {
		name           string
		respBody       []byte
		expectedAttrs  []attribute.KeyValue
		expectedStatus trace.Status
	}{
		{
			name:           "successful response",
			respBody:       basicImageRespBody,
			expectedAttrs:  []attribute.KeyValue{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewImageGenerationRecorder(nil)

			resp := &openaisdk.ImagesResponse{}
			err := json.Unmarshal(tt.respBody, resp)
			require.NoError(t, err)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, resp)
				return false
			})

			// Check that key attributes are present
			_ = attributesToMap(actualSpan.Attributes)
			require.Equal(t, trace.Status{Code: codes.Ok, Description: ""}, actualSpan.Status)
		})
	}
}

func TestImageGenerationRecorder_RecordResponseOnError(t *testing.T) {
	recorder := NewImageGenerationRecorder(nil)

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponseOnError(span, 400, []byte(`{"error":{"message":"Invalid request","type":"invalid_request_error"}}`))
		return false
	})

	require.Equal(t, trace.Status{
		Code:        codes.Error,
		Description: `Error code: 400 - {"error":{"message":"Invalid request","type":"invalid_request_error"}}`,
	}, actualSpan.Status)
}

// attributesToMap converts attribute KeyValue to a simple map for assertions.
func attributesToMap(kvs []attribute.KeyValue) map[string]string {
	m := make(map[string]string, len(kvs))
	for _, kv := range kvs {
		switch kv.Value.Type() {
		case attribute.STRING:
			m[string(kv.Key)] = kv.Value.AsString()
		case attribute.BOOL:
			m[string(kv.Key)] = strconv.FormatBool(kv.Value.AsBool())
		case attribute.INT64:
			m[string(kv.Key)] = strconv.FormatInt(kv.Value.AsInt64(), 10)
		case attribute.FLOAT64:
			m[string(kv.Key)] = strconv.FormatFloat(kv.Value.AsFloat64(), 'f', -1, 64)
		default:
			m[string(kv.Key)] = kv.Value.AsString()
		}
	}
	return m
}

var _ tracing.ImageGenerationRecorder = (*ImageGenerationRecorder)(nil)
