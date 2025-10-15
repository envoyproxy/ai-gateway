// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package lang

import "testing"

func TestCaseInsensitiveValue(t *testing.T) {
	tests := []struct {
		name string
		m    map[string]any
		key  string
		want string
	}{
		{
			name: "nil map",
			m:    nil,
			key:  "anything",
			want: "",
		},
		{
			name: "exact match returns value",
			m:    map[string]any{"Foo": "bar", "foo": "should-not-be-used"},
			key:  "Foo",
			want: "bar",
		},
		{
			name: "case-insensitive match when exact not present",
			m:    map[string]any{"FOO": "baz"},
			key:  "foo",
			want: "baz",
		},
		{
			name: "multiple case variants - alphabetical first chosen",
			m:    map[string]any{"ALPHA": 2, "Alpha": 1},
			key:  "alpha",
			want: "2", // ALPHA is alphabetically first
		},
		{
			name: "nil value formatted",
			m:    map[string]any{"key": nil},
			key:  "key",
			want: "<nil>",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CaseInsensitiveValue(tc.m, tc.key)
			if got != tc.want {
				t.Fatalf("CaseInsensitiveValue(%v, %q) = %q; want %q", tc.m, tc.key, got, tc.want)
			}
		})
	}
}
