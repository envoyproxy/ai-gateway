// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"

	"github.com/envoyproxy/ai-gateway/internal/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/dynamicmodule"
	"github.com/envoyproxy/ai-gateway/internal/dynamicmodule/sdk"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

func main() {} // This must be present to make a shared library.

// Set the envoy.NewHTTPFilter function to create a new http filter.
func init() {
	g := &globalState{}
	if err := g.initializeEnv(); err != nil {
		panic("failed to create env config: " + err.Error())
	}

	// TODO: use a writer implemented with the Logger ABI of Envoy.
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug, // Adjust log level from environment variable if needed.
	}))
	if err := filterapi.StartConfigWatcher(context.Background(),
		os.Getenv("AI_GATEWAY_DYNAMIC_MODULE_FILTER_CONFIG_PATH"), g, logger, time.Second*5); err != nil {
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

func (g *globalState) initializeEnv() error {
	ctx := context.Background()
	promRegistry := prometheus.NewRegistry()
	promReader, err := otelprom.New(otelprom.WithRegisterer(promRegistry))
	if err != nil {
		return fmt.Errorf("failed to create prometheus reader: %w", err)
	}

	meter, _, err := metrics.NewMeterFromEnv(ctx, os.Stdout, promReader)
	if err != nil {
		return fmt.Errorf("failed to create metrics: %w", err)
	}

	endpointPrefixes, err := internalapi.ParseEndpointPrefixes(os.Getenv(
		"AI_GATEWAY_DYNAMIC_MODULE_FILTER_ENDPOINT_PREFIXES",
	))
	if err != nil {
		return fmt.Errorf("failed to parse endpoint prefixes: %w", err)
	}

	metricsRequestHeaderAttributes, err := internalapi.ParseRequestHeaderAttributeMapping(os.Getenv(
		"AI_GATEWAY_DYNAMIC_MODULE_FILTER_METRICS_REQUEST_HEADER_ATTRIBUTES",
	))
	if err != nil {
		return fmt.Errorf("failed to parse metrics header mapping: %w", err)
	}

	g.env = &dynamicmodule.Env{
		RootPrefix:                    os.Getenv("AI_GATEWAY_DYNAMIC_MODULE_ROOT_PREFIX"),
		EndpointPrefixes:              endpointPrefixes,
		ChatCompletionMetricsFactory:  metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationChat),
		MessagesMetricsFactory:        metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationMessages),
		CompletionMetricsFactory:      metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationCompletion),
		EmbeddingsMetricsFactory:      metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationEmbedding),
		ImageGenerationMetricsFactory: metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationImageGeneration),
		RerankMetricsFactory:          metrics.NewMetricsFactory(meter, metricsRequestHeaderAttributes, metrics.GenAIOperationRerank),
	}
	return nil
}
