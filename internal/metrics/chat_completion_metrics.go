// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

// ChatCompletion is the interface for the chat completion AI Gateway metrics.
type ChatCompletion interface {
	// StartRequest initializes timing for a new request.
	StartRequest()
	// SetModel sets the model the request. This is usually called after parsing the request body .
	SetModel(model string)
	// SetBackend sets the selected backend when the routing decision has been made. This is usually called
	// after parsing the request body to determine the model and invoke the routing logic.
	SetBackend(backend filterapi.Backend)

	// RecordTokenUsage records token usage metrics.
	RecordTokenUsage(inputTokens, outputTokens, totalTokens uint32)
	// RecordRequestCompletion records latency metrics for the entire request
	RecordRequestCompletion(success bool)
	// RecordTokenLatency records latency metrics for token generation.
	RecordTokenLatency(tokens uint32)
}

// chatCompletion is the implementation for the chat completion AI Gateway metrics.
type chatCompletion struct {
	metrics        *genAI
	firstTokenSent bool
	requestStart   time.Time
	lastTokenTime  time.Time
	model          string
	backend        string
}

// NewChatCompletion creates a new ProcessorMetrics instance.
func NewChatCompletion(registry prometheus.Registerer) ChatCompletion {
	return &chatCompletion{
		metrics: newGenAI(registry),
		model:   "unknown",
		backend: "unknown",
	}
}

// StartRequest initializes timing for a new request.
func (c *chatCompletion) StartRequest() {
	c.requestStart = time.Now()
	c.firstTokenSent = false
}

// SetModel sets the model for the request.
func (c *chatCompletion) SetModel(model string) {
	c.model = model
}

// SetBackend sets the name of the backend to be reported in the metrics according to:
// https://opentelemetry.io/docs/specs/semconv/attributes-registry/gen-ai/#gen-ai-system
func (c *chatCompletion) SetBackend(backend filterapi.Backend) {
	switch backend.Schema.Name {
	case filterapi.APISchemaOpenAI:
		c.backend = "openai"
	case filterapi.APISchemaAWSBedrock:
		c.backend = "aws.bedrock"
	default:
		c.backend = backend.Name
	}
}

// RecordTokenUsage implements [ChatCompletion.RecordTokenUsage].
func (c *chatCompletion) RecordTokenUsage(inputTokens, outputTokens, totalTokens uint32) {
	c.metrics.tokenUsage.WithLabelValues("chat", c.backend, "input", c.model, c.model).Observe(float64(inputTokens))
	c.metrics.tokenUsage.WithLabelValues("chat", c.backend, "output", c.model, c.model).Observe(float64(outputTokens))
	c.metrics.tokenUsage.WithLabelValues("chat", c.backend, "total", c.model, c.model).Observe(float64(totalTokens))
}

// RecordRequestCompletion implements [ChatCompletion.RecordRequestCompletion].
func (c *chatCompletion) RecordRequestCompletion(success bool) {
	if success {
		c.metrics.requestLatency.WithLabelValues("chat", c.backend, c.model, c.model, "").Observe(time.Since(c.requestStart).Seconds())
	} else {
		// We don't have a set of typed errors yet, or a set of low-cardinality values, so we can just set the value to the
		// placeholder one. See: https://opentelemetry.io/docs/specs/semconv/attributes-registry/error/#error-type
		c.metrics.requestLatency.WithLabelValues("chat", c.backend, c.model, c.model, "_OTHER").Observe(time.Since(c.requestStart).Seconds())
	}
}

// RecordTokenLatency implements [ChatCompletion.RecordTokenLatency].
func (c *chatCompletion) RecordTokenLatency(tokens uint32) {
	if !c.firstTokenSent {
		c.firstTokenSent = true
		c.metrics.firstTokenLatency.WithLabelValues("chat", c.backend, c.model, c.model).Observe(time.Since(c.requestStart).Seconds())
	} else if tokens > 0 {
		// Calculate time between tokens.
		itl := time.Since(c.lastTokenTime).Seconds() / float64(tokens)
		c.metrics.outputTokenLatency.WithLabelValues("chat", c.backend, c.model, c.model).Observe(itl)
	}
	c.lastTokenTime = time.Now()
}
