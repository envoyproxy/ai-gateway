package main

import (
	"flag"
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
func getOptions() (opts controller.Options, extensionServerPort *string) {
	opts = defaultOptions()
	flag.StringVar(&opts.ExtProcImage, "extprocImage", opts.ExtProcImage, "The image for the external processor")
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
	options, extensionServerPort := getOptions()
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
	if err := controller.StartControllers(ctx, k8sConfig, setupLog, options); err != nil {
		setupLog.Error(err, "failed to start controller")
	}
}
