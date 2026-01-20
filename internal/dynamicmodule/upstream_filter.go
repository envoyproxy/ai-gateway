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
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

type (
	// upstreamFilterConfig implements [sdk.HTTPFilterConfig].
	upstreamFilterConfig struct {
		logger *slog.Logger
		env    *Env
	}
	// upstreamFilter implements [sdk.HTTPFilter].
	upstreamFilter[ReqT, RespT, RespChunkT any, EndpointSpec endpointspec.Spec[ReqT, RespT, RespChunkT]] struct {
		logger                 *slog.Logger
		debugLogEnabled        bool
		env                    *Env
		routerFilter           *routerFilter[ReqT, RespT, RespChunkT, EndpointSpec]
		onRetry                bool
		metrics                metrics.Metrics
		translator             translator.Translator[ReqT, tracingapi.Span[RespT, RespChunkT]]
		backend                *filterapi.RuntimeBackend
		reqHeaders, resHeaders map[string]string
		// cost is the cost of the request that is accumulated during the processing of the response.
		costs metrics.TokenUsage
	}
)

func NewUpstreamFilterConfig(env *Env) sdk.HTTPFilterConfig {
	return &upstreamFilterConfig{env: env, logger: env.Logger.With(slog.String("component", "upstream_filter"))}
}

// NewFilter implements [sdk.HTTPFilterConfig].
func (f *upstreamFilterConfig) NewFilter(e sdk.EnvoyHTTPFilter) sdk.HTTPFilter {
	internalRequestID, ok := e.GetDynamicMetadataString(internalapi.AIGatewayFilterMetadataNamespace,
		internalRequestIDMetadataKey)
	if !ok {
		e.SendLocalReply(500, nil, []byte("internal server error"))
		f.logger.Error("internal request ID not found in dynamic metadata")
		return sdk.NoopHTTPFilter{}
	}
	rfs := f.env.RouterFilters
	rfs.Lock.RLock()
	rf, ok := rfs.Filters[internalRequestID]
	rfs.Lock.RUnlock()
	if !ok {
		e.SendLocalReply(500, nil, []byte("internal server error"))
		f.logger.Error("router filter not found for internal request ID", slog.String("internal_request_id", internalRequestID))
		return sdk.NoopHTTPFilter{}
	}
	switch rf.Endpoint() {
	case chatCompletionsEndpoint:
		return &upstreamFilter[openai.ChatCompletionRequest,
			openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk, endpointspec.ChatCompletionsEndpointSpec]{
			metrics:         f.env.ChatCompletionMetricsFactory.NewMetrics(),
			logger:          f.logger,
			debugLogEnabled: f.env.DebugLogEnabled,
			env:             f.env,
			routerFilter: rf.(*routerFilter[openai.ChatCompletionRequest,
				openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk, endpointspec.ChatCompletionsEndpointSpec]),
		}
	case completionsEndpoint:
		return &upstreamFilter[openai.CompletionRequest,
			openai.CompletionResponse, openai.CompletionResponse, endpointspec.CompletionsEndpointSpec]{
			metrics:         f.env.CompletionMetricsFactory.NewMetrics(),
			logger:          f.logger,
			debugLogEnabled: f.env.DebugLogEnabled,
			env:             f.env,
			routerFilter: rf.(*routerFilter[openai.CompletionRequest,
				openai.CompletionResponse, openai.CompletionResponse, endpointspec.CompletionsEndpointSpec]),
		}
	case embeddingsEndpoint:
		return &upstreamFilter[openai.EmbeddingRequest,
			openai.EmbeddingResponse, struct{}, endpointspec.EmbeddingsEndpointSpec]{
			metrics:         f.env.EmbeddingsMetricsFactory.NewMetrics(),
			logger:          f.logger,
			debugLogEnabled: f.env.DebugLogEnabled,
			env:             f.env,
			routerFilter: rf.(*routerFilter[openai.EmbeddingRequest,
				openai.EmbeddingResponse, struct{}, endpointspec.EmbeddingsEndpointSpec]),
		}
	case imagesGenerationsEndpoint:
		return &upstreamFilter[openai.ImageGenerationRequest,
			openai.ImageGenerationResponse, struct{}, endpointspec.ImageGenerationEndpointSpec]{
			metrics:         f.env.ImageGenerationMetricsFactory.NewMetrics(),
			logger:          f.logger,
			debugLogEnabled: f.env.DebugLogEnabled,
			env:             f.env,
			routerFilter: rf.(*routerFilter[openai.ImageGenerationRequest,
				openai.ImageGenerationResponse, struct{}, endpointspec.ImageGenerationEndpointSpec]),
		}
	case rerankEndpoint:
		return &upstreamFilter[cohereschema.RerankV2Request,
			cohereschema.RerankV2Response, struct{}, endpointspec.RerankEndpointSpec]{
			metrics:         f.env.RerankMetricsFactory.NewMetrics(),
			logger:          f.logger,
			debugLogEnabled: f.env.DebugLogEnabled,
			env:             f.env,
			routerFilter: rf.(*routerFilter[cohereschema.RerankV2Request,
				cohereschema.RerankV2Response, struct{}, endpointspec.RerankEndpointSpec]),
		}
	case messagesEndpoint:
		return &upstreamFilter[anthropic.MessagesRequest,
			anthropic.MessagesResponse, anthropic.MessagesStreamChunk, endpointspec.MessagesEndpointSpec]{
			metrics:         f.env.MessagesMetricsFactory.NewMetrics(),
			logger:          f.logger,
			debugLogEnabled: f.env.DebugLogEnabled,
			env:             f.env,
			routerFilter: rf.(*routerFilter[anthropic.MessagesRequest,
				anthropic.MessagesResponse, anthropic.MessagesStreamChunk, endpointspec.MessagesEndpointSpec]),
		}
	case responsesEndpoint:
		return &upstreamFilter[openai.ResponseRequest,
			openai.Response, openai.ResponseStreamEventUnion, endpointspec.ResponsesEndpointSpec]{
			metrics:         f.env.MessagesMetricsFactory.NewMetrics(),
			logger:          f.logger,
			debugLogEnabled: f.env.DebugLogEnabled,
			env:             f.env,
			routerFilter: rf.(*routerFilter[openai.ResponseRequest,
				openai.Response, openai.ResponseStreamEventUnion, endpointspec.ResponsesEndpointSpec]),
		}
	default:
		e.SendLocalReply(500, nil, []byte("BUG: unsupported endpoint at body parsing: "+rf.Endpoint().String()))
		return nil
	}
}

