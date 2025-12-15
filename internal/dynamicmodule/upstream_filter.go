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
	"strconv"
	"unsafe"

	"github.com/andybalholm/brotli"
	"github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/dynamicmodule/sdk"
	"github.com/envoyproxy/ai-gateway/internal/endpointspec"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/translator"
)

type (
	// upstreamFilterConfig implements [sdk.HTTPFilterConfig].
	upstreamFilterConfig struct{ env *Env }
	// upstreamFilter implements [sdk.HTTPFilter].
	upstreamFilter struct {
		env         *Env
		stream      bool
		success     bool
		typedFilter upstreamFilterTypedIface
	}

	upstreamFilterTypedIface interface {
		RequestHeaders(e sdk.EnvoyHTTPFilter, endOfStream bool) error
		RequestBody(e sdk.EnvoyHTTPFilter, endOfStream bool) error
		ResponseHeaders(e sdk.EnvoyHTTPFilter, endOfStream bool) error
		ResponseBody(e sdk.EnvoyHTTPFilter, endOfStream bool) error
		ResponseBodyOnError(e sdk.EnvoyHTTPFilter) error
	}

	upstreamFilterTyped[ReqT, RespT, RespChunkT any, EndpointSpec endpointspec.Spec[ReqT, RespT, RespChunkT]] struct {
		routerFilter *routerFilterTyped[ReqT, RespT, RespChunkT, EndpointSpec]
		translator   translator.Translator[ReqT, tracing.Span[RespT, RespChunkT]]
		metrics      metrics.Metrics

		onRetry                bool
		reqHeaders, resHeaders map[string]string
		backend                *filterapi.RuntimeBackend
		// cost is the cost of the request that is accumulated during the processing of the response.
		costs metrics.TokenUsage
	}
)

func NewUpstreamFilterConfig(env *Env) sdk.HTTPFilterConfig {
	return &upstreamFilterConfig{env: env}
}

// NewFilter implements [sdk.HTTPFilterConfig].
func (f *upstreamFilterConfig) NewFilter() sdk.HTTPFilter {
	return &upstreamFilter{env: f.env}
}

