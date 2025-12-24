// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dynamicmodule

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"

	"github.com/andybalholm/brotli"

	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/bodymutator"
	"github.com/envoyproxy/ai-gateway/internal/dynamicmodule/sdk"
	"github.com/envoyproxy/ai-gateway/internal/endpointspec"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

type (
	// upstreamFilterConfig implements [sdk.HTTPFilterConfig].
	upstreamFilterConfig struct {
		logger *slog.Logger
		env    *Env
	}
	// upstreamFilter implements [sdk.HTTPFilter].
	upstreamFilter struct {
		logger          *slog.Logger
		debugLogEnabled bool
		env             *Env
		typedFilter     upstreamFilterTypedIface
	}

	upstreamFilterTypedIface interface {
		RequestHeaders(e sdk.EnvoyHTTPFilter) error
		RequestBody(e sdk.EnvoyHTTPFilter) error
		ResponseHeaders(e sdk.EnvoyHTTPFilter, endOfStream bool) error
		ResponseBody(e sdk.EnvoyHTTPFilter, endOfStream bool) error
		ResponseBodyOnError(e sdk.EnvoyHTTPFilter) error
	}

	upstreamFilterTyped[ReqT, RespT, RespChunkT any, EndpointSpec endpointspec.Spec[ReqT, RespT, RespChunkT]] struct {
		logger          *slog.Logger
		debugLogEnabled bool
		routerFilter    *routerFilterTyped[ReqT, RespT, RespChunkT, EndpointSpec]
		translator      translator.Translator[ReqT, tracing.Span[RespT, RespChunkT]]
		metrics         metrics.Metrics

		onRetry                bool
		reqHeaders, resHeaders map[string]string
		backend                *filterapi.RuntimeBackend
		// cost is the cost of the request that is accumulated during the processing of the response.
		costs metrics.TokenUsage
	}
)

func NewUpstreamFilterConfig(env *Env) sdk.HTTPFilterConfig {
	return &upstreamFilterConfig{env: env, logger: env.Logger.With(slog.String("component", "upstream_filter"))}
}

// NewFilter implements [sdk.HTTPFilterConfig].
func (f *upstreamFilterConfig) NewFilter() sdk.HTTPFilter {
	return &upstreamFilter{env: f.env, logger: f.logger, debugLogEnabled: f.env.DebugLogEnabled}
}

// OnDestroy implements [sdk.HTTPFilter].
func (f *upstreamFilter) OnDestroy() {}