// OnDestroy implements [sdk.HTTPFilter].
func (f *upstreamFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) OnDestroy() {}

// RequestHeaders implements [sdk.HTTPFilter].
func (f *upstreamFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) RequestHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.RequestHeadersStatus {
	f.routerFilter.attemptCount++
	f.onRetry = f.routerFilter.attemptCount > 1
	if f.debugLogEnabled {
		f.logger.Debug("upstream filter request headers called",
			slog.Int("attempt_count", f.routerFilter.attemptCount),
			slog.Bool("on_retry", f.onRetry))
	}

	backend, ok := e.GetUpstreamHostMetadataString(internalapi.InternalEndpointMetadataNamespace, internalapi.InternalMetadataBackendNameKey)
	if !ok {
		f.logger.Error("backend name not found in upstream host metadata")
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.RequestHeadersStatusStopIteration
	}
	f.backend, ok = f.routerFilter.runtimeFilterConfig.Backends[backend]
	if !ok {
		f.logger.Error("backend not found in runtime filter config", slog.String("backend", backend))
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.RequestHeadersStatusStopIteration
	}
	f.routerFilter.upstreamFilter = f
	f.metrics.SetBackend(f.backend.Backend)
	var espec EndpointSpecT
	var err error
	f.translator, err = espec.GetTranslator(f.backend.Backend.Schema, f.backend.Backend.ModelNameOverride)
	if err != nil {
		f.logger.Error("failed to get translator for backend schema",
			slog.Any("schema", f.backend.Backend.Schema),
			slog.String("error", err.Error()))
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.RequestHeadersStatusStopIteration
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
				f.logger.Error("failed to set mutated header", slog.String("key", h.Key()))
				e.SendLocalReply(500, nil, []byte("internal server error"))
				return sdk.RequestHeadersStatusStopIteration
			}
			f.reqHeaders[h.Key()] = h.Value()
		}
		for _, key := range removes {
			if !e.SetRequestHeader(key, nil) {
				f.logger.Error("failed to remove mutated header", slog.String("key", key))
				e.SendLocalReply(500, nil, []byte("internal server error"))
				return sdk.RequestHeadersStatusStopIteration
			}
			delete(f.reqHeaders, key)
		}
	}
	return sdk.RequestHeadersStatusStopIteration
}

