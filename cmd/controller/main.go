// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"
	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	clientcfg "sigs.k8s.io/controller-runtime/pkg/client/config"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/webhook"

	"github.com/envoyproxy/ai-gateway/internal/controller"
	"github.com/envoyproxy/ai-gateway/internal/extensionserver"
)

type flags struct {
	extProcLogLevel       string
	extProcImage          string
	enableLeaderElection  bool
	logLevel              zapcore.Level
	extensionServerPort   string
	tlsCertDir            string
	tlsCertName           string
	tlsKeyName            string
	caBundleName          string
	envoyGatewayNamespace string
}

// parseAndValidateFlags parses the command-line arguments provided in args,
// validates them, and returns the parsed configuration.
func parseAndValidateFlags(args []string) (flags, error) {
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
	tlsCertDir := fs.String(
		"tlsCertDir",
		"/certs",
		"The directory containing the TLS certificate and key for the webhook server.",
	)
	caBundleName := fs.String(
		"caBundleName",
		"ca.crt",
		"The name of the CA bundle file.",
	)
	tlsCertName := fs.String(
		"tlsCertName",
		"tls.crt",
		"The name of the TLS certificate file.",
	)
	tlsKeyName := fs.String(
		"tlsKeyName",
		"tls.key",
		"The name of the TLS key file.",
	)
	envoyGatewayNamespace := fs.String(
		"envoyGatewayNamespace",
		"envoy-gateway-system",
		"The namespace where the Envoy Gateway system components are installed.",
	)

	if err := fs.Parse(args); err != nil {
		err = fmt.Errorf("failed to parse flags: %w", err)
		return flags{}, err
	}

	var slogLevel slog.Level
	if err := slogLevel.UnmarshalText([]byte(*extProcLogLevelPtr)); err != nil {
		err = fmt.Errorf("invalid external processor log level: %q", *extProcLogLevelPtr)
		return flags{}, err
	}

	var zapLogLevel zapcore.Level
	if err := zapLogLevel.UnmarshalText([]byte(*logLevelPtr)); err != nil {
		err = fmt.Errorf("invalid log level: %q", *logLevelPtr)
		return flags{}, err
	}
	return flags{
		extProcLogLevel:       *extProcLogLevelPtr,
		extProcImage:          *extProcImagePtr,
		enableLeaderElection:  *enableLeaderElectionPtr,
		logLevel:              zapLogLevel,
		extensionServerPort:   *extensionServerPortPtr,
		tlsCertDir:            *tlsCertDir,
		tlsCertName:           *tlsCertName,
		tlsKeyName:            *tlsKeyName,
		caBundleName:          *caBundleName,
		envoyGatewayNamespace: *envoyGatewayNamespace,
	}, nil
}

func main() {
	setupLog := ctrl.Log.WithName("setup")

	flags, err := parseAndValidateFlags(os.Args[1:])
	if err != nil {
		setupLog.Error(err, "failed to parse and validate flags")
		os.Exit(1)
	}

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&zap.Options{Development: true, Level: flags.logLevel})))
	k8sConfig, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "failed to get k8s config")
	}

	lis, err := net.Listen("tcp", flags.extensionServerPort)
	if err != nil {
		setupLog.Error(err, "failed to listen", "port", flags.extensionServerPort)
		os.Exit(1)
	}

	ctx := ctrl.SetupSignalHandler()
	mgrOpts := ctrl.Options{
		Scheme:           controller.Scheme,
		LeaderElection:   flags.enableLeaderElection,
		LeaderElectionID: "envoy-ai-gateway-controller",
		WebhookServer: webhook.NewServer(webhook.Options{
			CertDir:  flags.tlsCertDir,
			CertName: flags.tlsCertName,
			KeyName:  flags.tlsKeyName,
			Port:     9443,
		}),
	}
	mgr, err := ctrl.NewManager(k8sConfig, mgrOpts)
	if err != nil {
		setupLog.Error(err, "failed to create manager")
		os.Exit(1)
	}

	cli, err := client.New(clientcfg.GetConfigOrDie(), client.Options{Scheme: controller.Scheme})
	if err != nil {
		setupLog.Error(err, "failed to create client")
		os.Exit(1)
	}
	if err := maybePatchAdmissionWebhook(ctx, cli, filepath.Join(flags.tlsCertDir, flags.caBundleName)); err != nil {
		setupLog.Error(err, "failed to patch admission webhook")
		os.Exit(1)
	}

	// Start the extension server running alongside the controller.
	const extProcUDSPath = "/etc/ai-gateway-extproc-uds/run.sock"
	s := grpc.NewServer()
	extSrv := extensionserver.New(mgr.GetClient(), ctrl.Log, extProcUDSPath)
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
	if err := controller.StartControllers(ctx, mgr, k8sConfig, ctrl.Log.WithName("controller"), controller.Options{
		ExtProcImage:          flags.extProcImage,
		ExtProcLogLevel:       flags.extProcLogLevel,
		EnableLeaderElection:  flags.enableLeaderElection,
		EnvoyGatewayNamespace: flags.envoyGatewayNamespace,
		UDSPath:               extProcUDSPath,
	}); err != nil {
		setupLog.Error(err, "failed to start controller")
	}
}

// This should be the name of the mutating webhook configuration in helm chart.
const mutatingWebhookConfigurationName = "envoy-ai-gateway-gateway-pod-mutator"

// maybePatchAdmissionWebhook checks if the CA bundle in the mutating webhook configuration installed as part of the
// helm chart is the same as the one specified in the bundlePath. If not, it updates the CA bundle in the webhook configuration.
//
// This allows users to only need to manage the secret and not take care of mutating webhook configuration.
func maybePatchAdmissionWebhook(ctx context.Context, cli client.Client, bundlePath string) error {
	webhookConfigName := fmt.Sprintf("%s.%s", mutatingWebhookConfigurationName, os.Getenv("POD_NAMESPACE"))
	webhookCfg := &admissionregistrationv1.MutatingWebhookConfiguration{}
	if err := cli.Get(ctx, client.ObjectKey{Name: webhookConfigName}, webhookCfg); err != nil {
		return fmt.Errorf("failed to get mutating webhook configuration: %w", err)
	}

	if len(webhookCfg.Webhooks) != 1 {
		return fmt.Errorf("expected 1 webhook in %s, got %d", webhookConfigName, len(webhookCfg.Webhooks))
	}

	bundle, err := os.ReadFile(bundlePath)
	if err != nil {
		return fmt.Errorf("failed to read CA bundle: %w", err)
	}
	if !bytes.Equal(webhookCfg.Webhooks[0].ClientConfig.CABundle, bundle) {
		webhookCfg.Webhooks[0].ClientConfig.CABundle = bundle
		if err := cli.Update(ctx, webhookCfg); err != nil {
			return fmt.Errorf("failed to update mutating webhook configuration: %w", err)
		}
	}
	return nil
}
