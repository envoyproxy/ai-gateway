// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopeninference

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
)

func TestRequireSpanEqual(t *testing.T) {
	tests := []struct {
		name          string
		expected      *tracev1.Span
		actual        *tracev1.Span
		shouldFail    bool
		errorContains string
	}{
		{
			name:       "both nil spans",
			expected:   nil,
			actual:     nil,
			shouldFail: false,
		},
		{
			name:          "expected nil",
			expected:      nil,
			actual:        &tracev1.Span{Name: "test"},
			shouldFail:    true,
			errorContains: "expected span is nil but actual is not nil",
		},
		{
			name:          "actual nil",
			expected:      &tracev1.Span{Name: "test"},
			actual:        nil,
			shouldFail:    true,
			errorContains: "actual span is nil but expected is not nil",
		},
		{
			name: "equal spans",
			expected: &tracev1.Span{
				Name: "ChatCompletion",
				Kind: tracev1.Span_SPAN_KIND_INTERNAL,
				Attributes: []*commonv1.KeyValue{
					{Key: "test", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "value"}}},
				},
			},
			actual: &tracev1.Span{
				Name: "ChatCompletion",
				Kind: tracev1.Span_SPAN_KIND_INTERNAL,
				Attributes: []*commonv1.KeyValue{
					{Key: "test", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "value"}}},
				},
			},
			shouldFail: false,
		},
		{
			name: "equal spans with different trace IDs",
			expected: &tracev1.Span{
				TraceId: []byte{1, 2, 3},
				SpanId:  []byte{4, 5, 6},
				Name:    "ChatCompletion",
			},
			actual: &tracev1.Span{
				TraceId: []byte{7, 8, 9},
				SpanId:  []byte{10, 11, 12},
				Name:    "ChatCompletion",
			},
			shouldFail: false, // Should pass because we clear IDs.
		},
		{
			name: "equal spans with different timestamps",
			expected: &tracev1.Span{
				StartTimeUnixNano: 1000,
				EndTimeUnixNano:   2000,
				Name:              "ChatCompletion",
			},
			actual: &tracev1.Span{
				StartTimeUnixNano: 3000,
				EndTimeUnixNano:   4000,
				Name:              "ChatCompletion",
			},
			shouldFail: false, // Should pass because we clear timestamps.
		},
		{
			name: "different span names",
			expected: &tracev1.Span{
				Name: "ChatCompletion",
			},
			actual: &tracev1.Span{
				Name: "DifferentName",
			},
			shouldFail:    true,
			errorContains: "spans are not equal",
		},
		{
			name: "different attribute ordering",
			expected: &tracev1.Span{
				Name: "ChatCompletion",
				Attributes: []*commonv1.KeyValue{
					{Key: "b", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "2"}}},
					{Key: "a", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "1"}}},
				},
			},
			actual: &tracev1.Span{
				Name: "ChatCompletion",
				Attributes: []*commonv1.KeyValue{
					{Key: "a", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "1"}}},
					{Key: "b", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "2"}}},
				},
			},
			shouldFail: false, // Should pass because attributes are sorted.
		},
		{
			name: "JSON attributes with different formatting",
			expected: &tracev1.Span{
				Name: "ChatCompletion",
				Attributes: []*commonv1.KeyValue{
					{Key: "params", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: `{"model": "gpt-4", "temp": 0.7}`}}},
				},
			},
			actual: &tracev1.Span{
				Name: "ChatCompletion",
				Attributes: []*commonv1.KeyValue{
					{Key: "params", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: `{"model":"gpt-4","temp":0.7}`}}},
				},
			},
			shouldFail: false, // Should pass because JSON is normalized.
		},
		{
			name: "JSON array attributes normalized",
			expected: &tracev1.Span{
				Name: "ChatCompletion",
				Attributes: []*commonv1.KeyValue{
					{Key: "items", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: `[1, 2, 3]`}}},
				},
			},
			actual: &tracev1.Span{
				Name: "ChatCompletion",
				Attributes: []*commonv1.KeyValue{
					{Key: "items", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: `[1,2,3]`}}},
				},
			},
			shouldFail: false, // Should pass because JSON is normalized.
		},
		{
			name: "spans with events - timestamps cleared",
			expected: &tracev1.Span{
				Name: "ChatCompletion",
				Events: []*tracev1.Span_Event{
					{
						TimeUnixNano: 1000,
						Name:         "event1",
					},
				},
			},
			actual: &tracev1.Span{
				Name: "ChatCompletion",
				Events: []*tracev1.Span_Event{
					{
						TimeUnixNano: 2000,
						Name:         "event1",
					},
				},
			},
			shouldFail: false, // Should pass because event timestamps are cleared.
		},
		{
			name: "spans with links - IDs cleared",
			expected: &tracev1.Span{
				Name: "ChatCompletion",
				Links: []*tracev1.Span_Link{
					{
						TraceId: []byte{1, 2, 3},
						SpanId:  []byte{4, 5, 6},
					},
				},
			},
			actual: &tracev1.Span{
				Name: "ChatCompletion",
				Links: []*tracev1.Span_Link{
					{
						TraceId: []byte{7, 8, 9},
						SpanId:  []byte{10, 11, 12},
					},
				},
			},
			shouldFail: false, // Should pass because link IDs are cleared.
		},
		{
			name: "error status with Python dict format normalized",
			expected: &tracev1.Span{
				Name: "ChatCompletion",
				Status: &tracev1.Status{
					Code:    tracev1.Status_STATUS_CODE_ERROR,
					Message: "BadRequestError: Error code: 400 - {'error': {'code': 'test', 'message': 'test error'}}",
				},
			},
			actual: &tracev1.Span{
				Name: "ChatCompletion",
				Status: &tracev1.Status{
					Code:    tracev1.Status_STATUS_CODE_ERROR,
					Message: `Error code: 400 - {"error": {"code": "test", "message": "test error"}}`,
				},
			},
			shouldFail: false, // Should pass because errors are normalized to just "Error code: XXX".
		},
		{
			name: "spans with exception events",
			expected: &tracev1.Span{
				Name: "ChatCompletion",
				Events: []*tracev1.Span_Event{
					{
						Name: "exception",
						Attributes: []*commonv1.KeyValue{
							{Key: "exception.type", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "Error"}}},
							{Key: "exception.message", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "test error"}}},
						},
					},
				},
			},
			actual: &tracev1.Span{
				Name: "ChatCompletion",
				Events: []*tracev1.Span_Event{
					{
						Name: "exception",
						Attributes: []*commonv1.KeyValue{
							{Key: "exception.type", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "Error"}}},
							{Key: "exception.message", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "test error"}}},
						},
					},
				},
			},
			shouldFail: false,
		},
		{
			name: "nested zero structs in JSON value",
			expected: &tracev1.Span{
				Attributes: []*commonv1.KeyValue{
					{
						Key: "output.value",
						Value: &commonv1.AnyValue{
							Value: &commonv1.AnyValue_StringValue{
								StringValue: `{
                            "usage": {
                                "completion_tokens": 9,
                                "prompt_tokens": 19,
                                "total_tokens": 28
                            }
                        }`,
							},
						},
					},
				},
			},
			actual: &tracev1.Span{
				Attributes: []*commonv1.KeyValue{
					{
						Key: "output.value",
						Value: &commonv1.AnyValue{
							Value: &commonv1.AnyValue_StringValue{
								StringValue: `{
                            "usage": {
                                "completion_tokens": 9,
                                "prompt_tokens": 19,
                                "total_tokens": 28,
                                "completion_tokens_details": {
                                    "accepted_prediction_tokens": 0,
                                    "audio_tokens": 0,
                                    "reasoning_tokens": 0,
                                    "rejected_prediction_tokens": 0
                                },
                                "prompt_tokens_details": {
                                    "audio_tokens": 0,
                                    "cached_tokens": 0
                                }
                            }
                        }`,
							},
						},
					},
				},
			},
			shouldFail: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockT := &mockT{}

			RequireSpanEqual(mockT, tt.expected, tt.actual)

			if tt.shouldFail {
				require.True(t, mockT.failed, "expected test to fail but it passed")
				if tt.errorContains != "" {
					require.Contains(t, mockT.errorMsg, tt.errorContains, "error message mismatch")
				}
			} else {
				require.False(t, mockT.failed, "expected test to pass but it failed: %s", mockT.errorMsg)
			}
		})
	}
}

