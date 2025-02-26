// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

var _ Processor = (*instrumentedChatCompletionProcessor)(nil)

// instrumentedChatCompletionProcessor is a Processor that records metrics for chat completion requests.
type instrumentedChatCompletionProcessor struct {
	delegate       *chatCompletionProcessor
	metrics        TokenMetrics
	logger         *slog.Logger
	config         *processorConfig
	requestStart   time.Time
	backendName    string
	modelName      string
	firstTokenSent bool
	lastTokenTime  time.Time
}

// InstrumentChatCompletion wraps a ProcessorFactory with metrics recording for chat completion requests.
func InstrumentChatCompletion(factory ProcessorFactory, metrics TokenMetrics) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger) (Processor, error) {
		processor, err := factory(config, requestHeaders, logger)
		if err != nil {
			return nil, err
		}
		ccp, ok := processor.(*chatCompletionProcessor)
		if !ok {
			return nil, fmt.Errorf("unsupported processor type for the chat completion instrumentation: %T", processor)
		}
		return &instrumentedChatCompletionProcessor{
			delegate: ccp,
			metrics:  metrics,
			logger:   logger,
			config:   config,
		}, nil
	}
}

// ProcessRequestHeaders implements [Processor].
func (i *instrumentedChatCompletionProcessor) ProcessRequestHeaders(ctx context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	i.logger.Debug("Request instrumentation start")
	i.requestStart = time.Now()
	return i.delegate.ProcessRequestHeaders(ctx, headerMap)
}

// ProcessRequestBody implements [Processor].
func (i *instrumentedChatCompletionProcessor) ProcessRequestBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	resp, err := i.delegate.ProcessRequestBody(ctx, body)
	if err != nil {
		i.metrics.RecordRequestMetrics(i.backendName, i.modelName, false, time.Since(i.requestStart))
		return resp, err
	}
	i.modelName = headerValue(resp.GetRequestBody().GetResponse().GetHeaderMutation().GetSetHeaders(), i.config.modelNameHeaderKey)
	i.backendName = headerValue(resp.GetRequestBody().GetResponse().GetHeaderMutation().GetSetHeaders(), i.config.selectedBackendHeaderKey)
	return resp, err
}

// ProcessResponseHeaders implements [Processor].
func (i *instrumentedChatCompletionProcessor) ProcessResponseHeaders(ctx context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	return i.delegate.ProcessResponseHeaders(ctx, headerMap)
}

// ProcessResponseBody implements [Processor].
func (i *instrumentedChatCompletionProcessor) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	resp, err := i.delegate.ProcessResponseBody(ctx, body)
	if err != nil {
		i.metrics.RecordRequestMetrics(i.backendName, i.modelName, false, time.Since(i.requestStart))
		return resp, err
	}

	// If we have some token information, and it's the first time we see tokens in the response,
	// record the timme to first token.
	if i.delegate.costs.InputTokens > 0 {
		now := time.Now()
		if !i.firstTokenSent {
			i.logger.Debug("First token sent", "model", i.modelName, "backend", i.backendName)
			i.metrics.RecordTimeToFirstToken(i.backendName, i.modelName, now.Sub(i.requestStart))
			i.firstTokenSent = true
		} else {
			div := i.delegate.costs.OutputTokens
			if div == 0 {
				div = 1
			}
			itl := now.Sub(i.lastTokenTime).Seconds() / float64(div)
			i.metrics.RecordInterTokenLatency(i.backendName, i.modelName, itl)
		}
		i.lastTokenTime = time.Now()
	}

	// If the response is the end of the stream, record the request metrics, as the request will be completed.
	if body.EndOfStream {
		i.logger.Debug("Request instrumentation completed", "model", i.modelName, "backend", i.backendName)
		i.metrics.RecordRequestMetrics(i.backendName, i.modelName, true, time.Since(i.requestStart))
		i.metrics.RecordTokenMetrics(i.backendName, i.modelName, "input", float64(i.delegate.costs.InputTokens))
		i.metrics.RecordTokenMetrics(i.backendName, i.modelName, "output", float64(i.delegate.costs.OutputTokens))
		i.metrics.RecordTokenMetrics(i.backendName, i.modelName, "total", float64(i.delegate.costs.TotalTokens))
	}

	return resp, err
}

// headerValue returns the value of a header with the given name, if present.
func headerValue(headers []*corev3.HeaderValueOption, name string) string {
	for _, h := range headers {
		if h.Header.Key == name {
			return string(h.Header.RawValue)
		}
	}
	return ""
}
