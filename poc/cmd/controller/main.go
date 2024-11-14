package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/envoyproxy/gateway/proto/extension"
	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/controller"
	"github.com/tetratelabs/ai-gateway/internal/extensionserver"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit/sotw"
	"github.com/tetratelabs/ai-gateway/internal/version"
)

var (
	port           = flag.String("port", ":1063", "gRPC port for the extension server")
	monitoringAddr = flag.String("monitoringAddr", ":9090", "port for the monitoring server")
	extprocImage   = flag.String("extprocImage", "ghcr.io/tetratelabs/ai-gateway-extproc:latest",
		"image for the external processor")
	ratelimitAddr = flag.String("ratelimitAddr", "ratelimit.ai-gateway-system:8081", "address for the rate limit service")
	logLevel      = flag.String("logLevel", "info", "log level")
)

func main() {
	flag.Parse()
	rlHost, rlPortStr, err := net.SplitHostPort(*ratelimitAddr)
	if err != nil {
		fmt.Println("Failed to split ratelimit host port: ", err)
		os.Exit(-1)
	}
	rlPort, err := strconv.Atoi(rlPortStr)
	if err != nil {
		fmt.Println("Failed to convert ratelimit port: ", err)
		os.Exit(-1)
	}

	fmt.Println("Version: ", version.Get())
	ctx, cancel := context.WithCancel(context.Background())
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalsChan
		cancel()
	}()

	lis, err := net.Listen("tcp", *port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	k8sConfig, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("failed to get k8s config: %v", err)
	}

	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		log.Fatalf("failed to unmarshal log level: %v", err)
	}
	l := logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))
	klog.SetLogger(l)

	rlChan := make(chan *aigv1a1.LLMRouteList)
	if err := controller.StartController(ctx, l, *logLevel, rlChan, *monitoringAddr, k8sConfig, *extprocImage, *ratelimitAddr); err != nil {
		log.Fatalf("failed to start controller: %v", err)
	}

	l.Info("starting extension server", "port", *port)
	s := grpc.NewServer()

	srv := extensionserver.New(l, rlHost, uint32(rlPort))
	rlcServer := sotw.NewServer(s, l)

	extension.RegisterEnvoyGatewayExtensionServer(s, srv)
	grpc_health_v1.RegisterHealthServer(s, srv)
	rlcServer.Start(ctx, rlChan)

	// Trigger an initial configuration to make RateLimit server healthy.
	rlChan <- &aigv1a1.LLMRouteList{}

	go func() {
		<-ctx.Done()
		s.GracefulStop()
	}()
	_ = s.Serve(lis)
}
