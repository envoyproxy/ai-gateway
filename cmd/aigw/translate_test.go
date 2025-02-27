// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_translate(t *testing.T) {
	for _, tc := range []struct {
		name, in, out string
	}{
		{
			name: "basic",
			in:   "testdata/translate_basic.in.yaml",
			out:  "testdata/translate_basic.out.yaml",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buf := &bytes.Buffer{}
			err := translate(cmdTranslate{Paths: []string{tc.in}}, buf, os.Stderr)
			require.NoError(t, err)
			exp, err := os.ReadFile(tc.out)
			require.NoError(t, err)
			fmt.Println(buf.String())
			require.Equal(t, string(exp), buf.String())
		})
	}
}
