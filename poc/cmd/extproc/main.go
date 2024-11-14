package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"sigs.k8s.io/controller-runtime/pkg/metrics"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/extproc"
	"github.com/tetratelabs/ai-gateway/internal/version"
)

var (
	extProcPort    = flag.String("extProcPort", ":1063", "gRPC port for the external processor")
	monitoringAddr = flag.String("monitoringAddr", ":9090", "port for the monitoring server")
	configuration  = flag.String("configuration", "", "base64 encoded configuration")
	rateLimitAddr  = flag.String("rateLimitAddr", "ratelimit.ai-gateway-system:8081", "address for the rate limit service")
	logLevel       = flag.String("logLevel", "info", "log level")
)

func main() {
	flag.Parse()
	fmt.Println("Version: ", version.Get())
	if *configuration == "" {
		log.Fatalf("configuration must be provided")
	}

	route, err := unmarshalConfig(*configuration)
	if err != nil {
		log.Fatalf("failed to unmarshal configuration: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalsChan
		cancel()
	}()

	lis, err := net.Listen("tcp", *extProcPort)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		log.Fatalf("failed to unmarshal log level: %v", err)
	}
	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))

	server, err := extproc.NewServer(ctx, *rateLimitAddr, route, l)
	if err != nil {
		log.Fatalf("failed to create external processor server: %v", err)
	}
	defer server.Close()

	handlers := http.NewServeMux()
	handlers.Handle("/metrics", promhttp.InstrumentMetricHandler(
		metrics.Registry, promhttp.HandlerFor(metrics.Registry, promhttp.HandlerOpts{}),
	))
	metricsServer := &http.Server{
		Handler:           handlers,
		Addr:              *monitoringAddr,
		ReadTimeout:       5 * time.Second,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       15 * time.Second,
	}
	go func() {
		if err := metricsServer.ListenAndServe(); err != nil {
			log.Fatalf("start metrics server failed: %v", err)
		}
	}()

	s := grpc.NewServer()
	extprocv3.RegisterExternalProcessorServer(s, server)
	grpc_health_v1.RegisterHealthServer(s, server)
	go func() {
		<-ctx.Done()
		s.GracefulStop()
	}()
	_ = s.Serve(lis)
}

func unmarshalConfig(base64encoded string) (*aigv1a1.LLMRoute, error) {
	encoded, err := base64.StdEncoding.DecodeString(base64encoded)
	if err != nil {
		return nil, err
	}
	var route aigv1a1.LLMRoute
	if err := json.Unmarshal(encoded, &route); err != nil {
		return nil, err
	}
	return &route, nil
}
