package main

import (
	"bytes"
	"github.com/stretchr/testify/require"
	"io"
	"os"
	"testing"
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
			tf: func(c cmdTranslate, stdout, stderr io.Writer) error {
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
