// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func Test_healthcheck(t *testing.T) {
	pid := os.Getpid()

	t.Run("returns error when no envoy subprocess", func(t *testing.T) {
		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		defer cancel()
		err := doHealthcheck(ctx, pid, logger)
		require.EqualError(t, err, "timeout waiting for Envoy process: no Envoy process found")
		// Contains not Equal because there's a timestamp
		require.Contains(t, buf.String(), "Failed to find Envoy admin server")
	})

	t.Run("returns nil when ready", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			require.Equal(t, "/ready", r.URL.Path)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("live"))
		}))
		defer server.Close()

		u, err := url.Parse(server.URL)
		require.NoError(t, err)
		port, err := strconv.Atoi(u.Port())
		require.NoError(t, err)

		adminFile := filepath.Join(t.TempDir(), "admin-address.txt")
		require.NoError(t, os.WriteFile(adminFile, []byte(fmt.Sprintf("127.0.0.1:%d", port)), 0o600))

		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()

		cmdStr := fmt.Sprintf("sleep 30 && echo -- --admin-address-path %s", adminFile)
		cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)
		require.NoError(t, cmd.Start())
		defer func() {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}()

		time.Sleep(100 * time.Millisecond)

		var buf bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&buf, nil))
		err = doHealthcheck(t.Context(), pid, logger)
		require.NoError(t, err)
		require.Empty(t, buf)
	})
}
