// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnmarshalJSONNestedUnion(t *testing.T) {
	additionalSuccessCases := []struct {
		name     string
		data     []byte
		expected interface{}
	}{
		{
			name:     "string with escaped path", // Tests json.Unmarshal fallback when strconv.Unquote fails
			data:     []byte(`"/path\/to\/file"`),
			expected: "/path/to/file",
		},
		{
			name:     "truncated array defaults to string array",
			data:     []byte(`[]`),
			expected: []string{},
		},
		{
			name:     "array with whitespace before close bracket",
			data:     []byte(`[  ]`),
			expected: []string{},
		},
		{
			name:     "negative number in array",
			data:     []byte(`[-1, -2, -3]`),
			expected: []int64{-1, -2, -3},
		},
		{
			name:     "array with leading whitespace",
			data:     []byte(`[ "test"]`),
			expected: []string{"test"},
		},
		{
			name:     "data with leading whitespace",
			data:     []byte(`  "test"`),
			expected: "test",
		},
		{
			name:     "data with all whitespace types",
			data:     []byte(" \t\n\r\"test\""),
			expected: "test",
		},
		{
			name:     "array of token arrays",
			data:     []byte(`[[-1, -2, -3], [1, 2, 3]]`),
			expected: [][]int64{{-1, -2, -3}, {1, 2, 3}},
		},
		{
			name:     "array of strings",
			data:     []byte(`[ "aa", "bb", "cc" ]`),
			expected: []string{"aa", "bb", "cc"},
		},
		{
			name: "array of EmbeddingInputItem objects",
			data: []byte(`[{"content":"hello"},{"content":"world","task_type":"RETRIEVAL_QUERY"}]`),
			expected: []EmbeddingInputItem{
				{Content: "hello"},
				{Content: "world", TaskType: "RETRIEVAL_QUERY"},
			},
		},
		{
			name: "single EmbeddingInputItem object",
			data: []byte(`{"content":"test content","task_type":"RETRIEVAL_DOCUMENT","title":"Test"}`),
			expected: EmbeddingInputItem{
				Content:  "test content",
				TaskType: "RETRIEVAL_DOCUMENT",
				Title:    "Test",
			},
		},
		{
			name: "mixed array with strings and objects",
			data: []byte(`["plain text",{"content":"structured content","task_type":"RETRIEVAL_QUERY"},"another string"]`),
			expected: []interface{}{
				"plain text",
				EmbeddingInputItem{Content: "structured content", TaskType: "RETRIEVAL_QUERY"},
				"another string",
			},
		},
		{
			name: "mixed array with whitespace",
			data: []byte(`[ "text" , { "content": "obj" } , "more text" ]`),
			expected: []interface{}{
				"text",
				EmbeddingInputItem{Content: "obj"},
				"more text",
			},
		},
	}

	allCases := append(promptUnionBenchmarkCases, additionalSuccessCases...) //nolint:gocritic // intentionally creating new slice
	for _, tc := range allCases {
		t.Run(tc.name, func(t *testing.T) {
			val, err := unmarshalJSONNestedUnion("prompt", tc.data)
			require.NoError(t, err)
			require.Equal(t, tc.expected, val)
		})
	}
}

func TestUnmarshalJSONNestedUnion_Errors(t *testing.T) {
	errorTestCases := []struct {
		name        string
		data        []byte
		expectedErr string
	}{
		{
			name:        "truncated data",
			data:        []byte{},
			expectedErr: "truncated prompt data",
		},
		{
			name:        "only whitespace",
			data:        []byte("   \t\n\r   "),
			expectedErr: "truncated prompt data",
		},
		{
			name:        "invalid JSON string",
			data:        []byte(`"unterminated`),
			expectedErr: "cannot unmarshal prompt as string",
		},
		{
			name:        "truncated data",
			data:        []byte(`[`),
			expectedErr: "truncated prompt data",
		},
		{
			name:        "invalid array element",
			data:        []byte(`[null]`),
			expectedErr: "invalid prompt array element",
		},
		{
			name:        "invalid array element - object",
			data:        []byte(`[{}]`),
			expectedErr: "invalid prompt array element",
		},
		{
			name:        "invalid string array",
			data:        []byte(`["test", 123]`),
			expectedErr: "cannot unmarshal prompt as []string",
		},
		{
			name:        "invalid int array",
			data:        []byte(`[1, "two", 3]`),
			expectedErr: "cannot unmarshal prompt as []int64",
		},
		{
			name:        "invalid nested int array",
			data:        []byte(`[[1, 2], ["three", 4]]`),
			expectedErr: "cannot unmarshal prompt as [][]int64",
		},
		{
			name:        "invalid type - object",
			data:        []byte(`{"key": "value"}`),
			expectedErr: "invalid prompt type (must be string, object, or array)",
		},
		{
			name:        "invalid type - null",
			data:        []byte(`null`),
			expectedErr: "invalid prompt type (must be string, object, or array)",
		},
		{
			name:        "invalid type - boolean",
			data:        []byte(`true`),
			expectedErr: "invalid prompt type (must be string, object, or array)",
		},
		{
			name:        "invalid type - bare number",
			data:        []byte(`42`),
			expectedErr: "invalid prompt type (must be string, object, or array)",
		},
		{
			name:        "array with only whitespace after bracket",
			data:        []byte(`[   `),
			expectedErr: "truncated prompt data",
		},
		{
			name:        "object without content field",
			data:        []byte(`{"task_type":"RETRIEVAL_QUERY"}`),
			expectedErr: "invalid prompt type",
		},
		{
			name:        "object with empty content",
			data:        []byte(`{"content":""}`),
			expectedErr: "invalid prompt type",
		},
		{
			name:        "array of objects with empty content",
			data:        []byte(`[{"content":"valid"},{"content":""}]`),
			expectedErr: "invalid prompt array element",
		},
		{
			name:        "mixed array with empty element",
			data:        []byte(`["text",   ,"more"]`),
			expectedErr: "invalid character",
		},
		{
			name:        "mixed array with invalid object",
			data:        []byte(`["text",{"task_type":"QUERY"},"more"]`),
			expectedErr: "invalid element type in mixed prompt array",
		},
		{
			name:        "mixed array with number element",
			data:        []byte(`["text",123,"more"]`),
			expectedErr: "cannot unmarshal prompt as []string",
		},
	}

	for _, tc := range errorTestCases {
		t.Run(tc.name, func(t *testing.T) {
			val, err := unmarshalJSONNestedUnion("prompt", tc.data)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErr)
			require.Zero(t, val)
		})
	}
}
