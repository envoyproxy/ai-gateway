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
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/internal/tracing/openinference"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// Interface conformance checks.
var (
	_ tracingapi.CreateFileRecorder          = (*CreateFileRecorder)(nil)
	_ tracingapi.RetrieveFileRecorder        = (*RetrieveFileRecorder)(nil)
	_ tracingapi.RetrieveFileContentRecorder = (*RetrieveFileContentRecorder)(nil)
	_ tracingapi.DeleteFileRecorder          = (*DeleteFileRecorder)(nil)
)

var (
	basicFileObject = &openai.FileObject{
		ID:       "file-abc123",
		Filename: "training.jsonl",
		Purpose:  openai.FileObjectPurposeFineTune,
		Status:   openai.FileObjectStatusProcessed,
	}

	basicFileDeleted = &openai.FileDeleted{
		ID:      "file-abc123",
		Deleted: true,
		Object:  "file",
	}
)

// ---------------------------------------------------------------------------
// CreateFileRecorder
// ---------------------------------------------------------------------------

func TestCreateFileRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name             string
		expectedSpanName string
	}{
		{
			name:             "basic request",
			expectedSpanName: "CreateFile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewCreateFileRecorderFromEnv()

			spanName, opts := recorder.StartParams(nil, nil)
			actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
		})
	}
}

func TestCreateFileRecorder_RecordRequest(t *testing.T) {
	tests := []struct {
		name          string
		req           *openai.FileNewParams
		reqBody       []byte
		expectedAttrs []attribute.KeyValue
	}{
		{
			name:    "basic request",
			req:     nil,
			reqBody: nil,
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewCreateFileRecorderFromEnv()

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, tt.req, tt.reqBody)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Empty(t, actualSpan.Events)
			require.Equal(t, trace.Status{Code: codes.Unset, Description: ""}, actualSpan.Status)
		})
	}
}

func TestCreateFileRecorder_RecordResponse(t *testing.T) {
	tests := []struct {
		name           string
		resp           *openai.FileObject
		config         *openinference.TraceConfig
		expectedAttrs  []attribute.KeyValue
		expectedStatus trace.Status
	}{
		{
			name:   "successful response",
			resp:   basicFileObject,
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.String("output.file_id", basicFileObject.ID),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:           "hidden outputs",
			resp:           basicFileObject,
			config:         &openinference.TraceConfig{HideOutputs: true},
			expectedAttrs:  []attribute.KeyValue{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:           "nil response",
			resp:           nil,
			config:         &openinference.TraceConfig{},
			expectedAttrs:  []attribute.KeyValue{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewCreateFileRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, tt.resp)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Equal(t, tt.expectedStatus, actualSpan.Status)
		})
	}
}