// RequestBody implements [sdk.HTTPFilter].
func (f *upstreamFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) RequestBody(e sdk.EnvoyHTTPFilter, endOfStream bool) sdk.RequestBodyStatus {
	if f.debugLogEnabled {
		f.logger.Debug("upstream request body called", slog.Bool("end_of_stream", endOfStream))
	}
	if !endOfStream {
		// The body is already buffered in Envoy at this point, so this should be almost no-op.
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}
	var err error
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
	newHeaders, newBody, err := f.translator.RequestBody(f.routerFilter.originalRequestBodyRaw.Bytes(),
		f.routerFilter.originalRequestBody, forceBodyMutation)
	if err != nil {
		err = fmt.Errorf("failed to translate request body for upstream: %w", err)
		f.logger.Error("request body error", slog.String("error", err.Error()))
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}
	if f.debugLogEnabled {
		f.logger.Debug("upstream request body translated",
			slog.Any("new_headers", newHeaders),
			slog.Int("new_body_length", len(newBody)),
			slog.String("new_body", string(newBody)))
	}
	for _, h := range newHeaders {
		if !e.SetRequestHeader(h.Key(), []byte(h.Value())) {
			err = fmt.Errorf("failed to set translated header %s", h.Key())
			f.logger.Error("request body error", slog.String("error", err.Error()))
			e.SendLocalReply(500, nil, []byte("internal server error"))
			return sdk.RequestBodyStatusStopIterationAndBuffer
		}
		f.reqHeaders[h.Key()] = h.Value()
	}

	if bm := b.Backend.BodyMutation; bm != nil {
		originalRaw := f.routerFilter.originalRequestBodyRaw.Bytes()
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
				err = errors.New("failed to drain current request body for replacement")
				f.logger.Error("request body error", slog.String("error", err.Error()))
				e.SendLocalReply(500, nil, []byte("internal server error"))
				return sdk.RequestBodyStatusStopIterationAndBuffer
			}
			ok = e.AppendBufferedRequestBody(newBody)
			if !ok {
				err = errors.New("failed to append new request body for replacement")
				f.logger.Error("request body error", slog.String("error", err.Error()))
				e.SendLocalReply(500, nil, []byte("internal server error"))
				return sdk.RequestBodyStatusStopIterationAndBuffer
			}
		} else {
			// On retry path, the body will be in received buffer.
			cur, ok = e.GetReceivedRequestBody()
			if !ok {
				err = errors.New("failed to get current request body for replacement")
				f.logger.Error("request body error", slog.String("error", err.Error()))
				e.SendLocalReply(500, nil, []byte("internal server error"))
				return sdk.RequestBodyStatusStopIterationAndBuffer
			}
			ok = e.DrainReceivedRequestBody(cur.Len())
			if !ok {
				err = errors.New("failed to drain current received request body for replacement")
				f.logger.Error("request body error", slog.String("error", err.Error()))
				e.SendLocalReply(500, nil, []byte("internal server error"))
				return sdk.RequestBodyStatusStopIterationAndBuffer
			}
			ok = e.AppendReceivedRequestBody(newBody)
			if !ok {
				err = errors.New("failed to append new request body for replacement")
				f.logger.Error("request body error", slog.String("error", err.Error()))
				e.SendLocalReply(500, nil, []byte("internal server error"))
				return sdk.RequestBodyStatusStopIterationAndBuffer
			}
		}

		// Set the content-length header with the new body length.
		if !e.SetRequestHeader("content-length", []byte(strconv.Itoa(len(newBody)))) {
			err = fmt.Errorf("failed to set content-length header")
			f.logger.Error("request body error", slog.String("error", err.Error()))
			e.SendLocalReply(500, nil, []byte("internal server error"))
			return sdk.RequestBodyStatusStopIterationAndBuffer
		}
		f.reqHeaders["content-length"] = strconv.Itoa(len(newBody))
	}

	// Next is to do the upstream auth if needed.
	if b.Handler != nil {
		originalOrNewBody := f.routerFilter.originalRequestBodyRaw.Bytes()
		if newBody != nil {
			originalOrNewBody = newBody
		}

		var authHeaders []internalapi.Header
		authHeaders, err = b.Handler.Do(context.Background(), f.reqHeaders, originalOrNewBody)
		if err != nil {
			err = fmt.Errorf("failed to get auth headers from handler: %w", err)
			f.logger.Error("request body error", slog.String("error", err.Error()))
			e.SendLocalReply(500, nil, []byte("internal server error"))
			return sdk.RequestBodyStatusStopIterationAndBuffer
		}
		for _, h := range authHeaders {
			if !e.SetRequestHeader(h.Key(), []byte(h.Value())) {
				err = fmt.Errorf("failed to set auth header %s", h.Key())
				f.logger.Error("request body error", slog.String("error", err.Error()))
				e.SendLocalReply(500, nil, []byte("internal server error"))
				return sdk.RequestBodyStatusStopIterationAndBuffer
			}
			f.reqHeaders[h.Key()] = h.Value()
		}
	}
	return sdk.RequestBodyStatusContinue
}

