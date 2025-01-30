package mainlib

import (
	"context"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/envoyproxy/ai-gateway/internal/extproc"
	"github.com/envoyproxy/ai-gateway/internal/version"
)

// parseAndValidateFlags parses and validates the flags passed to the external processor.
func parseAndValidateFlags(args []string) (configPath, addr string, logLevel slog.Level, err error) {
	fs := flag.NewFlagSet("AI Gateway External Processor", flag.ContinueOnError)
	configPathPtr := fs.String(
		"configPath",
		"",
		"path to the configuration file. The file must be in YAML format specified in filterconfig.Config type. "+
			"The configuration file is watched for changes.",
	)
	extProcAddrPtr := fs.String(
		"extProcAddr",
		":1063",
		"gRPC address for the external processor. For example, :1063 or unix:///tmp/ext_proc.sock",
	)
	logLevelPtr := fs.String(
		"logLevel",
		"info", "log level for the external processor. One of 'debug', 'info', 'warn', or 'error'.",
	)

	if err = fs.Parse(args); err != nil {
		err = fmt.Errorf("failed to parse flags: %w", err)
		return
	}

	if *configPathPtr == "" {
		err = fmt.Errorf("configPath must be provided")
		return
	}

	if err = logLevel.UnmarshalText([]byte(*logLevelPtr)); err != nil {
		err = fmt.Errorf("failed to unmarshal log level: %w", err)
		return
	}

	configPath = *configPathPtr
	addr = *extProcAddrPtr
	return
}

// Main is a main function for the external processor exposed
// for allowing users to build their own external processor.
func Main() {
	configPath, extProcAddr, level, err := parseAndValidateFlags(os.Args[1:])
	if err != nil {
		log.Fatalf("failed to parse and validate flags: %v", err)
	}

	l := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))

	l.Info("starting external processor",
		slog.String("version", version.Version),
		slog.String("address", extProcAddr),
		slog.String("configPath", configPath),
	)

	ctx, cancel := context.WithCancel(context.Background())
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalsChan
		cancel()
	}()

	lis, err := net.Listen(listenAddress(extProcAddr))
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	server, err := extproc.NewServer[*extproc.Processor](l, extproc.NewProcessor)
	if err != nil {
		log.Fatalf("failed to create external processor server: %v", err)
	}

	if err := extproc.StartConfigWatcher(ctx, configPath, server, l, time.Second*5); err != nil {
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

// listenAddress returns the network and address for the given address flag.
func listenAddress(addrFlag string) (string, string) {
	if strings.HasPrefix(addrFlag, "unix://") {
		return "unix", strings.TrimPrefix(addrFlag, "unix://")
	}
	return "tcp", addrFlag
}
