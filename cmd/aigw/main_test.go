// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"
)

func Test_doMain(t *testing.T) {
	tests := []struct {
		name         string
		args         []string
		env          map[string]string
		rf           runFn
		hf           healthcheckFn
		expOut       string
		expPanicCode *int
	}{
		{
			name: "help",
			args: []string{"--help"},
			expOut: `Usage: aigw <command>

Envoy AI Gateway CLI

Flags:
  -h, --help    Show context-sensitive help.

Commands:
  version
    Show version.

  run [<path>] [flags]
    Run the AI Gateway locally for given configuration.

  healthcheck [flags]
    Docker HEALTHCHECK command.

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
			name:         "version help",
			args:         []string{"version", "--help"},
			expPanicCode: ptr.To(0),
			expOut: `Usage: aigw version

Show version.

Flags:
  -h, --help    Show context-sensitive help.
`,
		},
		{
			name:         "run no arg",
			args:         []string{"run"},
			rf:           func(context.Context, cmdRun, runOpts, io.Writer, io.Writer) error { return nil },
			expPanicCode: ptr.To(80),
		},
		{
			name: "run with OpenAI env",
			args: []string{"run"},
			env:  map[string]string{"OPENAI_API_KEY": "dummy-key"},
			rf:   func(context.Context, cmdRun, runOpts, io.Writer, io.Writer) error { return nil },
		},
		{
			name: "run help",
			args: []string{"run", "--help"},
			rf:   func(context.Context, cmdRun, runOpts, io.Writer, io.Writer) error { return nil },
			expOut: `Usage: aigw run [<path>] [flags]

Run the AI Gateway locally for given configuration.

Arguments:
  [<path>]    Path to the AI Gateway configuration yaml file. Optional when at
              least OPENAI_API_KEY is set.

Flags:
  -h, --help               Show context-sensitive help.

      --debug              Enable debug logging emitted to stderr.
      --admin-port=1064    HTTP port for the admin server (serves /metrics and
                           /health endpoints).
`,
			expPanicCode: ptr.To(0),
		},
		{
			name: "run with path",
			args: []string{"run", "./path"},
			rf: func(_ context.Context, c cmdRun, _ runOpts, _, _ io.Writer) error {
				abs, err := filepath.Abs("./path")
				require.NoError(t, err)
				require.Equal(t, abs, c.Path)
				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.env {
				t.Setenv(k, v)
			}
			out := &bytes.Buffer{}
			if tt.expPanicCode != nil {
				require.PanicsWithValue(t, *tt.expPanicCode, func() {
					doMain(t.Context(), out, os.Stderr, tt.args, func(code int) { panic(code) }, tt.rf, tt.hf)
				})
			} else {
				doMain(t.Context(), out, os.Stderr, tt.args, nil, tt.rf, tt.hf)
			}
			require.Equal(t, tt.expOut, out.String())
		})
	}
}
