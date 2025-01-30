package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/envoyproxy/gateway/proto/extension"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/envoyproxy/ai-gateway/internal/controller"
	"github.com/envoyproxy/ai-gateway/internal/extensionserver"
)

var (
	flagExtProcLogLevel = flag.String("extProcLogLevel",
		"info", "The log level for the external processor. One of 'debug', 'info', 'warn', or 'error'.")
	flagExtProcImage = flag.String("extProcImage",
		"envoyproxy/envoy", "The image for the external processor")
	flagEnableLeaderElection = flag.Bool("enableLeaderElection",
		true, "Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flagLogLevel = flag.String("logLevel",
		"info", "The log level for the controller manager. One of 'debug', 'info', 'warn', or 'error'.")
	flagExtensionServerPort = flag.String("port", ":1063",
		"gRPC port for the extension server")
)

// parseAndValidateFlags parses the program flags and validates them.
func parseAndValidateFlags() (zapLevel zapcore.Level, err error) {
	flag.Parse()
	var level slog.Level
	if err = level.UnmarshalText([]byte(*flagExtProcLogLevel)); err != nil {
		err = fmt.Errorf("invalid external processor log level: %s", *flagExtProcLogLevel)
		return
	}

	if err = zapLevel.UnmarshalText([]byte(*flagLogLevel)); err != nil {
		err = fmt.Errorf("invalid log level: %s", *flagLogLevel)
		return
	}
	return
}

func main() {
	setupLog := ctrl.Log.WithName("setup")

	zapLogLevel, err := parseAndValidateFlags()
	if err != nil {
		setupLog.Error(err, "failed to parse flags")
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true, Level: zapLogLevel})))
	k8sConfig, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "failed to get k8s config")
	}

	lis, err := net.Listen("tcp", *flagExtensionServerPort)
	if err != nil {
		setupLog.Error(err, "failed to listen", "port", *flagExtensionServerPort)
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	// Start the extension server running alongside the controller.
	s := grpc.NewServer()
	extSrv := extensionserver.New(setupLog)
	extension.RegisterEnvoyGatewayExtensionServer(s, extSrv)
	grpc_health_v1.RegisterHealthServer(s, extSrv)
	go func() {
		<-ctx.Done()
		s.GracefulStop()
	}()
	go func() {
		if err := s.Serve(lis); err != nil {
			setupLog.Error(err, "failed to serve extension server")
		}
	}()

	// Start the controller.
	if err := controller.StartControllers(ctx, k8sConfig, ctrl.Log.WithName("controller"), controller.Options{
		ExtProcImage:         *flagExtProcImage,
		ExtProcLogLevel:      *flagExtProcLogLevel,
		EnableLeaderElection: *flagEnableLeaderElection,
	}); err != nil {
		setupLog.Error(err, "failed to start controller")
	}
}
