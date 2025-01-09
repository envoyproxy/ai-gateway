package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/envoyproxy/ai-gateway/internal/controller"
)

var (
	logLevel     = flag.String("logLevel", "info", "log level")
	extProcImage = flag.String("extprocImage",
		"ghcr.io/envoyproxy/ai-gateway-extproc:latest", "image for the external processor")
)

func main() {
	flag.Parse()
	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		log.Fatalf("failed to unmarshal log level: %v", err)
	}
	l := logr.FromSlogHandler(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: level,
	}))
	klog.SetLogger(l)

	k8sConfig, err := ctrl.GetConfig()
	if err != nil {
		log.Fatalf("failed to get k8s config: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalsChan
		cancel()
	}()

	// TODO: starts the extension server?

	if err := controller.StartControllers(ctx, k8sConfig, l, *logLevel, *extProcImage, true); err != nil {
		log.Fatalf("failed to start controller: %v", err)
	}
}
