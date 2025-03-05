// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRun_default(t *testing.T) {
	require.NoError(t, run(cmdRun{}, os.Stdout, os.Stderr))
}
