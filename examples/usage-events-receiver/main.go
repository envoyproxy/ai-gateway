// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Command usage-events-receiver is a reference implementation of the UsageEventSink HTTP
// receiver described in docs/proposals/012-per-request-ai-usage-event-export/proposal.md.
// It is meant to be deployed as a sidecar (or DaemonSet) next to Envoy AI Gateway, addressed
// over loopback, and durably acknowledges usage events before responding.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/envoyproxy/ai-gateway/examples/usage-events-receiver/receiver"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8090", "address the HTTP sink listens on")
	storeDir := flag.String("store-dir", "", "directory JetStream persists usage events to (required)")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if *storeDir == "" {
		logger.Error("-store-dir is required")
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv, err := receiver.New(ctx, receiver.Config{StoreDir: *storeDir})
	if err != nil {
		logger.Error("failed to start receiver", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := srv.Close(); err != nil {
			logger.Error("error during shutdown", "error", err)
		}
	}()

	httpServer := &http.Server{
		Addr:              *addr,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			logger.Error("error shutting down HTTP server", "error", err)
		}
	}()

	logger.Info("usage-events-receiver listening", "addr", *addr, "store_dir", *storeDir)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logger.Error("HTTP server exited with error", "error", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, "shutdown complete")
}
