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
		tf           translateFn
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

  translate <path> ... [flags]
    Translate yaml files containing AI Gateway resources to Envoy Gateway and
    Kubernetes resources. The translated resources are written to stdout.

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
			name: "translate",
			args: []string{"translate", "path1", "path2", "--debug"},
			tf: func(_ context.Context, c cmdTranslate, _, _ io.Writer) error {
				cwd, err := os.Getwd()
				require.NoError(t, err)
				require.Equal(t, []string{cwd + "/path1", cwd + "/path2"}, c.Paths)
				return nil
			},
		},
		{
			name: "translate no arg",
			args: []string{"translate"},
			tf:   func(_ context.Context, _ cmdTranslate, _, _ io.Writer) error { return nil },
			// Looks like the kong library follows the "semantic exit code" as in
			// https://github.com/square/exit?tab=readme-ov-file#about
			expPanicCode: ptr.To(80),
		},
		{
			name: "translate with help",
			args: []string{"translate", "--help"},
			expOut: `Usage: aigw translate <path> ... [flags]

Translate yaml files containing AI Gateway resources to Envoy Gateway and
Kubernetes resources. The translated resources are written to stdout.

Arguments:
  <path> ...    Paths to yaml files to translate.

Flags:
  -h, --help     Show context-sensitive help.

      --debug    Enable debug logging emitted to stderr.
`,
			expPanicCode: ptr.To(0),
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
              least OPENAI_API_KEY or AZURE_OPENAI_API_KEY is set.

Flags:
  -h, --help                 Show context-sensitive help.

      --debug                Enable debug logging emitted to stderr.
      --admin-port=1064      HTTP port for the admin server (serves /metrics and
                             /health endpoints).
      --mcp-config=STRING    (Optional) Path to the file containing the list of
                             MCP servers. When this is given, any other given
                             config file is ignored.
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
					doMain(t.Context(), out, os.Stderr, tt.args, func(code int) { panic(code) }, tt.tf, tt.rf, tt.hf)
				})
			} else {
				doMain(t.Context(), out, os.Stderr, tt.args, nil, tt.tf, tt.rf, tt.hf)
			}
			require.Equal(t, tt.expOut, out.String())
		})
	}
}

func TestCmdRun_Validate(t *testing.T) {
	tests := []struct {
		name          string
		cmd           cmdRun
		envVars       map[string]string
		expectedError string
	}{
		{
			name:          "no config and no env vars",
			cmd:           cmdRun{Path: ""},
			envVars:       map[string]string{},
			expectedError: "you must supply at least OPENAI_API_KEY or AZURE_OPENAI_API_KEY or a config file path",
		},
		{
			name:    "config path provided",
			cmd:     cmdRun{Path: "/path/to/config.yaml"},
			envVars: map[string]string{},
		},
		{
			name: "OPENAI_API_KEY set",
			cmd:  cmdRun{Path: ""},
			envVars: map[string]string{
				"OPENAI_API_KEY": "sk-test",
			},
		},
		{
			name: "AZURE_OPENAI_API_KEY set",
			cmd:  cmdRun{Path: ""},
			envVars: map[string]string{
				"AZURE_OPENAI_API_KEY": "azure-key",
			},
		},
		{
			name: "both API keys set",
			cmd:  cmdRun{Path: ""},
			envVars: map[string]string{
				"OPENAI_API_KEY":       "sk-test",
				"AZURE_OPENAI_API_KEY": "azure-key",
			},
		},
		{
			name: "config path and OPENAI_API_KEY both set",
			cmd:  cmdRun{Path: "/path/to/config.yaml"},
			envVars: map[string]string{
				"OPENAI_API_KEY": "sk-test",
			},
		},
		{
			name: "config path and AZURE_OPENAI_API_KEY both set",
			cmd:  cmdRun{Path: "/path/to/config.yaml"},
			envVars: map[string]string{
				"AZURE_OPENAI_API_KEY": "azure-key",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			err := tt.cmd.Validate()

			if tt.expectedError != "" {
				require.EqualError(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
			}
		})
	}
}
