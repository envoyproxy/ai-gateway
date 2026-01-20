// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"

	"github.com/envoyproxy/ai-gateway/internal/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/dynamicmodule"
	"github.com/envoyproxy/ai-gateway/internal/dynamicmodule/sdk"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing"
)

func main() {} // This must be present to make a shared library.

// List of constants for environment variable names.
const (
	envAdminAddress                   = "AI_GATEWAY_DYNAMIC_MODULE_ADMIN_ADDRESS"      // mandatory
	envFilterConfigPath               = "AI_GATEWAY_DYNAMIC_MODULE_FILTER_CONFIG_PATH" // mandatory
	envRootPrefix                     = "AI_GATEWAY_DYNAMIC_MODULE_ROOT_PREFIX"
	envEndpointPrefixes               = "AI_GATEWAY_DYNAMIC_MODULE_FILTER_ENDPOINT_PREFIXES"
	envMetricsRequestHeaderAttributes = "AI_GATEWAY_DYNAMIC_MODULE_FILTER_METRICS_REQUEST_HEADER_ATTRIBUTES"
	envTracingRequestHeaderAttributes = "AI_GATEWAY_DYNAMIC_MODULE_FILTER_TRACING_REQUEST_HEADER_ATTRIBUTES"
)

// Set the envoy.NewHTTPFilter function to create a new http filter.
func init() {
	g := &globalState{}
	promRegistry := prometheus.NewRegistry()
	if err := g.initializeEnv(promRegistry); err != nil {
		panic("failed to create env config: " + err.Error())
	}

	err := startAdminServer(
		g.env.Logger,
		os.Getenv(envAdminAddress),
		promRegistry)
	if err != nil {
		panic("failed to start admin server: " + err.Error())
	}

	if err := filterapi.StartConfigWatcher(context.Background(),
		os.Getenv(envFilterConfigPath), g, g.env.Logger, time.Second*5); err != nil {
		panic("failed to start filter config watcher: " + err.Error())
	}
	sdk.NewHTTPFilterConfig = g.newHTTPFilterConfig
}

// globalState implements [filterapi.ConfigReceiver] to load filter configuration.
type globalState struct {
	fc  *filterapi.RuntimeConfig
	env *dynamicmodule.Env
}

// newHTTPFilterConfig creates a new http filter based on the config.
//
// `config` is the configuration string that is specified in the Envoy configuration.
func (g *globalState) newHTTPFilterConfig(name string, _ []byte) sdk.HTTPFilterConfig {
	switch name {
	case "ai_gateway.router":
		return dynamicmodule.NewRouterFilterConfig(g.env, &g.fc)
	case "ai_gateway.upstream":
		return dynamicmodule.NewUpstreamFilterConfig(g.env)
	default:
		panic("unknown filter: " + name)
	}
}

// LoadConfig implements [filterapi.ConfigReceiver.LoadConfig].
func (g *globalState) LoadConfig(ctx context.Context, config *filterapi.Config) error {
	newConfig, err := filterapi.NewRuntimeConfig(ctx, config, backendauth.NewHandler)
	if err != nil {
		return fmt.Errorf("cannot create runtime filter config: %w", err)
	}
	g.fc = newConfig // This is racy but we don't care.
	return nil
}

func (g *globalState) initializeEnv(promRegistry *prometheus.Registry) error {
	ctx := context.Background()
	promReader, err := otelprom.New(otelprom.WithRegisterer(promRegistry))
	if err != nil {
		return fmt.Errorf("failed to create prometheus reader: %w", err)
	}

	meter, _, err := metrics.NewMeterFromEnv(ctx, os.Stdout, promReader)
	if err != nil {
		return fmt.Errorf("failed to create metrics: %w", err)
	}

	endpointPrefixes, err := internalapi.ParseEndpointPrefixes(os.Getenv(envEndpointPrefixes))
	if err != nil {
		return fmt.Errorf("failed to parse endpoint prefixes: %w", err)
	}

	metricsRequestHeaderAttributes, err := internalapi.ParseRequestHeaderAttributeMapping(os.Getenv(
		envMetricsRequestHeaderAttributes,
	))
	if err != nil {
		return fmt.Errorf("failed to parse metrics header mapping: %w", err)
	}
	spanRequestHeaderAttributes, err := internalapi.ParseRequestHeaderAttributeMapping(os.Getenv(
		envTracingRequestHeaderAttributes,
	))
	if err != nil {
		return fmt.Errorf("failed to parse tracing header mapping: %w", err)
	}

	tr, err := tracing.NewTracingFromEnv(ctx, os.Stdout, spanRequestHeaderAttributes)
	if err != nil {
		return err
	}

	g.env = &dynamicmodule.Env{
		RootPrefix:                    cmp.Or(os.Getenv(envRootPrefix), "/"),
		EndpointPrefixes:              endpointPrefixes,
		ChatCompletionMetricsFactory:  metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationChat),
		MessagesMetricsFactory:        metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationMessages),
		CompletionMetricsFactory:      metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationCompletion),
		EmbeddingsMetricsFactory:      metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationEmbedding),
		ImageGenerationMetricsFactory: metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationImageGeneration),
		RerankMetricsFactory:          metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationRerank),
		Tracing:                       tr,
		RouterFilters: &dynamicmodule.RouterFilters{
			Filters: make(map[string]dynamicmodule.RouterFilterItem),
		},
		Logger: sdk.NewSlogLogger(),
	}
	g.env.DebugLogEnabled = g.env.Logger.Enabled(ctx, slog.LevelDebug)
	return nil
}

func startAdminServer(l *slog.Logger, address string, registry prometheus.Gatherer) error {
	var lc net.ListenConfig
	lis, err := lc.Listen(context.Background(), "tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen for admin: %w", err)
	}

	l.Info("admin server listening on " + address)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(
		registry,
		promhttp.HandlerOpts{},
	))
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		l.Info("starting admin server on " + address)
		if err := server.Serve(lis); err != nil && !errors.Is(err, http.ErrServerClosed) {
			l.Error("admin server failed: " + err.Error())
		}
	}()
	return nil
}
