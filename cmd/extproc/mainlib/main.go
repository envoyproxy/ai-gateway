// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mainlib

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	metricsdk "go.opentelemetry.io/otel/sdk/metric"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/envoyproxy/ai-gateway/internal/extproc"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/version"
)

// extProcFlags is the struct that holds the flags passed to the external processor.
type extProcFlags struct {
	configPath  string     // path to the configuration file.
	extProcAddr string     // gRPC address for the external processor.
	logLevel    slog.Level // log level for the external processor.
	metricsAddr string     // HTTP address for the metrics server.
}

// parseAndValidateFlags parses and validates the flas passed to the external processor.
func parseAndValidateFlags(args []string) (extProcFlags, error) {
	var (
		flags extProcFlags
		errs  []error
		fs    = flag.NewFlagSet("AI Gateway External Processor", flag.ContinueOnError)
	)

	fs.StringVar(&flags.configPath,
		"configPath",
		"",
		"path to the configuration file. The file must be in YAML format specified in filterapi.Config type. "+
			"The configuration file is watched for changes.",
	)
	fs.StringVar(&flags.extProcAddr,
		"extProcAddr",
		":1063",
		"gRPC address for the external processor. For example, :1063 or unix:///tmp/ext_proc.sock.",
	)
	logLevelPtr := fs.String(
		"logLevel",
		"info",
		"log level for the external processor. One of 'debug', 'info', 'warn', or 'error'.",
	)
	fs.StringVar(&flags.metricsAddr, "metricsAddr", ":9190", "HTTP address for the metrics server.")

	if err := fs.Parse(args); err != nil {
		return extProcFlags{}, fmt.Errorf("failed to parse extProcFlags: %w", err)
	}

	if flags.configPath == "" {
		errs = append(errs, fmt.Errorf("configPath must be provided"))
	}
	if err := flags.logLevel.UnmarshalText([]byte(*logLevelPtr)); err != nil {
		errs = append(errs, fmt.Errorf("failed to unmarshal log level: %w", err))
	}

	return flags, errors.Join(errs...)
}

// Main is a main function for the external processor exposed
// for allowing users to build their own external processor.
func Main() {
	flags, err := parseAndValidateFlags(os.Args[1:])
	if err != nil {
		log.Fatalf("failed to parse and validate extProcFlags: %v", err)
	}

	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: flags.logLevel}))

	l.Info("starting external processor",
		slog.String("version", version.Version),
		slog.String("address", flags.extProcAddr),
		slog.String("configPath", flags.configPath),
	)

	ctx, cancel := context.WithCancel(context.Background())
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalsChan
		cancel()
	}()

	lis, err := net.Listen(listenAddress(flags.extProcAddr))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	metricsServer, meter := startMetricsServer(flags.metricsAddr, l)

	server, err := extproc.NewServer(l)
	if err != nil {
		log.Fatalf("failed to create external processor server: %v", err)
	}
	server.Register("/v1/chat/completions", extproc.ChatCompletionProcessorFactory(metrics.NewChatCompletion(meter)))
	server.Register("/v1/models", extproc.NewModelsProcessor)

	if err := extproc.StartConfigWatcher(ctx, flags.configPath, server, l, time.Second*5); err != nil {
		log.Fatalf("failed to start config watcher: %v", err)
	}

	s := grpc.NewServer()
	extprocv3.RegisterExternalProcessorServer(s, server)
	grpc_health_v1.RegisterHealthServer(s, server)

	go func() {
		<-ctx.Done()
		s.GracefulStop()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := metricsServer.Shutdown(shutdownCtx); err != nil {
			l.Error("Failed to shutdown metrics server gracefully", "error", err)
		}
	}()

	_ = s.Serve(lis)
}

// listenAddress returns the network and address for the given address flag.
func listenAddress(addrFlag string) (string, string) {
	if strings.HasPrefix(addrFlag, "unix://") {
		return "unix", strings.TrimPrefix(addrFlag, "unix://")
	}
	return "tcp", addrFlag
}

// startMetricsServer starts the HTTP server for Prometheus metrics.
func startMetricsServer(addr string, logger *slog.Logger) (*http.Server, metric.Meter) {
	registry := prometheus.NewRegistry()
	exporter, err := otelprom.New(otelprom.WithRegisterer(registry))
	if err != nil {
		log.Fatal("failed to create metrics exporter")
	}
	provider := metricsdk.NewMeterProvider(metricsdk.WithReader(exporter))
	meter := provider.Meter("envoyproxy/ai-gateway")

	// Create a new HTTP server for metrics.
	mux := http.NewServeMux()

	// Register the metrics handler.
	mux.Handle("/metrics", promhttp.HandlerFor(
		registry,
		promhttp.HandlerOpts{
			EnableOpenMetrics: true,
		},
	))

	// Add a simple health check endpoint.
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("Starting metrics server", "address", addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("Metrics server failed", "error", err)
		}
	}()

	return server, meter
}