// ResponseHeaders implements [sdk.HTTPFilter].
func (f *upstreamFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseHeaders(sdk.EnvoyHTTPFilter, bool) sdk.ResponseHeadersStatus {
	return sdk.ResponseHeadersStatusContinue
}

// ResponseBody implements [sdk.HTTPFilter].
func (f *upstreamFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseBody(sdk.EnvoyHTTPFilter, bool) sdk.ResponseBodyStatus {
	return sdk.ResponseBodyStatusContinue
}

func (f *upstreamFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseHeadersImpl(e sdk.EnvoyHTTPFilter, _ bool) error {
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

func (f *upstreamFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseBodyImpl(e sdk.EnvoyHTTPFilter, endOfStream bool) (err error) {
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
	if f.routerFilter.stream {
		body, ok = e.GetReceivedResponseBody()
		if !ok {
			if f.debugLogEnabled {
				f.logger.Debug("no received response body to process",
					slog.Bool("end_of_stream", endOfStream))
			}
			body = sdk.NoopBodyReader()
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
		if f.routerFilter.stream {
			cur, ok := e.GetReceivedResponseBody()
			if ok {
				ok = e.DrainReceivedResponseBody(cur.Len())
				if !ok {
					return errors.New("failed to drain current received response body for replacement")
				}
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

func (f *upstreamFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) buildDynamicMetadataOnResponse(e sdk.EnvoyHTTPFilter, costs *metrics.TokenUsage) (err error) {
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
			cachedCreationIn, _ := costs.CacheCreationInputTokens()
			out, _ := costs.OutputTokens()
			total, _ := costs.TotalTokens()
			costU64, err := llmcostcel.EvaluateProgram(
				rc.CELProg,
				f.reqHeaders[internalapi.ModelNameHeaderKeyDefault],
				f.backend.Backend.Name,
				in,
				cachedIn,
				cachedCreationIn,
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

func (f *upstreamFilter[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseBodyOnError(e sdk.EnvoyHTTPFilter) (err error) {
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
