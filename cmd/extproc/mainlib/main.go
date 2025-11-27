// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mainlib

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"time"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/prometheus/client_golang/prometheus"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health/grpc_health_v1"

	"github.com/envoyproxy/ai-gateway/internal/extproc"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/mcpproxy"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing"
	"github.com/envoyproxy/ai-gateway/internal/version"
)

// extProcFlags is the struct that holds the flags passed to the external processor.
type extProcFlags struct {
	configPath                     string        // path to the configuration file.
	extProcAddr                    string        // gRPC address for the external processor.
	logLevel                       slog.Level    // log level for the external processor.
	adminPort                      int           // HTTP port for the admin server (metrics and health).
	metricsRequestHeaderAttributes string        // comma-separated key-value pairs for mapping HTTP request headers to otel metric attributes.
	metricsRequestHeaderLabels     string        // DEPRECATED: use metricsRequestHeaderAttributes instead.
	spanRequestHeaderAttributes    string        // comma-separated key-value pairs for mapping HTTP request headers to otel span attributes.
	mcpAddr                        string        // address for the MCP proxy server which can be either tcp or unix domain socket.
	mcpSessionEncryptionSeed       string        // Seed for deriving the key for encrypting MCP sessions.
	mcpWriteTimeout                time.Duration // the maximum duration before timing out writes of the MCP response.
	// rootPrefix is the root prefix for all the processors.
	rootPrefix string
	// maxRecvMsgSize is the maximum message size in bytes that the gRPC server can receive.
	maxRecvMsgSize int
	// endpointPrefixes is the comma-separated key-value pairs for endpoint prefixes.
	endpointPrefixes string
}

// parseAndValidateFlags parses and validates the flags passed to the external processor.
func parseAndValidateFlags(args []string) (extProcFlags, error) {
	var (
		flags extProcFlags
		errs  []error
		fs    = flag.NewFlagSet("AI Gateway External Processor", flag.ContinueOnError)
	)

	fs.StringVar(&flags.configPath,
		"configPath",
		"",
		"path to the configuration file. The file must be in YAML format specified in filterapi.Config type. "+
			"The configuration file is watched for changes.",
	)
	fs.StringVar(&flags.extProcAddr,
		"extProcAddr",
		":1063",
		"gRPC address for the external processor. For example, :1063 or unix:///tmp/ext_proc.sock.",
	)
	logLevelPtr := fs.String(
		"logLevel",
		"info",
		"log level for the external processor. One of 'debug', 'info', 'warn', or 'error'.",
	)
	fs.IntVar(&flags.adminPort, "adminPort", 1064, "HTTP port for the admin server (serves /metrics and /health endpoints).")
	fs.StringVar(&flags.metricsRequestHeaderAttributes,
		"metricsRequestHeaderAttributes",
		"",
		"Comma-separated key-value pairs for mapping HTTP request headers to otel metric attributes. Format: x-team-id:team.id,x-user-id:user.id.",
	)
	fs.StringVar(&flags.metricsRequestHeaderLabels,
		"metricsRequestHeaderLabels",
		"",
		"DEPRECATED: Use -metricsRequestHeaderAttributes instead. This flag will be removed in a future release.",
	)
	fs.StringVar(&flags.spanRequestHeaderAttributes,
		"spanRequestHeaderAttributes",
		"",
		"Comma-separated key-value pairs for mapping HTTP request headers to otel span attributes. Format: x-session-id:session.id,x-user-id:user.id.",
	)
	fs.StringVar(&flags.rootPrefix,
		"rootPrefix",
		"/",
		"The root path prefix for all the processors.",
	)
	fs.StringVar(&flags.endpointPrefixes,
		"endpointPrefixes",
		"",
		"Comma-separated key-value pairs for endpoint prefixes. Format: openai:/,cohere:/cohere,anthropic:/anthropic.",
	)
	fs.IntVar(&flags.maxRecvMsgSize,
		"maxRecvMsgSize",
		4*1024*1024,
		"Maximum message size in bytes that the gRPC server can receive. Default is 4MB.",
	)
	fs.StringVar(&flags.mcpAddr, "mcpAddr", "", "the address (TCP or UDS) for the MCP proxy server, such as :1063 or unix:///tmp/ext_proc.sock. Optional.")
	fs.StringVar(&flags.mcpSessionEncryptionSeed,
		"mcpSessionEncryptionSeed",
		"mcp",
		"Arbitrary string seed used to derive the MCP session encryption key. "+
			"Do not include commas as they are used as separators. You can optionally pass \"fallback\" seed after the first one to allow for key rotation. "+
			"For example: \"new-seed,old-seed-for-fallback\". The fallback seed is only used for decryption.",
	)
	fs.DurationVar(&flags.mcpWriteTimeout, "mcpWriteTimeout", 120*time.Second,
		"The maximum duration before timing out writes of the MCP response")

	if err := fs.Parse(args); err != nil {
		return extProcFlags{}, fmt.Errorf("failed to parse extProcFlags: %w", err)
	}

	// Handle deprecated flag: fall back to metricsRequestHeaderLabels if metricsRequestHeaderAttributes is not set.
	if flags.metricsRequestHeaderAttributes == "" && flags.metricsRequestHeaderLabels != "" {
		flags.metricsRequestHeaderAttributes = flags.metricsRequestHeaderLabels
	}

	if flags.configPath == "" {
		errs = append(errs, fmt.Errorf("configPath must be provided"))
	}
	if err := flags.logLevel.UnmarshalText([]byte(*logLevelPtr)); err != nil {
		errs = append(errs, fmt.Errorf("failed to unmarshal log level: %w", err))
	}
	if flags.spanRequestHeaderAttributes != "" {
		if _, err := internalapi.ParseRequestHeaderAttributeMapping(flags.spanRequestHeaderAttributes); err != nil {
			errs = append(errs, fmt.Errorf("failed to parse tracing header mapping: %w", err))
		}
	}
	if flags.endpointPrefixes != "" {
		if _, err := internalapi.ParseEndpointPrefixes(flags.endpointPrefixes); err != nil {
			errs = append(errs, fmt.Errorf("failed to parse endpoint prefixes: %w", err))
		}
	}

	return flags, errors.Join(errs...)
}

