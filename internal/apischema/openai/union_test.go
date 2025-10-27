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