// RequestHeaders implements [sdk.HTTPFilter].
func (f *upstreamFilter) RequestHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.RequestHeadersStatus {
	internalRequestID, ok := e.GetDynamicMetadataString(internalapi.AIGatewayFilterMetadataNamespace,
		internalRequestIDMetadataKey)
	if !ok {
		e.SendLocalReply(500, nil, []byte("internal server error"))
		f.logger.Error("internal request ID not found in dynamic metadata")
		return sdk.RequestHeadersStatusStopIteration
	}
	rfs := f.env.RouterFilters
	rfs.Lock.RLock()
	rf, ok := rfs.Filters[internalRequestID].(*routerFilter)
	rfs.Lock.RUnlock()
	if !ok {
		e.SendLocalReply(500, nil, []byte("internal server error"))
		f.logger.Error("router filter not found for request ID", slog.String("internal_request_id", internalRequestID))
		return sdk.RequestHeadersStatusStopIteration
	}

	rf.attemptCount++
	onRetry := rf.attemptCount > 1
	if f.debugLogEnabled {
		f.logger.Debug("upstream filter request headers called",
			slog.Int("attempt_count", rf.attemptCount),
			slog.Bool("on_retry", onRetry))
	}

	backend, ok := e.GetUpstreamHostMetadataString(internalapi.InternalEndpointMetadataNamespace, internalapi.InternalMetadataBackendNameKey)
	if !ok {
		f.logger.Error("backend name not found in upstream host metadata")
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.RequestHeadersStatusStopIteration
	}
	b, ok := rf.runtimeFilterConfig.Backends[backend]
	if !ok {
		f.logger.Error("backend not found in runtime filter config", slog.String("backend", backend))
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.RequestHeadersStatusStopIteration
	}

	var m metrics.Metrics
	switch rf.endpoint {
	case chatCompletionsEndpoint:
		m = f.env.ChatCompletionMetricsFactory.NewMetrics()
		typed := &upstreamFilterTyped[openai.ChatCompletionRequest,
			openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk, endpointspec.ChatCompletionsEndpointSpec]{
			metrics:         m,
			onRetry:         onRetry,
			backend:         b,
			logger:          f.logger,
			debugLogEnabled: f.debugLogEnabled,
			routerFilter: rf.typedFilter.(*routerFilterTyped[openai.ChatCompletionRequest,
				openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk, endpointspec.ChatCompletionsEndpointSpec]),
		}
		typed.routerFilter.upstreamFilter = typed
		f.typedFilter = typed
	case completionsEndpoint:
		m = f.env.CompletionMetricsFactory.NewMetrics()
		typed := &upstreamFilterTyped[openai.CompletionRequest,
			openai.CompletionResponse, openai.CompletionResponse, endpointspec.CompletionsEndpointSpec]{
			onRetry:         onRetry,
			metrics:         m,
			backend:         b,
			logger:          f.logger,
			debugLogEnabled: f.debugLogEnabled,
			routerFilter: rf.typedFilter.(*routerFilterTyped[openai.CompletionRequest,
				openai.CompletionResponse, openai.CompletionResponse, endpointspec.CompletionsEndpointSpec]),
		}
		typed.routerFilter.upstreamFilter = typed
		f.typedFilter = typed
	case embeddingsEndpoint:
		m = f.env.EmbeddingsMetricsFactory.NewMetrics()
		typed := &upstreamFilterTyped[openai.EmbeddingRequest,
			openai.EmbeddingResponse, struct{}, endpointspec.EmbeddingsEndpointSpec]{
			onRetry:         onRetry,
			metrics:         m,
			backend:         b,
			logger:          f.logger,
			debugLogEnabled: f.debugLogEnabled,
			routerFilter: rf.typedFilter.(*routerFilterTyped[openai.EmbeddingRequest,
				openai.EmbeddingResponse, struct{}, endpointspec.EmbeddingsEndpointSpec]),
		}
		typed.routerFilter.upstreamFilter = typed
		f.typedFilter = typed
	case imagesGenerationsEndpoint:
		m = f.env.ImageGenerationMetricsFactory.NewMetrics()
		typed := &upstreamFilterTyped[openai.ImageGenerationRequest,
			openai.ImageGenerationResponse, struct{}, endpointspec.ImageGenerationEndpointSpec]{
			onRetry:         onRetry,
			metrics:         m,
			backend:         b,
			logger:          f.logger,
			debugLogEnabled: f.debugLogEnabled,
			routerFilter: rf.typedFilter.(*routerFilterTyped[openai.ImageGenerationRequest,
				openai.ImageGenerationResponse, struct{}, endpointspec.ImageGenerationEndpointSpec]),
		}
		typed.routerFilter.upstreamFilter = typed
		f.typedFilter = typed
	case rerankEndpoint:
		m = f.env.RerankMetricsFactory.NewMetrics()
		typed := &upstreamFilterTyped[cohereschema.RerankV2Request,
			cohereschema.RerankV2Response, struct{}, endpointspec.RerankEndpointSpec]{
			onRetry:         onRetry,
			metrics:         m,
			backend:         b,
			logger:          f.logger,
			debugLogEnabled: f.debugLogEnabled,
			routerFilter: rf.typedFilter.(*routerFilterTyped[cohereschema.RerankV2Request,
				cohereschema.RerankV2Response, struct{}, endpointspec.RerankEndpointSpec]),
		}
		typed.routerFilter.upstreamFilter = typed
		f.typedFilter = typed
	case messagesEndpoint:
		m = f.env.MessagesMetricsFactory.NewMetrics()
		typed := &upstreamFilterTyped[anthropic.MessagesRequest,
			anthropic.MessagesResponse, anthropic.MessagesStreamChunk, endpointspec.MessagesEndpointSpec]{
			onRetry:         onRetry,
			metrics:         m,
			backend:         b,
			logger:          f.logger,
			debugLogEnabled: f.debugLogEnabled,
			routerFilter: rf.typedFilter.(*routerFilterTyped[anthropic.MessagesRequest,
				anthropic.MessagesResponse, anthropic.MessagesStreamChunk, endpointspec.MessagesEndpointSpec]),
		}
		typed.routerFilter.upstreamFilter = typed
		f.typedFilter = typed
	case responsesEndpoint:
		m = f.env.MessagesMetricsFactory.NewMetrics()
		typed := &upstreamFilterTyped[openai.ResponseRequest,
			openai.Response, openai.ResponseStreamEventUnion, endpointspec.ResponsesEndpointSpec]{
			onRetry:         onRetry,
			metrics:         m,
			backend:         b,
			logger:          f.logger,
			debugLogEnabled: f.debugLogEnabled,
			routerFilter: rf.typedFilter.(*routerFilterTyped[openai.ResponseRequest,
				openai.Response, openai.ResponseStreamEventUnion, endpointspec.ResponsesEndpointSpec]),
		}
		typed.routerFilter.upstreamFilter = typed
		f.typedFilter = typed
	default:
		e.SendLocalReply(500, nil, []byte(fmt.Sprintf("unsupported endpoint type: %v", rf.endpoint)))
		return sdk.RequestHeadersStatusStopIteration
	}
	m.SetBackend(b.Backend)
	if err := f.typedFilter.RequestHeaders(e); err != nil {
		f.logger.Error("request headers error", slog.String("error", err.Error()))
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.RequestHeadersStatusStopIteration
	}
	return sdk.RequestHeadersStatusStopIteration // I think this should be StopIteration.
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) RequestHeaders(e sdk.EnvoyHTTPFilter) error {
	var espec EndpointSpecT
	var err error
	f.translator, err = espec.GetTranslator(f.backend.Backend.Schema, f.backend.Backend.ModelNameOverride)
	if err != nil {
		return fmt.Errorf("failed to get translator for backend schema %s: %w", f.backend.Backend.Schema, err)
	}

	f.reqHeaders = multiValueHeadersToSingleValue(e.GetRequestHeaders())

	// Now mutate the headers based on the backend configuration.
	be := f.backend.Backend
	if hm := headermutator.NewHeaderMutator(be.HeaderMutation, f.routerFilter.originalRequestHeaders); hm != nil {
		sets, removes := hm.Mutate(f.reqHeaders, f.onRetry)
		if f.debugLogEnabled {
			f.logger.Debug("setting mutated header",
				slog.Any("sets", sets),
				slog.Any("removes", removes))
		}
		for _, h := range sets {
			if !e.SetRequestHeader(h.Key(), []byte(h.Value())) {
				return fmt.Errorf("failed to set mutated header %s", h.Key())
			}
			f.reqHeaders[h.Key()] = h.Value()
		}
		for _, key := range removes {
			if !e.SetRequestHeader(key, nil) {
				return fmt.Errorf("failed to remove mutated header %s", key)
			}
			delete(f.reqHeaders, key)
		}
	}
	return nil
}

