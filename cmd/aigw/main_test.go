// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_doMain(t *testing.T) {
	tests := []struct {
		name   string
		args   []string
		tf     translateFn
		expOut string
	}{
		{
			name:   "version",
			args:   []string{"version"},
			expOut: "Envoy AI Gateway CLI: dev\n",
		},
		{
			name: "translate",
			args: []string{"translate", "path1", "path2"},
			tf: func(c cmdTranslate, _, _ io.Writer) error {
				cwd, err := os.Getwd()
				require.NoError(t, err)
				require.Equal(t, []string{cwd + "/path1", cwd + "/path2"}, c.Paths)
				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			doMain(out, os.Stderr, tt.args, tt.tf)
			require.Equal(t, tt.expOut, out.String())
		})
	}
}
