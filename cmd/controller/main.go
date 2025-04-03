// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	"github.com/envoyproxy/ai-gateway/internal/controller"
	"github.com/envoyproxy/ai-gateway/internal/extensionserver"
)

// parseAndValidateFlags parses the command-line arguments provided in args,
// validates them, and returns the parsed configuration.
func parseAndValidateFlags(args []string) (
	extProcLogLevel string,
	extProcImage string,
	enableLeaderElection bool,
	logLevel zapcore.Level,
	extensionServerPort string,
	enableInfExt bool,
	err error,
) {
	fs := flag.NewFlagSet("AI Gateway Controller", flag.ContinueOnError)

	extProcLogLevelPtr := fs.String(
		"extProcLogLevel",
		"info",
		"The log level for the external processor. One of 'debug', 'info', 'warn', or 'error'.",
	)
	extProcImagePtr := fs.String(
		"extProcImage",
		"docker.io/envoyproxy/ai-gateway-extproc:latest",
		"The image for the external processor",
	)
	enableLeaderElectionPtr := fs.Bool(
		"enableLeaderElection",
		true,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.",
	)
	logLevelPtr := fs.String(
		"logLevel",
		"info",
		"The log level for the controller manager. One of 'debug', 'info', 'warn', or 'error'.",
	)
	extensionServerPortPtr := fs.String(
		"port",
		":1063",
		"gRPC port for the extension server",
	)
	enableInfExtPtr := fs.Bool(
		"enableInferenceExtension",
		false,
		"Enable the Gateway Inference Extetension. When enabling this, the CRDs for the InferenceModel and InferencePool must be installed prior to starting the controller.",
	)

	if err = fs.Parse(args); err != nil {
		err = fmt.Errorf("failed to parse flags: %w", err)
		return
	}

	var slogLevel slog.Level
	if err = slogLevel.UnmarshalText([]byte(*extProcLogLevelPtr)); err != nil {
		err = fmt.Errorf("invalid external processor log level: %q", *extProcLogLevelPtr)
		return
	}

	var zapLogLevel zapcore.Level
	if err = zapLogLevel.UnmarshalText([]byte(*logLevelPtr)); err != nil {
		err = fmt.Errorf("invalid log level: %q", *logLevelPtr)
		return
	}
	return *extProcLogLevelPtr, *extProcImagePtr, *enableLeaderElectionPtr, zapLogLevel, *extensionServerPortPtr, *enableInfExtPtr, nil
}

func main() {
	setupLog := ctrl.Log.WithName("setup")

	flagExtProcLogLevel,
		flagExtProcImage,
		flagEnableLeaderElection,
		zapLogLevel,
		flagExtensionServerPort,
		enableInfExt,
		err := parseAndValidateFlags(os.Args[1:])
	if err != nil {
		setupLog.Error(err, "failed to parse and validate flags")
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true, Level: zapLogLevel})))
	k8sConfig, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "failed to get k8s config")
	}

	lis, err := net.Listen("tcp", flagExtensionServerPort)
	if err != nil {
		setupLog.Error(err, "failed to listen", "port", flagExtensionServerPort)
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()

	// Start the extension server running alongside the controller.
	s := grpc.NewServer()
	extSrv := extensionserver.New(ctrl.Log)
	egextension.RegisterEnvoyGatewayExtensionServer(s, extSrv)
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
		ExtProcImage:         flagExtProcImage,
		ExtProcLogLevel:      flagExtProcLogLevel,
		EnableLeaderElection: flagEnableLeaderElection,
		EnableInfExt:         enableInfExt,
	}); err != nil {
		setupLog.Error(err, "failed to start controller")
	}
}