// RequestBody implements [sdk.HTTPFilter].
func (f *upstreamFilter) RequestBody(e sdk.EnvoyHTTPFilter, endOfStream bool) sdk.RequestBodyStatus {
	if f.debugLogEnabled {
		f.logger.Debug("upstream request body called", slog.Bool("end_of_stream", endOfStream))
	}
	if !endOfStream {
		// The body is already buffered in Envoy at this point, so this should be almost no-op.
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}

	if err := f.typedFilter.RequestBody(e); err != nil {
		f.logger.Error("request body error", slog.String("error", err.Error()))
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}
	return sdk.RequestBodyStatusContinue
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) RequestBody(e sdk.EnvoyHTTPFilter) (err error) {
	defer func() {
		if err != nil {
			f.metrics.RecordRequestCompletion(context.Background(), false, f.reqHeaders)
		}
	}()

	// Start tracking metrics for this request.
	f.metrics.StartRequest(f.reqHeaders)
	// Set the original model from the request body before any overrides
	f.metrics.SetOriginalModel(f.routerFilter.originalModel)
	// Set the request model for metrics from the original model or override if applied.
	reqModel := cmp.Or(f.backend.Backend.ModelNameOverride, f.routerFilter.originalModel)
	f.metrics.SetRequestModel(reqModel)

	// We force the body mutation in the following cases:
	// * The request is a retry request because the body mutation might have happened the previous iteration.
	// * The request is a streaming request, and the IncludeUsage option is set to false since we need to ensure that
	//	the token usage is calculated correctly without being bypassed.
	forceBodyMutation := f.onRetry || f.routerFilter.forceBodyMutation

	b := f.backend
	newHeaders, newBody, err := f.translator.RequestBody(f.routerFilter.originalRequestBodyRaw,
		f.routerFilter.originalRequestBody, forceBodyMutation)
	if err != nil {
		return fmt.Errorf("failed to translate request body for upstream: %w", err)
	}
	if f.debugLogEnabled {
		f.logger.Debug("upstream request body translated",
			slog.Any("new_headers", newHeaders),
			slog.Int("new_body_length", len(newBody)),
			slog.String("new_body", string(newBody)))
	}
	for _, h := range newHeaders {
		if !e.SetRequestHeader(h.Key(), []byte(h.Value())) {
			return fmt.Errorf("failed to set translated header %s", h.Key())
		}
		f.reqHeaders[h.Key()] = h.Value()
	}

	if bm := b.Backend.BodyMutation; bm != nil {
		originalRaw := f.routerFilter.originalRequestBodyRaw
		mutator := bodymutator.NewBodyMutator(bm, originalRaw)
		var mutatedBody []byte
		if newBody == nil {
			mutatedBody, err = mutator.Mutate(originalRaw, f.onRetry)
		} else {
			mutatedBody, err = mutator.Mutate(newBody, f.onRetry)
		}
		newBody = mutatedBody
	}

	if newBody != nil {
		if cur, ok := e.GetBufferedRequestBody(); ok {
			ok = e.DrainBufferedRequestBody(cur.Len())
			if !ok {
				return errors.New("failed to drain current request body for replacement")
			}
			ok = e.AppendBufferedRequestBody(newBody)
			if !ok {
				return errors.New("failed to append new request body for replacement")
			}
		} else {
			// On retry path, the body will be in received buffer.
			cur, ok = e.GetReceivedRequestBody()
			if !ok {
				return errors.New("failed to get current request body for replacement")
			}
			ok = e.DrainReceivedRequestBody(cur.Len())
			if !ok {
				return errors.New("failed to drain current received request body for replacement")
			}
			ok = e.AppendReceivedRequestBody(newBody)
			if !ok {
				return errors.New("failed to append new request body for replacement")
			}
		}

		// Set the content-length header with the new body length.
		if !e.SetRequestHeader("content-length", []byte(strconv.Itoa(len(newBody)))) {
			return fmt.Errorf("failed to set content-length header")
		}
		f.reqHeaders["content-length"] = strconv.Itoa(len(newBody))
	}

	// Next is to do the upstream auth if needed.
	if b.Handler != nil {
		originalOrNewBody := f.routerFilter.originalRequestBodyRaw
		if newBody != nil {
			originalOrNewBody = newBody
		}

		authHeaders, err := b.Handler.Do(context.Background(), f.reqHeaders, originalOrNewBody)
		if err != nil {
			return fmt.Errorf("failed to get auth headers from handler: %w", err)
		}
		for _, h := range authHeaders {
			if !e.SetRequestHeader(h.Key(), []byte(h.Value())) {
				return fmt.Errorf("failed to set auth header %s", h.Key())
			}
			f.reqHeaders[h.Key()] = h.Value()
		}
	}
	return nil
}