// RequestHeaders implements [sdk.HTTPFilter].
func (f *upstreamFilter) RequestHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.RequestHeadersStatus {
	rfPtrStr, ok := e.GetDynamicMetadataString(internalapi.AIGatewayFilterMetadataNamespace,
		routerFilterPointerDynamicMetadataKey)
	if !ok {
		e.SendLocalReply(500, nil, []byte("router filter pointer not found in dynamic metadata"))
		return sdk.RequestHeadersStatusStopIteration
	}
	rfPtr, err := strconv.ParseInt(rfPtrStr, 10, 64)
	if err != nil {
		e.SendLocalReply(500, nil, []byte(fmt.Sprintf("invalid router filter pointer: %v", err)))
		return sdk.RequestHeadersStatusStopIteration
	}
	rf := (*routerFilter)(unsafe.Pointer(uintptr(rfPtr))) // nolint:govet
	rf.attemptCount++
	onRetry := rf.attemptCount > 1
	backend, ok := e.GetUpstreamHostMetadataString(internalapi.AIGatewayFilterMetadataNamespace, internalapi.InternalMetadataBackendNameKey)
	if !ok {
		e.SendLocalReply(500, nil, []byte("backend name not found in upstream host metadata"))
		return sdk.RequestHeadersStatusStopIteration
	}
	b, ok := rf.runtimeFilterConfig.Backends[backend]
	if !ok {
		e.SendLocalReply(500, nil, []byte(fmt.Sprintf("backend %s not found in filter config", backend)))
		return sdk.RequestHeadersStatusStopIteration
	}
	f.stream = rf.typedFilter.Stream()

	switch rf.endpoint {
	case chatCompletionsEndpoint:
		f.typedFilter = &upstreamFilterTyped[openai.ChatCompletionRequest,
			openai.ChatCompletionResponse, openai.ChatCompletionResponseChunk, endpointspec.ChatCompletionsEndpointSpec]{
			metrics: f.env.ChatCompletionMetricsFactory.NewMetrics(),
			onRetry: onRetry,
			backend: b,
		}
	case completionsEndpoint:
		f.typedFilter = &upstreamFilterTyped[openai.CompletionRequest,
			openai.CompletionResponse, openai.CompletionResponse, endpointspec.CompletionsEndpointSpec]{
			onRetry: onRetry,
			metrics: f.env.CompletionMetricsFactory.NewMetrics(),
			backend: b,
		}
	case embeddingsEndpoint:
		f.typedFilter = &upstreamFilterTyped[openai.EmbeddingRequest,
			openai.EmbeddingResponse, struct{}, endpointspec.EmbeddingsEndpointSpec]{
			onRetry: onRetry,
			metrics: f.env.EmbeddingsMetricsFactory.NewMetrics(),
			backend: b,
		}
	case imagesGenerationsEndpoint:
		f.typedFilter = &upstreamFilterTyped[openai.ImageGenerationRequest,
			openai.ImageGenerationResponse, struct{}, endpointspec.ImageGenerationEndpointSpec]{
			onRetry: onRetry,
			metrics: f.env.ImageGenerationMetricsFactory.NewMetrics(),
			backend: b,
		}
	case rerankEndpoint:
		f.typedFilter = &upstreamFilterTyped[cohereschema.RerankV2Request,
			cohereschema.RerankV2Response, struct{}, endpointspec.RerankEndpointSpec]{
			onRetry: onRetry,
			metrics: f.env.RerankMetricsFactory.NewMetrics(),
			backend: b,
		}
	case messagesEndpoint:
		f.typedFilter = &upstreamFilterTyped[anthropic.MessagesRequest,
			anthropic.MessagesResponse, anthropic.MessagesStreamChunk, endpointspec.MessagesEndpointSpec]{
			onRetry: onRetry,
			metrics: f.env.MessagesMetricsFactory.NewMetrics(),
			backend: b,
		}
	default:
		e.SendLocalReply(500, nil, []byte(fmt.Sprintf("unsupported endpoint type: %v", rf.endpoint)))
		return sdk.RequestHeadersStatusStopIteration
	}
	if err := f.typedFilter.RequestHeaders(e, false); err != nil {
		// TODO: log the error in the proper way.
		fmt.Println("request headers error:", err)
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.RequestHeadersStatusStopIteration
	}
	return sdk.RequestHeadersStatusContinue
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) RequestHeaders(e sdk.EnvoyHTTPFilter, _ bool) error {
	var espec EndpointSpecT
	var err error
	f.translator, err = espec.GetTranslator(f.backend.Backend.Schema, f.backend.Backend.ModelNameOverride)
	if err != nil {
		return fmt.Errorf("failed to get translator for backend schema %s: %v", f.backend.Backend.Schema, err)
	}

	f.reqHeaders = multiValueHeadersToSingleValue(e.GetRequestHeaders())

	// Now mutate the headers based on the backend configuration.
	be := f.backend.Backend
	if hm := be.HeaderMutation; hm != nil {
		sets, removes := headermutator.NewHeaderMutator(be.HeaderMutation, f.routerFilter.originalRequestHeaders).Mutate(f.reqHeaders, f.onRetry)
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
	if !endOfStream {
		// TODO: ideally, we should not buffer the entire body for the passthrough case.
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}

	if err := f.typedFilter.RequestBody(e, endOfStream); err != nil {
		// TODO: log the error in the proper way.
		fmt.Println("request body error:", err)
		e.SendLocalReply(500, nil, []byte("internal server error"))
		return sdk.RequestBodyStatusStopIterationAndBuffer
	}
	return sdk.RequestBodyStatusContinue
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) RequestBody(e sdk.EnvoyHTTPFilter, endOfStream bool) (err error) {
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
	reqModel := cmp.Or(f.reqHeaders[internalapi.ModelNameHeaderKeyDefault], f.routerFilter.originalModel)
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
		return fmt.Errorf("failed to translate request body for upstream: %v", err)
	}
	for _, h := range newHeaders {
		if !e.SetRequestHeader(h.Key(), []byte(h.Value())) {
			return fmt.Errorf("failed to set translated header %s", h.Key())
		}
	}

	if bm := b.Backend.BodyMutation; bm != nil {
		// TODO: Apply body mutation.
		_ = bm
	}

	if newBody != nil {
		cur, ok := e.GetRequestBody()
		if !ok {
			return errors.New("failed to get current request body for replacement")
		}
		_ = e.DrainResponseBody(cur.Len())
		_ = e.AppendRequestBody(newBody)

		// Set the content-length header with the new body length.
		if !e.SetRequestHeader("content-length", []byte(strconv.Itoa(len(newBody)))) {
			return fmt.Errorf("failed to set content-length header")
		}
	}

	// Next is to do the upstream auth if needed.
	if b.Handler != nil {
		var originalOrNewBody []byte
		if newBody != nil {
			originalOrNewBody = newBody
		}

		authHeaders, err := b.Handler.Do(context.Background(), f.reqHeaders, originalOrNewBody)
		if err != nil {
			return fmt.Errorf("failed to get auth headers from handler: %v", err)
		}
		for _, h := range authHeaders {
			if !e.SetRequestHeader(h.Key(), []byte(h.Value())) {
				return fmt.Errorf("failed to set auth header %s", h.Key())
			}
		}
	}
	return nil
}

