// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mainlib

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
)

// newGrpcClient creates a gRPC client connection for the provided address.
// The returned connection must be closed when no longer needed.
func newGrpcClient(addr net.Addr, opts ...grpc.DialOption) (*grpc.ClientConn, error) {
	var prefix string
	switch addr.Network() {
	case "unix":
		prefix = "unix://"
	default:
		prefix = ""
	}
	opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	return grpc.NewClient(prefix+addr.String(), opts...)
}

// startAdminServer starts an HTTP admin server on the provided listener for
// serving Prometheus metrics and health checks. It exposes two endpoints:
//   - /metrics: Serves Prometheus metrics using the provided registry.
//   - /health: Same check Envoy uses: this ExternalProcessorServer.
//
// The server returned is running in a goroutine.
func startAdminServer(lis net.Listener, logger *slog.Logger, registry prometheus.Gatherer, extprocHealth grpc_health_v1.HealthClient) *http.Server {
	mux := http.NewServeMux()

	mux.Handle("/metrics", promhttp.HandlerFor(
		registry,
		promhttp.HandlerOpts{},
	))

	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()

		resp, err := extprocHealth.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
		if err != nil {
			http.Error(w, fmt.Sprintf("health check RPC failed: %v", err), http.StatusInternalServerError)
			return
		}
		if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			http.Error(w, fmt.Sprintf("unhealthy status: %s", resp.Status), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK\n"))
	})

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		logger.Info("starting admin server", "address", lis.Addr())
		if err := server.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Admin server failed", "error", err)
		}
	}()

	return server
}
