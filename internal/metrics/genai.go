// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package metrics

import "go.opentelemetry.io/otel/metric"

const (
	// Metric names, attributes and values according to the Semantic Conventions for Generative AI Metrics.
	// See: https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/

	genaiMetricClientTokenUsage         = "gen_ai.client.token.usage" // #nosec G101: Potential hardcoded credentials
	genaiMetricServerRequestDuration    = "gen_ai.server.request.duration"
	genaiMetricServerTimeToFirstToken   = "gen_ai.server.time_to_first_token"   // #nosec G101: Potential hardcoded credentials
	genaiMetricServerTimePerOutputToken = "gen_ai.server.time_per_output_token" // #nosec G101: Potential hardcoded credentials

	genaiAttributeOperationName = "gen_ai.operation.name"
	genaiAttributeSystemName    = "gen_ai.system.name"
	genaiAttributeRequestModel  = "gen_ai.request.model"
	genaiAttributeTokenType     = "gen_ai.token.type" // #nosec G101: Potential hardcoded credentials
	genaiAttributeErrorType     = "error.type"

	genaiOperationChat      = "chat"
	genaiOperationEmbedding = "embedding"
	genaiSystemOpenAI       = "openai"
	genAISystemAWSBedrock   = "aws.bedrock"
	genaiTokenTypeInput     = "input"
	genaiTokenTypeOutput    = "output"
	genaiTokenTypeTotal     = "total"
	genaiErrorTypeFallback  = "_OTHER"
)

// genAI holds metrics according to the Semantic Conventions for Generative AI Metrics.
// See: https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/.
type genAI struct {
	// Number of tokens processed.
	// See: https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#metric-gen_aiclienttokenusage
	tokenUsage metric.Float64Histogram
	// requestLatency is the total latency of the request.
	// Measured from the start of the received request headers in extproc to the end of the processed response body in extproc.
	// See: https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#metric-gen_aiserverrequestduration
	requestLatency metric.Float64Histogram
	// firstTokenLatency is the latency to receive the first token.
	// Measured from the start of the received request headers in extproc to the receiving of the first token in the response body in extproc.
	// See: https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#metric-gen_aiservertime_to_first_token
	firstTokenLatency metric.Float64Histogram
	// outputTokenLatency is the latency between consecutive tokens, if supported, or by chunks/tokens otherwise, by backend, model.
	// See: https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-metrics/#metric-gen_aiservertime_per_output_token
	outputTokenLatency metric.Float64Histogram
}

// newGenAI creates a new genAI metrics instance.
func newGenAI(meter metric.Meter) *genAI {
	return &genAI{
		tokenUsage: mustRegisterHistogram(meter,
			genaiMetricClientTokenUsage,
			metric.WithDescription("Number of tokens processed."),
			metric.WithUnit("{token}"),
			metric.WithExplicitBucketBoundaries(1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864),
		),
		requestLatency: mustRegisterHistogram(meter,
			genaiMetricServerRequestDuration,
			metric.WithDescription("Time spent processing request."),
			metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(0.01, 0.02, 0.04, 0.08, 0.16, 0.32, 0.64, 1.28, 2.56, 5.12, 10.24, 20.48, 40.96, 81.92),
		),
		firstTokenLatency: mustRegisterHistogram(meter,
			genaiMetricServerTimeToFirstToken,
			metric.WithDescription("Time to receive first token in streaming responses."),
			metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(0.001, 0.005, 0.01, 0.02, 0.04, 0.06, 0.08, 0.1, 0.25, 0.5, 0.75, 1.0, 2.5, 5.0, 7.5, 10.0),
		),
		outputTokenLatency: mustRegisterHistogram(meter,
			genaiMetricServerTimePerOutputToken,
			metric.WithDescription("Time between consecutive tokens in streaming responses."),
			metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(0.01, 0.025, 0.05, 0.075, 0.1, 0.15, 0.2, 0.3, 0.4, 0.5, 0.75, 1.0, 2.5),
		),
	}
}

// mustRegisterHistogram registers a histogram with the meter and panics if it fails.
func mustRegisterHistogram(meter metric.Meter, name string, options ...metric.Float64HistogramOption) metric.Float64Histogram {
	h, err := meter.Float64Histogram(name, options...)
	if err != nil {
		panic(err)
	}
	return h
}
