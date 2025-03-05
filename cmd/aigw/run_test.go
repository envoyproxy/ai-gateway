package main

import (
	"github.com/stretchr/testify/require"
	"os"
	"testing"
)

func TestRun_default(t *testing.T) {
	require.NoError(t, run(cmdRun{}, os.Stdout, os.Stderr))
}
