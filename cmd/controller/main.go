package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/envoyproxy/gateway/proto/extension"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/envoyproxy/ai-gateway/internal/controller"
	"github.com/envoyproxy/ai-gateway/internal/extensionserver"
)

var setupLog = ctrl.Log.WithName("setup")

// defaultOptions returns the default values for the program options.
func defaultOptions() controller.Options {
	return controller.Options{
		ExtProcImage:         "ghcr.io/envoyproxy/ai-gateway/extproc:latest",
		EnableLeaderElection: false,
	}
}

// getOptions parses the program flags and returns them as Options.
func getOptions() (opts controller.Options, extensionServerPort *string, err error) {
	opts = defaultOptions()
	flag.StringVar(&opts.ExtProcLogLevel, "extProcLogLevel", opts.ExtProcLogLevel,
		"The log level for the external processor. Either 'debug', 'info', 'warn', or 'error'.")
	var level slog.Level
	if err = level.UnmarshalText([]byte(opts.ExtProcLogLevel)); err != nil {
		return opts, nil, fmt.Errorf("failed to unmarshal log level: %w", err)
	}
	flag.StringVar(&opts.ExtProcImage, "extProcImage", opts.ExtProcImage, "The image for the external processor")
	flag.BoolVar(&opts.EnableLeaderElection, "leader-elect", opts.EnableLeaderElection,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	extensionServerPort = flag.String("port", ":1063", "gRPC port for the extension server")
	zapOpts := zap.Options{Development: true}
	zapOpts.BindFlags(flag.CommandLine)
	opts.ZapOptions = zapOpts
	flag.Parse()
	return
}

func main() {
	options, extensionServerPort, err := getOptions()
	if err != nil {
		setupLog.Error(err, "failed to get options")
		os.Exit(1)
	}
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&options.ZapOptions)))
	k8sConfig, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "failed to get k8s config")
	}

	lis, err := net.Listen("tcp", *extensionServerPort)
	if err != nil {
		setupLog.Error(err, "failed to listen", "port", *extensionServerPort)
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
	if err := controller.StartControllers(ctx, k8sConfig, ctrl.Log.WithName("controller"), options); err != nil {
		setupLog.Error(err, "failed to start controller")
	}
}
