package main

import (
	"flag"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/envoyproxy/ai-gateway/internal/controller"
)

var setupLog = ctrl.Log.WithName("setup")

// defaultOptions returns the default values for the program options.
func defaultOptions() controller.Options {
	return controller.Options{
		ExtProcImage:         "ghcr.io/envoyproxy/ai-gateway-extproc:latest",
		EnableLeaderElection: false,
	}
}

// getOptions parses the program flags and returns them as Options.
func getOptions() controller.Options {
	opts := defaultOptions()
	flag.StringVar(&opts.ExtProcImage, "extprocImage", opts.ExtProcImage, "The image for the external processor")
	flag.BoolVar(&opts.EnableLeaderElection, "leader-elect", opts.EnableLeaderElection,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	zapOpts := zap.Options{
		Development: true,
	}
	zapOpts.BindFlags(flag.CommandLine)
	opts.ZapOptions = zapOpts
	flag.Parse()
	return opts
}

func main() {
	options := getOptions()
	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&options.ZapOptions)))
	k8sConfig, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "failed to get k8s config")
	}

	// TODO: starts the extension server?

	if err := controller.StartControllers(ctrl.SetupSignalHandler(), k8sConfig, setupLog, options); err != nil {
		setupLog.Error(err, "failed to start controller")
	}
}
