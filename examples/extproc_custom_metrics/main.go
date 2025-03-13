// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"go.opentelemetry.io/otel/metric"

	"github.com/envoyproxy/ai-gateway/cmd/extproc/mainlib"
	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
)

// This demonstrates how to build a custom chat completion metrics for the external processor.
func main() {
	// Initializes the custom chat completion metrics.
	x.NewCustomChatCompletionMetrics = newCustomChatCompletionMetrics

	// Executes the main function of the external processor.
	ctx, cancel := context.WithCancel(context.Background())
	signalsChan := make(chan os.Signal, 1)
	signal.Notify(signalsChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-signalsChan
		cancel()
	}()
	mainlib.Main(ctx, os.Args[1:], os.Stderr)
}

// newCustomChatCompletionMetrics implements [x.NewCustomChatCompletionMetrics].
func newCustomChatCompletionMetrics(meter metric.Meter) x.ChatCompletionMetrics {
	return &myCustomChatCompletionMetrics{
		meter:  meter,
		logger: slog.New(slog.NewTextHandler(os.Stdout, nil)),
	}
}

// myCustomChatCompletionMetrics implements [x.ChatCompletionMetrics].
type myCustomChatCompletionMetrics struct {
	meter  metric.Meter
	logger *slog.Logger
}

func (m *myCustomChatCompletionMetrics) StartRequest(headers map[string]string) {
	m.logger.Info("StartRequest", "headers", headers)
}

func (m *myCustomChatCompletionMetrics) SetModel(model string) {
	m.logger.Info("SetModel", "model", model)
}

func (m *myCustomChatCompletionMetrics) SetBackend(backend filterapi.Backend) {
	m.logger.Info("SetBackend", "backend", backend.Name)
}

func (m *myCustomChatCompletionMetrics) RecordTokenUsage(_ context.Context, inputTokens, outputTokens, totalTokens uint32) {
	m.logger.Info("RecordTokenUsage", "inputTokens", inputTokens, "outputTokens", outputTokens, "totalTokens", totalTokens)
}

func (m *myCustomChatCompletionMetrics) RecordRequestCompletion(_ context.Context, success bool) {
	m.logger.Info("RecordRequestCompletion", "success", success)
}

func (m *myCustomChatCompletionMetrics) RecordTokenLatency(_ context.Context, tokens uint32) {
	m.logger.Info("RecordTokenLatency", "tokens", tokens)
}