// mockT implements testing.TB.
var _ testing.TB = (*mockT)(nil)

type mockT struct {
	testing.TB
	failed   bool
	errorMsg string
}

func (m *mockT) Helper() {
	// No-op for testing.
}

func (m *mockT) Fatalf(format string, args ...any) {
	m.failed = true
	m.errorMsg = strings.TrimSpace(fmt.Sprintf(format, args...))
}

func TestNormalizeJSON(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "normalizes JSON formatting",
			input:    `{"choices": [{"logprobs": {}, "message": {"content": "Hello"}}]}`,
			expected: `{"choices":[{"message":{"content":"Hello"}}]}`,
		},
		{
			name:     "handles whitespace",
			input:    `{ "key" : "value" , "num" : 42 }`,
			expected: `{"key":"value","num":42}`,
		},
		{
			name:     "invalid JSON returns original",
			input:    "not valid json",
			expected: "not valid json",
		},
		{
			name:     "removes arrays with only null values",
			input:    `{"model":"gpt-4","object":"chat.completion","prompt_filter_results":[null],"usage":{"total_tokens":10}}`,
			expected: `{"model":"gpt-4","object":"chat.completion","usage":{"total_tokens":10}}`,
		},
		{
			name:     "removes arrays with objects containing only zero values",
			input:    `{"model":"gpt-4","prompt_filter_results":[{"prompt_index":0,"content_filter_results":{}}],"usage":{"total_tokens":10}}`,
			expected: `{"model":"gpt-4","usage":{"total_tokens":10}}`,
		},
		{
			name:     "preserves arrays with non-zero values",
			input:    `{"model":"gpt-4","prompt_filter_results":[{"prompt_index":0,"content_filter_results":{"hate":{"filtered":true}}}],"usage":{"total_tokens":10}}`,
			expected: `{"model":"gpt-4","prompt_filter_results":[{"content_filter_results":{"hate":{"filtered":true}}}],"usage":{"total_tokens":10}}`,
		},
		{
			name:     "removes nested maps with all zero values",
			input:    `{"model":"gpt-4","metadata":{"foo":"","bar":0,"baz":false},"usage":{"total_tokens":10}}`,
			expected: `{"model":"gpt-4","usage":{"total_tokens":10}}`,
		},
		{
			name:     "preserves nested maps with non-zero values",
			input:    `{"model":"gpt-4","metadata":{"foo":"value","bar":0},"usage":{"total_tokens":10}}`,
			expected: `{"metadata":{"foo":"value"},"model":"gpt-4","usage":{"total_tokens":10}}`,
		},
		{
			name:     "removes empty arrays",
			input:    `{"model":"gpt-4","empty_list":[],"usage":{"total_tokens":10}}`,
			expected: `{"model":"gpt-4","usage":{"total_tokens":10}}`,
		},
		{
			name:     "preserves arrays with mixed zero and non-zero values",
			input:    `{"model":"gpt-4","items":[0,"value",false],"usage":{"total_tokens":10}}`,
			expected: `{"items":[0,"value",false],"model":"gpt-4","usage":{"total_tokens":10}}`,
		},
		{
			name:     "handles deeply nested zero structures",
			input:    `{"model":"gpt-4","deep":{"level1":{"level2":{"level3":{"empty":""}}}},"usage":{"total_tokens":10}}`,
			expected: `{"model":"gpt-4","usage":{"total_tokens":10}}`,
		},
		{
			name:     "preserves non-zero boolean true",
			input:    `{"model":"gpt-4","stream":true,"usage":{"total_tokens":10}}`,
			expected: `{"model":"gpt-4","stream":true,"usage":{"total_tokens":10}}`,
		},
		{
			name:     "removes boolean false",
			input:    `{"model":"gpt-4","stream":false,"usage":{"total_tokens":10}}`,
			expected: `{"model":"gpt-4","usage":{"total_tokens":10}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeJSON(tt.input)
			require.Equal(t, tt.expected, result)
		})
	}
}

func TestNormalizeSpanForComparison(t *testing.T) {
	tests := []struct {
		name  string
		input *tracev1.Span
		check func(t *testing.T, span *tracev1.Span)
	}{
		{
			name: "removes Python-specific attributes",
			input: &tracev1.Span{
				Attributes: []*commonv1.KeyValue{
					{Key: "exception.type", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "Error"}}},
					{Key: "exception.stacktrace", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "traceback..."}}},
					{Key: "exception.escaped", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "False"}}},
					{Key: "other.attribute", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "value"}}},
				},
				Events: []*tracev1.Span_Event{
					{
						Name: "exception",
						Attributes: []*commonv1.KeyValue{
							{Key: "exception.type", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "Error"}}},
							{Key: "exception.stacktrace", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "traceback..."}}},
							{Key: "exception.escaped", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "False"}}},
						},
					},
				},
			},
			check: func(t *testing.T, span *tracev1.Span) {
				require.Len(t, span.Attributes, 2)
				for _, attr := range span.Attributes {
					require.NotContains(t, []string{"exception.stacktrace", "exception.escaped"}, attr.Key)
				}
				require.Len(t, span.Events[0].Attributes, 1)
				require.Equal(t, "exception.type", span.Events[0].Attributes[0].Key)
			},
		},
		{
			name: "normalizes Python dict format error message",
			input: &tracev1.Span{
				Status: &tracev1.Status{
					Message: "Error code: 400 - {'error': {'message': 'bad request'}}",
				},
			},
			check: func(t *testing.T, span *tracev1.Span) {
				require.Equal(t, "Error code: 400", span.Status.Message)
			},
		},
		{
			name: "normalizes multiline JSON error message",
			input: &tracev1.Span{
				Status: &tracev1.Status{
					Message: "Error code: 400 - {\n  \"error\": {\n    \"message\": \"bad request\"\n  }\n}",
				},
			},
			check: func(t *testing.T, span *tracev1.Span) {
				require.Equal(t, "Error code: 400", span.Status.Message)
			},
		},
		{
			name: "extracts error code from simple message",
			input: &tracev1.Span{
				Status: &tracev1.Status{
					Message: "Error code: 500 - Internal server Error",
				},
			},
			check: func(t *testing.T, span *tracev1.Span) {
				require.Equal(t, "Error code: 500", span.Status.Message)
			},
		},
		{
			name: "removes empty strings but keeps falsey values",
			input: &tracev1.Span{
				Attributes: []*commonv1.KeyValue{
					{Key: "test.empty", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: ""}}},
					{Key: "test.false", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "False"}}},
					{Key: "test.zero", Value: &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "0"}}},
				},
			},
			check: func(t *testing.T, span *tracev1.Span) {
				require.Len(t, span.Attributes, 2)
				for _, attr := range span.Attributes {
					require.NotEqual(t, "test.empty", attr.Key)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			normalizeSpanForComparison(tt.input)
			tt.check(t, tt.input)
		})
	}
}

func TestIsZeroValue(t *testing.T) {
	tests := []struct {
		name  string
		value *commonv1.AnyValue
		want  bool
	}{
		{"nil value", nil, true},
		{"empty string", &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: ""}}, true},
		{"non-empty string", &commonv1.AnyValue{Value: &commonv1.AnyValue_StringValue{StringValue: "test"}}, false},
		{"zero int", &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: 0}}, true},
		{"non-zero int", &commonv1.AnyValue{Value: &commonv1.AnyValue_IntValue{IntValue: 42}}, false},
		{"zero double", &commonv1.AnyValue{Value: &commonv1.AnyValue_DoubleValue{DoubleValue: 0.0}}, true},
		{"non-zero double", &commonv1.AnyValue{Value: &commonv1.AnyValue_DoubleValue{DoubleValue: 3.14}}, false},
		{"false bool", &commonv1.AnyValue{Value: &commonv1.AnyValue_BoolValue{BoolValue: false}}, true},
		{"true bool", &commonv1.AnyValue{Value: &commonv1.AnyValue_BoolValue{BoolValue: true}}, false},
		{"nil array", &commonv1.AnyValue{Value: &commonv1.AnyValue_ArrayValue{ArrayValue: nil}}, true},
		{"empty array", &commonv1.AnyValue{Value: &commonv1.AnyValue_ArrayValue{ArrayValue: &commonv1.ArrayValue{Values: []*commonv1.AnyValue{}}}}, true},
		{"non-empty array", &commonv1.AnyValue{Value: &commonv1.AnyValue_ArrayValue{ArrayValue: &commonv1.ArrayValue{Values: []*commonv1.AnyValue{{Value: &commonv1.AnyValue_IntValue{IntValue: 1}}}}}}, false},
		{"nil kvlist", &commonv1.AnyValue{Value: &commonv1.AnyValue_KvlistValue{KvlistValue: nil}}, true},
		{"empty kvlist", &commonv1.AnyValue{Value: &commonv1.AnyValue_KvlistValue{KvlistValue: &commonv1.KeyValueList{Values: []*commonv1.KeyValue{}}}}, true},
		{"non-empty kvlist", &commonv1.AnyValue{Value: &commonv1.AnyValue_KvlistValue{KvlistValue: &commonv1.KeyValueList{Values: []*commonv1.KeyValue{{Key: "test"}}}}}, false},
		{"empty bytes", &commonv1.AnyValue{Value: &commonv1.AnyValue_BytesValue{BytesValue: []byte{}}}, true},
		{"non-empty bytes", &commonv1.AnyValue{Value: &commonv1.AnyValue_BytesValue{BytesValue: []byte{1, 2, 3}}}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isZeroValue(tt.value)
			require.Equal(t, tt.want, got)
		})
	}
}