// ResponseHeaders implements [sdk.HTTPFilter].
func (f *upstreamFilter) ResponseHeaders(e sdk.EnvoyHTTPFilter, _ bool) sdk.ResponseHeadersStatus {
	if err := f.typedFilter.ResponseHeaders(e, false); err != nil {
		return sdk.ResponseHeadersStatusStopIteration
	}
	status, _ := e.GetRequestHeader(":status")
	f.success = status == "200"
	return sdk.ResponseHeadersStatusContinue
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseHeaders(e sdk.EnvoyHTTPFilter, _ bool) (err error) {
	_ = e
	defer func() {
		if err != nil {
			f.metrics.RecordRequestCompletion(context.Background(), false, f.reqHeaders)
		}
	}()

	f.resHeaders = multiValueHeadersToSingleValue(e.GetResponseHeaders())
	return nil
}

// ResponseBody implements [sdk.HTTPFilter].
func (f *upstreamFilter) ResponseBody(e sdk.EnvoyHTTPFilter, endOfStream bool) sdk.ResponseBodyStatus {
	if (!f.success || !f.stream) && !endOfStream {
		// Buffer the entire body if not streaming.
		return sdk.ResponseBodyStatusStopIterationAndBuffer
	}
	if !f.success {
		if err := f.typedFilter.ResponseBodyOnError(e, endOfStream); err != nil {
			return sdk.ResponseBodyStatusStopIterationAndBuffer
		}
	}

	if err := f.typedFilter.ResponseBody(e, endOfStream); err != nil {
		return sdk.ResponseBodyStatusStopIterationAndBuffer
	}
	return sdk.ResponseBodyStatusContinue
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseBody(e sdk.EnvoyHTTPFilter, endOfStream bool) (err error) {
	recordRequestCompletionErr := false
	defer func() {
		ctx := context.Background()
		if err != nil || recordRequestCompletionErr {
			f.metrics.RecordRequestCompletion(ctx, false, f.reqHeaders)
			return
		}
		if endOfStream {
			f.metrics.RecordRequestCompletion(ctx, true, f.reqHeaders)
		}
	}()

	reader, ok := e.GetResponseBody()
	if !ok {
		return errors.New("failed to get response body")
	}

	body, err := io.ReadAll(reader)
	if err != nil {
		return fmt.Errorf("failed to read response body: %v", err)
	}

	// Decompress the body if needed using common utility.
	decodingResult, err := decodeContentIfNeeded(bytes.NewReader(body), f.resHeaders["content-encoding"])
	if err != nil {
		return err
	}
	var newHeaders []internalapi.Header
	var newBody []byte
	newHeaders, newBody, err = f.translator.ResponseError(f.resHeaders, decodingResult.reader)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response error: %w", err)
	}

	span := f.routerFilter.span
	if span != nil {
		b := bodyMutation.GetBody()
		if b == nil {
			b = body.Body
		}
		u.parent.span.EndSpanOnError(code, b)
	}

	return nil
}

func (f *upstreamFilterTyped[ReqT, RespT, RespChunkT, EndpointSpecT]) ResponseBodyOnError(e sdk.EnvoyHTTPFilter) (err error) {
	defer func() {
		f.metrics.RecordRequestCompletion(context.Background(), false, f.reqHeaders)
	}()

	var newHeaders []internalapi.Header
	var newBody []byte
	newHeaders, newBody, err = f.translator.ResponseError(f.resHeaders, decodingResult.reader)
	if err != nil {
		return fmt.Errorf("failed to transform response error: %w", err)
	}
	if span != nil {
		b := bodyMutation.GetBody()
		if b == nil {
			b = body.Body
		}
		span.EndSpanOnError(code, b)
	}
	// Mark so the deferred handler records failure.
	recordRequestCompletionErr = true
}

// contentDecodingResult contains the result of content decoding operation.
type contentDecodingResult struct {
	reader    io.Reader
	isEncoded bool
}

// decodeContentIfNeeded decompresses the response body based on the content-encoding header.
// Currently, supports gzip and brotli encoding, but can be extended to support other encodings in the future.
// Returns a reader for the (potentially decompressed) body and metadata about the encoding.
func decodeContentIfNeeded(body io.Reader, contentEncoding string) (contentDecodingResult, error) {
	switch contentEncoding {
	case "gzip":
		reader, err := gzip.NewReader(body)
		if err != nil {
			return contentDecodingResult{}, fmt.Errorf("failed to decode gzip: %w", err)
		}
		return contentDecodingResult{
			reader:    reader,
			isEncoded: true,
		}, nil
	case "br":
		reader := brotli.NewReader(body)
		return contentDecodingResult{
			reader:    reader,
			isEncoded: true,
		}, nil
	default:
		return contentDecodingResult{
			reader:    body,
			isEncoded: false,
		}, nil
	}
}
