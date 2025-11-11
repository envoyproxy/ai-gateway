// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package version

import "testing"

func TestParse(t *testing.T) {
	type versionStringTest struct {
		input string
		want  string
	}
	// versionString syntax:
	//   <release tag>-<commits since release tag>-<commit hash>
	tests := []versionStringTest{
		{input: "0.6.6-0-g12345678", want: "0.6.6"},
		{input: "0.6.6-2-gabcdef01", want: "abcdef01 (0.6.6, +2)"},
		{input: "0.6.6-rc1-0-g12345678", want: "0.6.6-rc1"},
		{input: "0.6.6-rc1-g12345678:", want: "dev"}, // unparseable: no commits present
		{input: "", want: "dev"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			version = test.input
			if have := Parse(); test.want != have {
				t.Errorf("want: %s, have: %s", test.want, have)
			}
		})
	}
}
