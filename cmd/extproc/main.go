package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/envoyproxy/ai-gateway/internal/extproc"
	"github.com/envoyproxy/ai-gateway/internal/version"
)

var (
	configPath = flag.String("configPath", "", "path to the configuration file. "+
		"The file must be in YAML format speified in extprocconfig.Config type. The configuration file is watched for changes.")
	// TODO: unix domain socket support.
	extProcPort = flag.String("extProcPort", ":1063", "gRPC port for the external processor")
	logLevel    = flag.String("logLevel", "info", "log level")
)

func main() {
	flag.Parse()

	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		log.Fatalf("failed to unmarshal log level: %v", err)
	}
	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))

	l.Info("starting external processor", slog.String("version", version.Version))

	if *configPath == "" {
		log.Fatal("configPath must be provided")
	}

	ctx, cancel := context.WithCancel(context.Background())
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalsChan
		cancel()
	}()

	// TODO: unix domain socket support.
	lis, err := net.Listen("tcp", *extProcPort)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	server, err := extproc.NewServer[*extproc.Processor](l, extproc.NewProcessor)
	if err != nil {
		log.Fatalf("failed to create external processor server: %v", err)
	}

	if err := extproc.StartConfigWatcher(ctx, *configPath, server, l, time.Second*5); err != nil {
		log.Fatalf("failed to start config watcher: %v", err)
	}

	s := grpc.NewServer()
	extprocv3.RegisterExternalProcessorServer(s, server)
	grpc_health_v1.RegisterHealthServer(s, server)
	go func() {
		<-ctx.Done()
		s.GracefulStop()
	}()
	_ = s.Serve(lis)
}