func TestCreateFileRecorder_RecordResponseOnError(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		errorBody      []byte
		expectedStatus trace.Status
		expectedEvents int
	}{
		{
			name:       "400 bad request error",
			statusCode: 400,
			errorBody:  []byte(`{"error":{"message":"Invalid file","type":"invalid_request_error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 400 - {\"error\":{\"message\":\"Invalid file\",\"type\":\"invalid_request_error\"}}",
			},
			expectedEvents: 1,
		},
		{
			name:       "500 internal server error",
			statusCode: 500,
			errorBody:  []byte(`{"error":{"message":"Internal server error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 500 - {\"error\":{\"message\":\"Internal server error\"}}",
			},
			expectedEvents: 1,
		},
		{
			name:       "413 payload too large",
			statusCode: 413,
			errorBody:  []byte(`{"error":{"message":"File too large","type":"invalid_request_error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 413 - {\"error\":{\"message\":\"File too large\",\"type\":\"invalid_request_error\"}}",
			},
			expectedEvents: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewCreateFileRecorderFromEnv()

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponseOnError(span, tt.statusCode, tt.errorBody)
				return false
			})

			require.Equal(t, tt.expectedStatus, actualSpan.Status)
			require.Len(t, actualSpan.Events, tt.expectedEvents)
			if tt.expectedEvents > 0 {
				require.Equal(t, "exception", actualSpan.Events[0].Name)
			}
		})
	}
}

func TestCreateFileRecorder_NewCreateFileRecorder_NilConfig(t *testing.T) {
	recorder := NewCreateFileRecorder(nil)

	require.NotNil(t, recorder)
	_, ok := recorder.(*CreateFileRecorder)
	require.True(t, ok)
}

// ---------------------------------------------------------------------------
// RetrieveFileRecorder
// ---------------------------------------------------------------------------

func TestRetrieveFileRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name             string
		expectedSpanName string
	}{
		{
			name:             "basic request",
			expectedSpanName: "RetrieveFile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewRetrieveFileRecorderFromEnv()

			spanName, opts := recorder.StartParams(nil, nil)
			actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
		})
	}
}

func TestRetrieveFileRecorder_RecordRequest(t *testing.T) {
	tests := []struct {
		name          string
		expectedAttrs []attribute.KeyValue
	}{
		{
			name: "basic request",
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewRetrieveFileRecorderFromEnv()

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, nil, nil)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Empty(t, actualSpan.Events)
			require.Equal(t, trace.Status{Code: codes.Unset, Description: ""}, actualSpan.Status)
		})
	}
}

func TestRetrieveFileRecorder_RecordResponse(t *testing.T) {
	tests := []struct {
		name           string
		resp           *openai.FileObject
		config         *openinference.TraceConfig
		expectedAttrs  []attribute.KeyValue
		expectedStatus trace.Status
	}{
		{
			name:   "successful response",
			resp:   basicFileObject,
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.String("output.file_id", basicFileObject.ID),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:           "hidden outputs",
			resp:           basicFileObject,
			config:         &openinference.TraceConfig{HideOutputs: true},
			expectedAttrs:  []attribute.KeyValue{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:           "nil response",
			resp:           nil,
			config:         &openinference.TraceConfig{},
			expectedAttrs:  []attribute.KeyValue{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewRetrieveFileRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, tt.resp)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Equal(t, tt.expectedStatus, actualSpan.Status)
		})
	}
}

func TestRetrieveFileRecorder_RecordResponseOnError(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		errorBody      []byte
		expectedStatus trace.Status
		expectedEvents int
	}{
		{
			name:       "404 not found error",
			statusCode: 404,
			errorBody:  []byte(`{"error":{"message":"File not found","type":"invalid_request_error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 404 - {\"error\":{\"message\":\"File not found\",\"type\":\"invalid_request_error\"}}",
			},
			expectedEvents: 1,
		},
		{
			name:       "401 authentication error",
			statusCode: 401,
			errorBody:  []byte(`{"error":{"message":"Unauthorized","type":"authentication_error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 401 - {\"error\":{\"message\":\"Unauthorized\",\"type\":\"authentication_error\"}}",
			},
			expectedEvents: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewRetrieveFileRecorderFromEnv()

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponseOnError(span, tt.statusCode, tt.errorBody)
				return false
			})

			require.Equal(t, tt.expectedStatus, actualSpan.Status)
			require.Len(t, actualSpan.Events, tt.expectedEvents)
			if tt.expectedEvents > 0 {
				require.Equal(t, "exception", actualSpan.Events[0].Name)
			}
		})
	}
}

func TestRetrieveFileRecorder_NewRetrieveFileRecorder_NilConfig(t *testing.T) {
	recorder := NewRetrieveFileRecorder(nil)

	require.NotNil(t, recorder)
	_, ok := recorder.(*RetrieveFileRecorder)
	require.True(t, ok)
}

// ---------------------------------------------------------------------------
// RetrieveFileContentRecorder
// ---------------------------------------------------------------------------

func TestRetrieveFileContentRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name             string
		expectedSpanName string
	}{
		{
			name:             "basic request",
			expectedSpanName: "RetrieveFileContent",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewRetrieveFileContentRecorderFromEnv()

			spanName, opts := recorder.StartParams(nil, nil)
			actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
		})
	}
}

func TestRetrieveFileContentRecorder_RecordRequest(t *testing.T) {
	tests := []struct {
		name          string
		expectedAttrs []attribute.KeyValue
	}{
		{
			name: "basic request",
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewRetrieveFileContentRecorderFromEnv()

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, nil, nil)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Empty(t, actualSpan.Events)
			require.Equal(t, trace.Status{Code: codes.Unset, Description: ""}, actualSpan.Status)
		})
	}
}

func TestRetrieveFileContentRecorder_RecordResponse(t *testing.T) {
	tests := []struct {
		name           string
		config         *openinference.TraceConfig
		expectedAttrs  []attribute.KeyValue
		expectedStatus trace.Status
	}{
		{
			name:           "successful response",
			config:         &openinference.TraceConfig{},
			expectedAttrs:  []attribute.KeyValue{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:           "hidden outputs",
			config:         &openinference.TraceConfig{HideOutputs: true},
			expectedAttrs:  []attribute.KeyValue{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewRetrieveFileContentRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, nil)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Equal(t, tt.expectedStatus, actualSpan.Status)
		})
	}
}

func TestRetrieveFileContentRecorder_RecordResponseOnError(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		errorBody      []byte
		expectedStatus trace.Status
		expectedEvents int
	}{
		{
			name:       "404 not found error",
			statusCode: 404,
			errorBody:  []byte(`{"error":{"message":"File not found","type":"invalid_request_error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 404 - {\"error\":{\"message\":\"File not found\",\"type\":\"invalid_request_error\"}}",
			},
			expectedEvents: 1,
		},
		{
			name:       "500 internal server error",
			statusCode: 500,
			errorBody:  []byte(`{"error":{"message":"Internal server error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 500 - {\"error\":{\"message\":\"Internal server error\"}}",
			},
			expectedEvents: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewRetrieveFileContentRecorderFromEnv()

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponseOnError(span, tt.statusCode, tt.errorBody)
				return false
			})

			require.Equal(t, tt.expectedStatus, actualSpan.Status)
			require.Len(t, actualSpan.Events, tt.expectedEvents)
			if tt.expectedEvents > 0 {
				require.Equal(t, "exception", actualSpan.Events[0].Name)
			}
		})
	}
}

func TestRetrieveFileContentRecorder_NewRetrieveFileContentRecorder_NilConfig(t *testing.T) {
	recorder := NewRetrieveFileContentRecorder(nil)

	require.NotNil(t, recorder)
	_, ok := recorder.(*RetrieveFileContentRecorder)
	require.True(t, ok)
}

// ---------------------------------------------------------------------------
// DeleteFileRecorder
// ---------------------------------------------------------------------------

func TestDeleteFileRecorder_StartParams(t *testing.T) {
	tests := []struct {
		name             string
		expectedSpanName string
	}{
		{
			name:             "basic request",
			expectedSpanName: "DeleteFile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewDeleteFileRecorderFromEnv()

			spanName, opts := recorder.StartParams(nil, nil)
			actualSpan := testotel.RecordNewSpan(t, spanName, opts...)

			require.Equal(t, tt.expectedSpanName, actualSpan.Name)
			require.Equal(t, oteltrace.SpanKindInternal, actualSpan.SpanKind)
		})
	}
}

func TestDeleteFileRecorder_RecordRequest(t *testing.T) {
	tests := []struct {
		name          string
		expectedAttrs []attribute.KeyValue
	}{
		{
			name: "basic request",
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.SpanKind, openinference.SpanKindLLM),
				attribute.String(openinference.LLMSystem, openinference.LLMSystemOpenAI),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewDeleteFileRecorderFromEnv()

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordRequest(span, nil, nil)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Empty(t, actualSpan.Events)
			require.Equal(t, trace.Status{Code: codes.Unset, Description: ""}, actualSpan.Status)
		})
	}
}

func TestDeleteFileRecorder_RecordResponse(t *testing.T) {
	tests := []struct {
		name           string
		resp           *openai.FileDeleted
		config         *openinference.TraceConfig
		expectedAttrs  []attribute.KeyValue
		expectedStatus trace.Status
	}{
		{
			name:   "successful response",
			resp:   basicFileDeleted,
			config: &openinference.TraceConfig{},
			expectedAttrs: []attribute.KeyValue{
				attribute.String(openinference.OutputMimeType, openinference.MimeTypeJSON),
				attribute.String("output.file_id", basicFileDeleted.ID),
			},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:           "hidden outputs",
			resp:           basicFileDeleted,
			config:         &openinference.TraceConfig{HideOutputs: true},
			expectedAttrs:  []attribute.KeyValue{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
		{
			name:           "nil response",
			resp:           nil,
			config:         &openinference.TraceConfig{},
			expectedAttrs:  []attribute.KeyValue{},
			expectedStatus: trace.Status{Code: codes.Ok, Description: ""},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewDeleteFileRecorder(tt.config)

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponse(span, tt.resp)
				return false
			})

			openinference.RequireAttributesEqual(t, tt.expectedAttrs, actualSpan.Attributes)
			require.Equal(t, tt.expectedStatus, actualSpan.Status)
		})
	}
}

func TestDeleteFileRecorder_RecordResponseOnError(t *testing.T) {
	tests := []struct {
		name           string
		statusCode     int
		errorBody      []byte
		expectedStatus trace.Status
		expectedEvents int
	}{
		{
			name:       "404 not found error",
			statusCode: 404,
			errorBody:  []byte(`{"error":{"message":"File not found","type":"invalid_request_error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 404 - {\"error\":{\"message\":\"File not found\",\"type\":\"invalid_request_error\"}}",
			},
			expectedEvents: 1,
		},
		{
			name:       "500 internal server error",
			statusCode: 500,
			errorBody:  []byte(`{"error":{"message":"Internal server error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 500 - {\"error\":{\"message\":\"Internal server error\"}}",
			},
			expectedEvents: 1,
		},
		{
			name:       "403 forbidden error",
			statusCode: 403,
			errorBody:  []byte(`{"error":{"message":"Forbidden","type":"permission_error"}}`),
			expectedStatus: trace.Status{
				Code:        codes.Error,
				Description: "Error code: 403 - {\"error\":{\"message\":\"Forbidden\",\"type\":\"permission_error\"}}",
			},
			expectedEvents: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := NewDeleteFileRecorderFromEnv()

			actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
				recorder.RecordResponseOnError(span, tt.statusCode, tt.errorBody)
				return false
			})

			require.Equal(t, tt.expectedStatus, actualSpan.Status)
			require.Len(t, actualSpan.Events, tt.expectedEvents)
			if tt.expectedEvents > 0 {
				require.Equal(t, "exception", actualSpan.Events[0].Name)
			}
		})
	}
}

func TestDeleteFileRecorder_NewDeleteFileRecorder_NilConfig(t *testing.T) {
	recorder := NewDeleteFileRecorder(nil)

	require.NotNil(t, recorder)
	_, ok := recorder.(*DeleteFileRecorder)
	require.True(t, ok)
}

// ---------------------------------------------------------------------------
// Config from environment tests
// ---------------------------------------------------------------------------

func TestCreateFileRecorder_ConfigFromEnvironment(t *testing.T) {
	t.Setenv(openinference.EnvHideOutputs, "true")

	recorder := NewCreateFileRecorderFromEnv()

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponse(span, basicFileObject)
		return false
	})

	attrs := make(map[string]attribute.Value)
	for _, kv := range actualSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value
	}
	// output.file_id should not be present when outputs are hidden
	_, hasFileID := attrs["output.file_id"]
	require.False(t, hasFileID)
	require.Equal(t, codes.Ok, actualSpan.Status.Code)
}

func TestRetrieveFileRecorder_ConfigFromEnvironment(t *testing.T) {
	t.Setenv(openinference.EnvHideOutputs, "true")

	recorder := NewRetrieveFileRecorderFromEnv()

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponse(span, basicFileObject)
		return false
	})

	attrs := make(map[string]attribute.Value)
	for _, kv := range actualSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value
	}
	_, hasFileID := attrs["output.file_id"]
	require.False(t, hasFileID)
	require.Equal(t, codes.Ok, actualSpan.Status.Code)
}

func TestDeleteFileRecorder_ConfigFromEnvironment(t *testing.T) {
	t.Setenv(openinference.EnvHideOutputs, "true")

	recorder := NewDeleteFileRecorderFromEnv()

	actualSpan := testotel.RecordWithSpan(t, func(span oteltrace.Span) bool {
		recorder.RecordResponse(span, basicFileDeleted)
		return false
	})

	attrs := make(map[string]attribute.Value)
	for _, kv := range actualSpan.Attributes {
		attrs[string(kv.Key)] = kv.Value
	}
	_, hasFileID := attrs["output.file_id"]
	require.False(t, hasFileID)
	require.Equal(t, codes.Ok, actualSpan.Status.Code)
}
