// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/envoyproxy/ai-gateway/internal/aigw"
)

// healthcheck performs looks up the Envoy subprocess, gets its admin port,
// and returns no error when ready.
func healthcheck(ctx context.Context, _, stderr io.Writer) error {
	// Give up to 1 second for the health check
	ctx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{}))
	// In docker, pid 1 is the aigw process
	return doHealthcheck(ctx, 1, logger)
}

func doHealthcheck(ctx context.Context, aigwPid int, logger *slog.Logger) error {
	envoyAdmin, err := aigw.NewEnvoyAdminClient(ctx, aigwPid, 0)
	if err != nil {
		logger.Error("Failed to find Envoy admin server", "error", err)
		return err
	} else if err = envoyAdmin.IsReady(ctx); err != nil {
		logger.Error("Envoy admin server is not ready", "adminPort", envoyAdmin.Port(), "error", err)
		return err
	}
	return err
}
