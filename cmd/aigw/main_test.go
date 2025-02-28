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
	"k8s.io/utils/ptr"
)

func Test_doMain(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		tf           translateFn
		expOut       string
		expPanicCode *int
	}{
		{
			name: "help",
			args: []string{"--help"},
			tf:   func(_ cmdTranslate, _, _ io.Writer) error { return nil },
			expOut: `Usage: aigw <command> [flags]

Envoy AI Gateway CLI

Flags:
  -h, --help    Show context-sensitive help.

Commands:
  version [flags]
    Show version.

  translate <path> ... [flags]
    Translate yaml files containing AI Gateway resources to Envoy Gateway and
    Kubernetes API Gateway resources. The translated resources are written to
    stdout.

Run "aigw <command> --help" for more information on a command.
`,
			expPanicCode: ptr.To(0),
		},
		{
			name:   "version",
			args:   []string{"version"},
			expOut: "Envoy AI Gateway CLI: dev\n",
		},
		{
			name: "translate",
			args: []string{"translate", "path1", "path2", "--debug"},
			tf: func(c cmdTranslate, _, _ io.Writer) error {
				cwd, err := os.Getwd()
				require.NoError(t, err)
				require.Equal(t, []string{cwd + "/path1", cwd + "/path2"}, c.Paths)
				return nil
			},
		},
		{
			name:         "translate no arg",
			args:         []string{"translate"},
			tf:           func(_ cmdTranslate, _, _ io.Writer) error { return nil },
			expPanicCode: ptr.To(1),
		},
		{
			name: "translate with help",
			args: []string{"translate", "--help"},
			tf:   func(_ cmdTranslate, _, _ io.Writer) error { return nil },
			expOut: `Usage: aigw translate <path> ... [flags]

Translate yaml files containing AI Gateway resources to Envoy Gateway and
Kubernetes API Gateway resources. The translated resources are written to
stdout.

Arguments:
  <path> ...    Paths to yaml files to translate.

Flags:
  -h, --help     Show context-sensitive help.

      --debug    Enable debug logging emitted to stderr.
`,
			expPanicCode: ptr.To(0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			if tt.expPanicCode != nil {
				require.PanicsWithValue(t, *tt.expPanicCode, func() {
					doMain(out, os.Stderr, tt.args, func(code int) { panic(code) }, tt.tf)
				})
				require.Equal(t, tt.expOut, out.String())
				return
			}
			doMain(out, os.Stderr, tt.args, func(code int) { panic(code) }, tt.tf)
			require.Equal(t, tt.expOut, out.String())
		})
	}
}