// Main is a main function for the external processor exposed
// for allowing users to build their own external processor.
//
// * ctx is the context for the external processor.
// * args are the command line arguments passed to the external processor without the program name.
// * stderr is the writer to use for standard error where the external processor will output logs.
//
// This returns an error if the external processor fails to start, or nil otherwise. When the `ctx` is canceled,
// the function will return nil.
func Main(ctx context.Context, args []string, stderr io.Writer) (err error) {
	defer func() {
		// Don't err the caller about normal shutdown scenarios.
		if errors.Is(err, context.Canceled) || errors.Is(err, grpc.ErrServerStopped) {
			err = nil
		}
	}()
	flags, err := parseAndValidateFlags(args)
	if err != nil {
		return fmt.Errorf("failed to parse and validate extProcFlags: %w", err)
	}

	l := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: flags.logLevel}))

	// Warn if deprecated flag is being used.
	if flags.metricsRequestHeaderLabels != "" {
		l.Warn("The -metricsRequestHeaderLabels flag is deprecated and will be removed in a future release. Please use -metricsRequestHeaderAttributes instead.")
	}

	l.Info("starting external processor",
		slog.String("version", version.Parse()),
		slog.String("address", flags.extProcAddr),
		slog.String("configPath", flags.configPath),
	)

	network, address := listenAddress(flags.extProcAddr)
	extProcLis, err := listen(ctx, "external processor", network, address)
	if err != nil {
		return err
	}
	if network == "unix" {
		// Change the permission of the UDS to 0775 so that the envoy process (the same group) can access it.
		err = os.Chmod(address, 0o775)
		if err != nil {
			return fmt.Errorf("failed to change UDS permission: %w", err)
		}
	}

	adminLis, err := listen(ctx, "admin server", "tcp", fmt.Sprintf(":%d", flags.adminPort))
	if err != nil {
		return err
	}

	var mcpLis net.Listener
	if flags.mcpAddr != "" {
		mcpNetwork, mcpAddress := listenAddress(flags.mcpAddr)
		mcpLis, err = listen(ctx, "mcp proxy", mcpNetwork, mcpAddress)
		if err != nil {
			return err
		}
		if mcpNetwork == "unix" {
			// Change the permission of the UDS to 0775 so that the envoy process (the same group) can access it.
			err = os.Chmod(mcpAddress, 0o775)
			if err != nil {
				return fmt.Errorf("failed to change UDS permission: %w", err)
			}
		}
		l.Info("MCP proxy is enabled", "address", flags.mcpAddr)
	}

	// Parse header mapping for metrics.
	metricsRequestHeaderAttributes, err := internalapi.ParseRequestHeaderAttributeMapping(flags.metricsRequestHeaderAttributes)
	if err != nil {
		return fmt.Errorf("failed to parse metrics header mapping: %w", err)
	}

	// Parse header mapping for tracing spans.
	spanRequestHeaderAttributes, err := internalapi.ParseRequestHeaderAttributeMapping(flags.spanRequestHeaderAttributes)
	if err != nil {
		return fmt.Errorf("failed to parse tracing header mapping: %w", err)
	}

	// Parse endpoint prefixes and apply defaults for any missing values.
	endpointPrefixes, err := internalapi.ParseEndpointPrefixes(flags.endpointPrefixes)
	if err != nil {
		return fmt.Errorf("failed to parse endpoint prefixes: %w", err)
	}

	// Create Prometheus registry and reader which automatically converts
	// attribute to Prometheus-compatible format (e.g. dots to underscores).
	promRegistry := prometheus.NewRegistry()
	promReader, err := otelprom.New(otelprom.WithRegisterer(promRegistry))
	if err != nil {
		return fmt.Errorf("failed to create prometheus reader: %w", err)
	}

	// Create meter with Prometheus + optionally OTEL.
	meter, metricsShutdown, err := metrics.NewMeterFromEnv(ctx, os.Stdout, promReader)
	if err != nil {
		return fmt.Errorf("failed to create metrics: %w", err)
	}
	chatCompletionMetricsFactory := metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationChat)
	messagesMetricsFactory := metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationMessages)
	completionMetricsFactory := metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationCompletion)
	embeddingsMetricsFactory := metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationEmbedding)
	imageGenerationMetricsFactory := metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationImageGeneration)
	rerankMetricsFactory := metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationRerank)
	mcpMetrics := metrics.NewMCP(meter, metricsRequestHeaderAttributes)
	audioSpeechMetricsFactory := metrics.NewAudioSpeechFactory(meter, metricsRequestHeaderAttributes)

	tracing, err := tracing.NewTracingFromEnv(ctx, os.Stdout, spanRequestHeaderAttributes)
	if err != nil {
		return err
	}

	server, err := extproc.NewServer(l, tracing)
	if err != nil {
		return fmt.Errorf("failed to create external processor server: %w", err)
	}
	server.Register(path.Join(flags.rootPrefix, endpointPrefixes.OpenAI, "/v1/chat/completions"), extproc.ChatCompletionProcessorFactory(chatCompletionMetricsFactory))
	server.Register(path.Join(flags.rootPrefix, endpointPrefixes.OpenAI, "/v1/completions"), extproc.CompletionsProcessorFactory(completionMetricsFactory))
	server.Register(path.Join(flags.rootPrefix, endpointPrefixes.OpenAI, "/v1/embeddings"), extproc.EmbeddingsProcessorFactory(embeddingsMetricsFactory))
	server.Register(path.Join(flags.rootPrefix, endpointPrefixes.OpenAI, "/v1/images/generations"), extproc.ImageGenerationProcessorFactory(imageGenerationMetricsFactory))
	server.Register(path.Join(flags.rootPrefix, endpointPrefixes.Cohere, "/v2/rerank"), extproc.RerankProcessorFactory(rerankMetricsFactory))
	server.Register(path.Join(flags.rootPrefix, endpointPrefixes.OpenAI, "/v1/models"), extproc.NewModelsProcessor)
	server.Register(path.Join(flags.rootPrefix, endpointPrefixes.Anthropic, "/v1/messages"), extproc.MessagesProcessorFactory(messagesMetricsFactory))
	server.Register(path.Join(flags.rootPrefix, endpointPrefixes.OpenAI, "/v1/audio/speech"), extproc.AudioSpeechProcessorFactory(audioSpeechMetricsFactory))

	if watchErr := filterapi.StartConfigWatcher(ctx, flags.configPath, server, l, time.Second*5); watchErr != nil {
		return fmt.Errorf("failed to start config watcher: %w", watchErr)
	}

	// Create and register gRPC server with ExternalProcessorServer (the service Envoy calls).
	if err = filterapi.StartConfigWatcher(ctx, flags.configPath, server, l, time.Second*5); err != nil {
		return fmt.Errorf("failed to start config watcher: %w", err)
	}

	var mcpServer *http.Server
	if mcpLis != nil {
		seed, fallbackSeed, _ := strings.Cut(flags.mcpSessionEncryptionSeed, ",")
		mcpSessionCrypto := mcpproxy.DefaultSessionCrypto(seed, fallbackSeed)
		var mcpProxyMux *http.ServeMux
		var mcpProxyConfig *mcpproxy.ProxyConfig
		mcpProxyConfig, mcpProxyMux, err = mcpproxy.NewMCPProxy(l.With("component", "mcp-proxy"), mcpMetrics,
			tracing.MCPTracer(), mcpSessionCrypto)
		if err != nil {
			return fmt.Errorf("failed to create MCP proxy: %w", err)
		}
		if err = filterapi.StartConfigWatcher(ctx, flags.configPath, mcpProxyConfig, l, time.Second*5); err != nil {
			return fmt.Errorf("failed to start config watcher: %w", err)
		}

		mcpServer = &http.Server{
			Handler:           mcpProxyMux,
			ReadHeaderTimeout: 120 * time.Second,
			WriteTimeout:      flags.mcpWriteTimeout,
		}
		go func() {
			l.Info("Starting mcp proxy", "addr", mcpLis.Addr())
			if err2 := mcpServer.Serve(mcpLis); err2 != nil && !errors.Is(err2, http.ErrServerClosed) {
				l.Error("mcp proxy failed", "error", err2)
			}
		}()
	}

	s := grpc.NewServer(grpc.MaxRecvMsgSize(flags.maxRecvMsgSize))
	extprocv3.RegisterExternalProcessorServer(s, server)
	grpc_health_v1.RegisterHealthServer(s, server)

	// Create a gRPC client connection for the above ExternalProcessorServer.
	// This ensures Docker HEALTHCHECK and Kubernetes readiness probes pass
	// only when Envoy considers this external processor healthy.
	healthCheckConn, err := newGrpcClient(extProcLis.Addr())
	if err != nil {
		return fmt.Errorf("failed to create health check client: %w", err)
	}
	healthClient := grpc_health_v1.NewHealthClient(healthCheckConn)

	// Start HTTP admin server for metrics and health checks.
	adminServer := startAdminServer(adminLis, l, promRegistry, healthClient)

	go func() {
		<-ctx.Done()
		s.GracefulStop()

		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := healthCheckConn.Close(); err != nil {
			l.Error("Failed to close health check client", "error", err)
		}
		if err := adminServer.Shutdown(shutdownCtx); err != nil {
			l.Error("Failed to shutdown admin server gracefully", "error", err)
		}
		if err := tracing.Shutdown(shutdownCtx); err != nil {
			l.Error("Failed to shutdown tracing gracefully", "error", err)
		}
		if err := metricsShutdown(shutdownCtx); err != nil {
			l.Error("Failed to shutdown metrics gracefully", "error", err)
		}
		if mcpServer != nil {
			if err := mcpServer.Shutdown(shutdownCtx); err != nil {
				l.Error("Failed to shutdown mcp proxy server gracefully", "error", err)
			}
		}
	}()

	// Emit startup message to stderr when all listeners are ready.
	l.Info("AI Gateway External Processor is ready")
	return s.Serve(extProcLis)
}

func listen(ctx context.Context, name, network, address string) (net.Listener, error) {
	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, network, address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen for %s: %w", name, err)
	}
	return lis, nil
}

// listenAddress returns the network and address for the given address flag.
func listenAddress(addrFlag string) (string, string) {
	if after, ok := strings.CutPrefix(addrFlag, "unix://"); ok {
		p := after
		_ = os.Remove(p) // Remove the socket file if it exists.
		return "unix", p
	}
	return "tcp", addrFlag
}
