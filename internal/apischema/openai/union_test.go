// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package openai

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/json"
)

// TestUnmarshalJSONNestedUnion tests the completion API prompt parsing.
// This function only supports: string, []string, []int64, [][]int64
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
			name:        "invalid type - object (objects not supported for completion prompts)",
			data:        []byte(`{"key": "value"}`),
			expectedErr: "invalid prompt type (must be string or array)",
		},
		{
			name:        "invalid type - null",
			data:        []byte(`null`),
			expectedErr: "invalid prompt type (must be string or array)",
		},
		{
			name:        "invalid type - boolean",
			data:        []byte(`true`),
			expectedErr: "invalid prompt type (must be string or array)",
		},
		{
			name:        "invalid type - bare number",
			data:        []byte(`42`),
			expectedErr: "invalid prompt type (must be string or array)",
		},
		{
			name:        "array with only whitespace after bracket",
			data:        []byte(`[   `),
			expectedErr: "truncated prompt data",
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

// TestUnmarshalJSONEmbeddingInput tests the embedding API input parsing.
// This function supports: string, []string, EmbeddingInputItem, []EmbeddingInputItem, []int64, [][]int64
func TestUnmarshalJSONEmbeddingInput(t *testing.T) {
	successCases := []struct {
		name     string
		data     []byte
		expected interface{}
	}{
		{
			name:     "simple string",
			data:     []byte(`"hello world"`),
			expected: "hello world",
		},
		{
			name:     "array of strings",
			data:     []byte(`["aa", "bb", "cc"]`),
			expected: []string{"aa", "bb", "cc"},
		},
		{
			name:     "empty array",
			data:     []byte(`[]`),
			expected: []string{},
		},
		{
			name:     "array of tokens",
			data:     []byte(`[1, 2, 3]`),
			expected: []int64{1, 2, 3},
		},
		{
			name:     "array of token arrays",
			data:     []byte(`[[1, 2], [3, 4]]`),
			expected: [][]int64{{1, 2}, {3, 4}},
		},
		{
			name: "array of EmbeddingInputItem objects",
			data: []byte(`[{"content":"hello"},{"content":"world","task_type":"RETRIEVAL_QUERY"}]`),
			expected: []EmbeddingInputItem{
				{Content: EmbeddingContent{Value: "hello"}},
				{Content: EmbeddingContent{Value: "world"}, TaskType: "RETRIEVAL_QUERY"},
			},
		},
		{
			name: "single EmbeddingInputItem object with string content",
			data: []byte(`{"content":"test content","task_type":"RETRIEVAL_DOCUMENT","title":"Test"}`),
			expected: EmbeddingInputItem{
				Content:  EmbeddingContent{Value: "test content"},
				TaskType: "RETRIEVAL_DOCUMENT",
				Title:    "Test",
			},
		},
		{
			name: "single EmbeddingInputItem object with array content",
			data: []byte(`{"content":["text1","text2"],"task_type":"RETRIEVAL_QUERY"}`),
			expected: EmbeddingInputItem{
				Content:  EmbeddingContent{Value: []string{"text1", "text2"}},
				TaskType: "RETRIEVAL_QUERY",
			},
		},
	}

	for _, tc := range successCases {
		t.Run(tc.name, func(t *testing.T) {
			val, err := unmarshalJSONEmbeddingInput("input", tc.data)
			require.NoError(t, err)
			require.Equal(t, tc.expected, val)
		})
	}
}

func TestUnmarshalJSONEmbeddingInput_Errors(t *testing.T) {
	errorTestCases := []struct {
		name        string
		data        []byte
		expectedErr string
	}{
		{
			name:        "truncated data",
			data:        []byte{},
			expectedErr: "truncated input data",
		},
		{
			name:        "object without content field",
			data:        []byte(`{"task_type":"RETRIEVAL_QUERY"}`),
			expectedErr: "invalid input type",
		},
		{
			name:        "object with empty content",
			data:        []byte(`{"content":""}`),
			expectedErr: "invalid input type",
		},
		{
			name:        "object with empty array content",
			data:        []byte(`{"content":[]}`),
			expectedErr: "invalid input type",
		},
		{
			name:        "array of objects with empty content",
			data:        []byte(`[{"content":"valid"},{"content":""}]`),
			expectedErr: "invalid input array element",
		},
		{
			name:        "invalid type - null",
			data:        []byte(`null`),
			expectedErr: "invalid input type (must be string, object, or array)",
		},
		{
			name:        "invalid type - boolean",
			data:        []byte(`true`),
			expectedErr: "invalid input type (must be string, object, or array)",
		},
	}

	for _, tc := range errorTestCases {
		t.Run(tc.name, func(t *testing.T) {
			val, err := unmarshalJSONEmbeddingInput("input", tc.data)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErr)
			require.Zero(t, val)
		})
	}
}

func TestThinkingUnion_UnmarshalJSON(t *testing.T) {
	tests := []struct {
		name   string
		data   string
		expect ThinkingUnion
	}{
		{
			name: "enabled",
			data: `{"type":"enabled","budget_tokens":1024}`,
			expect: ThinkingUnion{
				OfEnabled: &ThinkingEnabled{Type: "enabled", BudgetTokens: 1024},
			},
		},
		{
			name: "disabled",
			data: `{"type":"disabled"}`,
			expect: ThinkingUnion{
				OfDisabled: &ThinkingDisabled{Type: "disabled"},
			},
		},
		{
			name: "adaptive",
			data: `{"type":"adaptive"}`,
			expect: ThinkingUnion{
				OfAdaptive: &ThinkingAdaptive{Type: "adaptive"},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got ThinkingUnion
			err := json.Unmarshal([]byte(tc.data), &got)
			require.NoError(t, err)
			require.Equal(t, tc.expect, got)
		})
	}
}

func TestThinkingUnion_UnmarshalJSON_Errors(t *testing.T) {
	tests := []struct {
		name        string
		data        string
		expectedErr string
	}{
		{
			name:        "missing type field",
			data:        `{"budget_tokens":1024}`,
			expectedErr: "thinking config does not have a type",
		},
		{
			name:        "invalid type value",
			data:        `{"type":"unknown"}`,
			expectedErr: "invalid thinking union type: unknown",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got ThinkingUnion
			err := json.Unmarshal([]byte(tc.data), &got)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.expectedErr)
		})
	}
}

func TestThinkingUnion_MarshalJSON(t *testing.T) {
	tests := []struct {
		name   string
		input  ThinkingUnion
		expect string
	}{
		{
			name: "enabled",
			input: ThinkingUnion{
				OfEnabled: &ThinkingEnabled{Type: "enabled", BudgetTokens: 1024},
			},
			expect: `{"budget_tokens":1024,"type":"enabled"}`,
		},
		{
			name: "disabled",
			input: ThinkingUnion{
				OfDisabled: &ThinkingDisabled{Type: "disabled"},
			},
			expect: `{"type":"disabled"}`,
		},
		{
			name: "adaptive",
			input: ThinkingUnion{
				OfAdaptive: &ThinkingAdaptive{Type: "adaptive"},
			},
			expect: `{"type":"adaptive"}`,
		},
		{
			name:   "all nil returns empty object",
			input:  ThinkingUnion{},
			expect: `{}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(&tc.input)
			require.NoError(t, err)
			require.JSONEq(t, tc.expect, string(got))
		})
	}
}
