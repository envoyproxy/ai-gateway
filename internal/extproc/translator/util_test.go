// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"testing"
)

// TestProcessStopToStringPointers tests the ProcessStopToStringPointers helper function.
func TestProcessStopToStringPointers(t *testing.T) {
	makeStrPtrSlice := func(strs ...string) []*string {
		pointers := make([]*string, len(strs))
		for i, s := range strs {
			temp := s
			pointers[i] = &temp
		}
		return pointers
	}

	testCases := []struct {
		name        string
		input       interface{}
		expected    []*string
		expectError bool
	}{
		{
			name:        "Successful conversion from single string",
			input:       "stop-word",
			expected:    makeStrPtrSlice("stop-word"),
			expectError: false,
		},
		{
			name:        "Successful handling of string slice",
			input:       []string{"stop1", "stop2", "stop3"},
			expected:    makeStrPtrSlice("stop1", "stop2", "stop3"),
			expectError: false,
		},
		{
			name:        "Successful handling of slice of string pointers",
			input:       makeStrPtrSlice("ptr1", "ptr2"),
			expected:    makeStrPtrSlice("ptr1", "ptr2"),
			expectError: false,
		},
		{
			name:        "Handling a nil interface",
			input:       nil,
			expected:    nil,
			expectError: false,
		},
		{
			name:        "Handling an empty string slice",
			input:       []string{},
			expected:    []*string{},
			expectError: false,
		},
		{
			name:        "Failed conversion with an integer",
			input:       12345,
			expected:    nil,
			expectError: true,
		},
		{
			name:        "Failed conversion with a slice of integers",
			input:       []int{1, 2, 3},
			expected:    nil,
			expectError: true,
		},
	}

	areEqual := func(a, b []*string) bool {
		if len(a) != len(b) {
			return false
		}
		if a == nil && b == nil {
			return true
		}
		for i := range a {
			// Check for nil pointers before dereferencing.
			if (a[i] == nil && b[i] != nil) || (a[i] != nil && b[i] == nil) {
				return false
			}
			if a[i] != nil && b[i] != nil && *a[i] != *b[i] {
				return false
			}
		}
		return true
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := processStop(tc.input)
			if tc.expectError {
				if err == nil {
					t.Errorf("Expected an error, but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Expected no error, but got: %v", err)
			}

			if !areEqual(result, tc.expected) {
				t.Errorf("Result slice values do not match expected slice values")
			}
		})
	}
}