// ResponseHeaders implements [sdk.HTTPFilter].
func (f *upstreamFilter) ResponseHeaders(sdk.EnvoyHTTPFilter, bool) sdk.ResponseHeadersStatus {
	return sdk.ResponseHeadersStatusContinue
}

// ResponseBody implements [sdk.HTTPFilter].
func (f *upstreamFilter) ResponseBody(sdk.EnvoyHTTPFilter, bool) sdk.ResponseBodyStatus {
	return sdk.ResponseBodyStatusContinue
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseHeaders(e sdk.EnvoyHTTPFilter, _ bool) error {
	f.resHeaders = multiValueHeadersToSingleValue(e.GetResponseHeaders())
	headers, err := f.translator.ResponseHeaders(f.resHeaders)
	if err != nil {
		return fmt.Errorf("failed to transform response headers: %w", err)
	}
	for _, h := range headers {
		if !e.SetResponseHeader(h.Key(), []byte(h.Value())) {
			return fmt.Errorf("failed to set transformed header %s", h.Key())
		}
	}
	return nil
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseBody(e sdk.EnvoyHTTPFilter, endOfStream bool) (err error) {
	defer func() {
		ctx := context.Background()
		if err != nil {
			f.metrics.RecordRequestCompletion(ctx, false, f.reqHeaders)
			return
		}
		if endOfStream {
			f.metrics.RecordRequestCompletion(ctx, true, f.reqHeaders)
		}
	}()
	// Decompress the body if needed using common utility.
	var body sdk.BodyReader
	var ok bool
	if f.routerFilter.Stream() {
		body, ok = e.GetReceivedResponseBody()
		if !ok {
			if f.debugLogEnabled {
				f.logger.Debug("no received response body to process",
					slog.Bool("end_of_stream", endOfStream))
			}
			return nil
		}
	} else {
		body, ok = e.GetBufferedResponseBody()
		if !ok {
			return errors.New("failed to get response body")
		}
	}

	decodingResult, err := decodeContentIfNeeded(body, f.resHeaders["content-encoding"])
	if err != nil {
		return err
	}
	span := f.routerFilter.span
	newHeaders, newBody, tokenUsage, responseModel, err := f.translator.ResponseBody(f.resHeaders, decodingResult.reader, endOfStream, span)
	if err != nil {
		return fmt.Errorf("failed to transform response: %w", err)
	}
	for _, h := range newHeaders {
		if !e.SetResponseHeader(h.Key(), []byte(h.Value())) {
			return fmt.Errorf("failed to set transformed header %s", h.Key())
		}
	}
	if newBody != nil {
		if decodingResult.isEncoded {
			if !e.SetResponseHeader("content-encoding", nil) {
				return errors.New("failed to remove content-encoding header after decoding")
			}
		}
		if f.routerFilter.Stream() {
			cur, ok := e.GetReceivedResponseBody()
			if !ok {
				return errors.New("failed to get current response body for replacement")
			}
			ok = e.DrainReceivedResponseBody(cur.Len())
			if !ok {
				return errors.New("failed to drain current received response body for replacement")
			}
			ok = e.AppendReceivedResponseBody(newBody)
			if !ok {
				return errors.New("failed to append new response body for replacement")
			}
		} else {
			cur, ok := e.GetBufferedResponseBody()
			if !ok {
				return errors.New("failed to get current response body for replacement")
			}
			ok = e.DrainBufferedResponseBody(cur.Len())
			if !ok {
				return errors.New("failed to drain current response body for replacement")
			}
			ok = e.AppendBufferedResponseBody(newBody)
			if !ok {
				return errors.New("failed to append new response body for replacement")
			}
		}
	}

	// Translator reports the latest cumulative token usage which we use to override existing costs.
	f.costs.Override(tokenUsage)

	// Set the response model for metrics
	f.metrics.SetResponseModel(responseModel)

	// Record metrics.
	ctx := context.Background()
	stream := f.routerFilter.stream
	if stream {
		// Token latency is only recorded for streaming responses, otherwise it doesn't make sense since
		// these metrics are defined as a difference between the two output events.
		out, _ := f.costs.OutputTokens()
		f.metrics.RecordTokenLatency(ctx, out, endOfStream, f.reqHeaders)
		// Emit usage once at end-of-stream using final totals.
		if endOfStream {
			f.metrics.RecordTokenUsage(ctx, f.costs, f.reqHeaders)
		}
	} else {
		f.metrics.RecordTokenUsage(ctx, f.costs, f.reqHeaders)
	}

	if endOfStream {
		if err = f.buildDynamicMetadataOnResponse(e, &f.costs); err != nil {
			return fmt.Errorf("failed to build dynamic metadata on response: %w", err)
		}
	}

	if endOfStream && span != nil {
		span.EndSpan()
	}
	return nil
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) buildDynamicMetadataOnResponse(e sdk.EnvoyHTTPFilter, costs *metrics.TokenUsage) (err error) {
	config := f.routerFilter.runtimeFilterConfig
	for i := range config.RequestCosts {
		rc := &config.RequestCosts[i]
		var cost uint32
		switch rc.Type {
		case filterapi.LLMRequestCostTypeInputToken:
			cost, _ = costs.InputTokens()
		case filterapi.LLMRequestCostTypeCachedInputToken:
			cost, _ = costs.CachedInputTokens()
		case filterapi.LLMRequestCostTypeOutputToken:
			cost, _ = costs.OutputTokens()
		case filterapi.LLMRequestCostTypeTotalToken:
			cost, _ = costs.TotalTokens()
		case filterapi.LLMRequestCostTypeCEL:
			in, _ := costs.InputTokens()
			cachedIn, _ := costs.CachedInputTokens()
			out, _ := costs.OutputTokens()
			total, _ := costs.TotalTokens()
			costU64, err := llmcostcel.EvaluateProgram(
				rc.CELProg,
				f.reqHeaders[internalapi.ModelNameHeaderKeyDefault],
				f.backend.Backend.Name,
				in,
				cachedIn,
				out,
				total,
			)
			if err != nil {
				return fmt.Errorf("failed to evaluate CEL expression: %w", err)
			}
			cost = uint32(costU64) //nolint:gosec
		default:
			return fmt.Errorf("unknown request cost kind: %s", rc.Type)
		}
		e.SetDynamicMetadataNumber(internalapi.AIGatewayFilterMetadataNamespace, rc.MetadataKey, float64(cost))
	}
	if f.backend.Backend.ModelNameOverride != "" {
		actualModel := f.backend.Backend.ModelNameOverride
		e.SetDynamicMetadataString(internalapi.AIGatewayFilterMetadataNamespace,
			"model_name_override", actualModel)
	}
	e.SetDynamicMetadataString(internalapi.AIGatewayFilterMetadataNamespace,
		"backend_name", f.backend.Backend.Name)

	if f.routerFilter.stream {
		timeToFirstTokenMs := f.metrics.GetTimeToFirstTokenMs()
		interTokenLatencyMs := f.metrics.GetInterTokenLatencyMs()
		e.SetDynamicMetadataNumber(internalapi.AIGatewayFilterMetadataNamespace,
			"token_latency_ttft", timeToFirstTokenMs)
		e.SetDynamicMetadataNumber(internalapi.AIGatewayFilterMetadataNamespace,
			"token_latency_itl", interTokenLatencyMs)
	}
	return nil
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseBodyOnError(e sdk.EnvoyHTTPFilter) (err error) {
	defer func() {
		f.metrics.RecordRequestCompletion(context.Background(), false, f.reqHeaders)
	}()

	body, ok := e.GetBufferedResponseBody()
	if !ok {
		return errors.New("failed to get response body for error handling")
	}
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		return fmt.Errorf("failed to read response body for error handling: %w", err)
	}
	originalLen := len(bodyBytes)
	if f.debugLogEnabled {
		f.logger.Debug("upstream response body on error processing started",
			slog.Int("original_length", originalLen),
			slog.String("body", string(bodyBytes)))
	}

	decodingResult, err := decodeContentIfNeeded(bytes.NewBuffer(bodyBytes), f.resHeaders["content-encoding"])
	if err != nil {
		return err
	}

	var newHeaders []internalapi.Header
	var newBody []byte
	newHeaders, newBody, err = f.translator.ResponseError(f.resHeaders, decodingResult.reader)
	if err != nil {
		return fmt.Errorf("failed to transform response error: %w", err)
	}
	span := f.routerFilter.span
	if span != nil {
		b := newBody
		if b == nil {
			b = bodyBytes
		}
		statusCode := f.resHeaders[":status"]
		code, _ := strconv.Atoi(statusCode)
		span.EndSpanOnError(code, b)
	}

	for _, h := range newHeaders {
		if !e.SetResponseHeader(h.Key(), []byte(h.Value())) {
			return fmt.Errorf(
				"failed to set transformed error header %s: %s", h.Key(), h.Value())
		}
	}
	if newBody != nil {
		if f.debugLogEnabled {
			f.logger.Debug("replacing error response body",
				slog.Int("original_length", originalLen),
				slog.Int("new_length", len(newBody)),
				slog.String("new_body", string(newBody)))
		}
		ok = e.DrainBufferedResponseBody(originalLen)
		if !ok {
			return errors.New("failed to drain current response body for replacement")
		}
		ok = e.AppendBufferedResponseBody(newBody)
		if !ok {
			return errors.New("failed to append new response body for replacement")
		}

		// Set the content-length header with the new body length.
		if !e.SetResponseHeader("content-length", []byte(strconv.Itoa(len(newBody)))) {
			return errors.New("failed to set content-length header")
		}
	}
	return nil
}

// contentDecodingResult contains the result of content decoding operation.
type contentDecodingResult struct {
	reader    io.Reader
	isEncoded bool
}

// decodeContentIfNeeded decompresses the response body based on the content-encoding header.
// Currently, supports gzip and brotli encoding, but can be extended to support other encodings in the future.
// Returns a reader for the (potentially decompressed) body and metadata about the encoding.
func decodeContentIfNeeded(base io.Reader, contentEncoding string) (contentDecodingResult, error) {
	switch contentEncoding {
	case "gzip":
		reader, err := gzip.NewReader(base)
		if err != nil {
			return contentDecodingResult{}, fmt.Errorf("failed to decode gzip: %w", err)
		}
		return contentDecodingResult{reader: reader, isEncoded: true}, nil
	case "br":
		reader := brotli.NewReader(base)
		return contentDecodingResult{reader: reader, isEncoded: true}, nil
	default:
		return contentDecodingResult{reader: base, isEncoded: false}, nil
	}
}
