package pprof

import (
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRun_disabled(t *testing.T) {
	t.Setenv(DisableEnvVarKey, "anything")
	ctx, cancel := context.WithCancel(context.Background())
	Run(ctx)
	// Try accessing the pprof server here if needed.
	resp, err := http.Get("http://localhost:6060/debug/pprof/")
	require.Error(t, err)
	require.Nil(t, resp)
	cancel()
}

func TestRun_enabled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	Run(ctx)
	// Try accessing the pprof server here if needed.
	resp, err := http.Get("http://localhost:6060/debug/pprof/cmdline")
	require.NoError(t, err)
	require.NotNil(t, resp)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Contains(t, string(body),
		// Test binary name should be present in the cmdline output.
		"pprof.test")
	cancel()
}
