// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/protobuf/types/known/structpb"

	cohereschema "github.com/envoyproxy/ai-gateway/internal/apischema/cohere"
	"github.com/envoyproxy/ai-gateway/internal/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/bodymutator"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

// RerankProcessorFactory returns a factory method to instantiate the rerank processor.
func RerankProcessorFactory(f metrics.RerankMetricsFactory) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, tracing tracing.Tracing, isUpstreamFilter bool) (Processor, error) {
		logger = logger.With("processor", "rerank", "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return &rerankProcessorRouterFilter{
				config:         config,
				tracer:         tracing.RerankTracer(),
				requestHeaders: requestHeaders,
				logger:         logger,
			}, nil
		}
		return &rerankProcessorUpstreamFilter{
			config:         config,
			requestHeaders: requestHeaders,
			logger:         logger,
			metrics:        f(),
		}, nil
	}
}

// rerankProcessorRouterFilter implements [Processor] for the Cohere `/cohere/v2/rerank` endpoint.
//
// This is primarily used to select the route for the request based on the model name.
type rerankProcessorRouterFilter struct {
	passThroughProcessor
	// upstreamFilter is the upstream filter that is used to process the request at the upstream filter.
	// This will be updated when the request is retried.
	//
	// On the response handling path, we don't need to do any operation until successful, so we use the implementation
	// of the upstream filter to handle the response at the router filter.
	//
	// TODO: this is a bit of a hack and dirty workaround, so revert this to a cleaner design later.
	upstreamFilter Processor
	logger         *slog.Logger
	config         *processorConfig
	requestHeaders map[string]string
	// originalRequestBody is the original request body that is passed to the upstream filter.
	// This is used to perform the transformation of the request body on the original input
	// when the request is retried.
	originalRequestBody    *cohereschema.RerankV2Request
	originalRequestBodyRaw []byte
	// upstreamFilterCount is the number of upstream filters that have been processed.
	// This is used to determine if the request is a retry request.
	upstreamFilterCount int
	// tracer is the tracer used for requests.
	tracer tracing.RerankTracer
	// span is the tracing span for this request, created in ProcessRequestBody.
	span tracing.RerankSpan
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (r *rerankProcessorRouterFilter) ProcessResponseHeaders(ctx context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// r.upstreamFilter can be nil.
	if r.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		return r.upstreamFilter.ProcessResponseHeaders(ctx, headerMap)
	}
	return r.passThroughProcessor.ProcessResponseHeaders(ctx, headerMap)
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (r *rerankProcessorRouterFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	// If the request failed to route and/or immediate response was returned before the upstream filter was set,
	// r.upstreamFilter can be nil.
	if r.upstreamFilter != nil { // See the comment on the "upstreamFilter" field.
		return r.upstreamFilter.ProcessResponseBody(ctx, body)
	}
	return r.passThroughProcessor.ProcessResponseBody(ctx, body)
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (r *rerankProcessorRouterFilter) ProcessRequestBody(ctx context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	originalModel, body, err := parseCohereRerankV2Body(rawBody)
	if err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}

	r.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = originalModel

	var additionalHeaders []*corev3.HeaderValueOption
	additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
		// Set the model name to the request header with the key `x-ai-eg-model`.
		Header: &corev3.HeaderValue{Key: internalapi.ModelNameHeaderKeyDefault, RawValue: []byte(originalModel)},
	}, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: originalPathHeader, RawValue: []byte(r.requestHeaders[":path"])},
	})
	r.originalRequestBody = body
	r.originalRequestBodyRaw = rawBody.Body

	// Tracing may need to inject headers, so create a header mutation here.
	headerMutation := &extprocv3.HeaderMutation{
		SetHeaders: additionalHeaders,
	}
	r.span = r.tracer.StartSpanAndInjectHeaders(
		ctx,
		r.requestHeaders,
		headerMutation,
		body,
		rawBody.Body,
	)

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{
			RequestBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation:  headerMutation,
					ClearRouteCache: true,
				},
			},
		},
	}, nil
}

// rerankProcessorUpstreamFilter implements [Processor] for the `/v2/rerank` endpoint at the upstream filter.
//
// This is created per retry and handles the translation as well as the authentication of the request.
type rerankProcessorUpstreamFilter struct {
	logger                 *slog.Logger
	config                 *processorConfig
	requestHeaders         map[string]string
	responseHeaders        map[string]string
	responseEncoding       string
	modelNameOverride      internalapi.ModelNameOverride
	backendName            string
	handler                backendauth.Handler
	headerMutator          *headermutator.HeaderMutator
	bodyMutator            *bodymutator.BodyMutator
	originalRequestBodyRaw []byte
	originalRequestBody    *cohereschema.RerankV2Request
	translator             translator.CohereRerankTranslator
	// onRetry is true if this is a retry request at the upstream filter.
	onRetry bool
	// cost is the cost of the request that is accumulated during the processing of the response.
	costs translator.LLMTokenUsage
	// metrics tracking.
	metrics metrics.RerankMetrics
	// span is the tracing span for this request, inherited from the router filter.
	span tracing.RerankSpan
}

// selectTranslator selects the translator based on the output schema.
func (r *rerankProcessorUpstreamFilter) selectTranslator(out filterapi.VersionedAPISchema) error {
	switch out.Name {
	case filterapi.APISchemaCohere:
		r.translator = translator.NewRerankCohereToCohereTranslator(out.Version, r.modelNameOverride, r.span)
	default:
		return fmt.Errorf("unsupported API schema: backend=%s", out)
	}
	return nil
}

// ProcessRequestHeaders implements [Processor.ProcessRequestHeaders].
//
// At the upstream filter, we already have the original request body at request headers phase.
// So, we simply do the translation and upstream auth at this stage, and send them back to Envoy
// with the status CONTINUE_AND_REPLACE. This allows Envoy to not send the request body again
// to the extproc.
func (r *rerankProcessorUpstreamFilter) ProcessRequestHeaders(ctx context.Context, _ *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			r.metrics.RecordRequestCompletion(ctx, false, r.requestHeaders)
		}
	}()

	// Start tracking metrics for this request.
	r.metrics.StartRequest(r.requestHeaders)
	// Set the original model from the request body before any overrides
	r.metrics.SetOriginalModel(r.originalRequestBody.Model)
	// Set the request model for metrics from the original model or override if applied.
	reqModel := cmp.Or(r.requestHeaders[internalapi.ModelNameHeaderKeyDefault], r.originalRequestBody.Model)
	r.metrics.SetRequestModel(reqModel)

	newHeaders, newBody, err := r.translator.RequestBody(r.originalRequestBodyRaw, r.originalRequestBody, r.onRetry)
	if err != nil {
		return nil, fmt.Errorf("failed to transform request: %w", err)
	}
	headerMutation, bodyMutation := mutationsFromTranslationResult(newHeaders, newBody)

	// Apply header mutations from the route and also restore original headers on retry.
	if h := r.headerMutator; h != nil {
		sets, removes := r.headerMutator.Mutate(r.requestHeaders, r.onRetry)
		headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, removes...)
		for _, hdr := range sets {
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
				AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
				Header: &corev3.HeaderValue{
					Key:      hdr.Key(),
					RawValue: []byte(hdr.Value()),
				},
			})
		}
	}

	for _, h := range headerMutation.SetHeaders {
		r.requestHeaders[h.Header.Key] = string(h.Header.RawValue)
	}
	if h := r.handler; h != nil {
		var hdrs []internalapi.Header
		hdrs, err = h.Do(ctx, r.requestHeaders, bodyMutation.GetBody())
		if err != nil {
			return nil, fmt.Errorf("failed to do auth request: %w", err)
		}
		for _, h := range hdrs {
			headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
				AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
				Header:       &corev3.HeaderValue{Key: h.Key(), RawValue: []byte(h.Value())},
			})
		}
	}

	var dm *structpb.Struct
	if bm := bodyMutation.GetBody(); bm != nil {
		dm = buildContentLengthDynamicMetadataOnRequest(len(bm))
	}
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestHeaders{
			RequestHeaders: &extprocv3.HeadersResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: headerMutation, BodyMutation: bodyMutation,
					Status: extprocv3.CommonResponse_CONTINUE_AND_REPLACE,
				},
			},
		},
		DynamicMetadata: dm,
	}, nil
}

// ProcessRequestBody implements [Processor.ProcessRequestBody].
func (r *rerankProcessorUpstreamFilter) ProcessRequestBody(context.Context, *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	panic("BUG: ProcessRequestBody should not be called in the upstream filter")
}

// ProcessResponseHeaders implements [Processor.ProcessResponseHeaders].
func (r *rerankProcessorUpstreamFilter) ProcessResponseHeaders(ctx context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			r.metrics.RecordRequestCompletion(ctx, false, r.requestHeaders)
		}
	}()

	r.responseHeaders = headersToMap(headers)
	if enc := r.responseHeaders["content-encoding"]; enc != "" {
		r.responseEncoding = enc
	}
	newHeaders, err := r.translator.ResponseHeaders(r.responseHeaders)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response headers: %w", err)
	}
	headerMutation, _ := mutationsFromTranslationResult(newHeaders, nil)
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{
		ResponseHeaders: &extprocv3.HeadersResponse{
			Response: &extprocv3.CommonResponse{HeaderMutation: headerMutation},
		},
	}}, nil
}

// ProcessResponseBody implements [Processor.ProcessResponseBody].
func (r *rerankProcessorUpstreamFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	recordRequestCompletionErr := false
	defer func() {
		if err != nil || recordRequestCompletionErr {
			r.metrics.RecordRequestCompletion(ctx, false, r.requestHeaders)
			return
		}
		if body.EndOfStream {
			r.metrics.RecordRequestCompletion(ctx, true, r.requestHeaders)
		}
	}()

	// Decompress the body if needed using common utility.
	decodingResult, err := decodeContentIfNeeded(body.Body, r.responseEncoding)
	if err != nil {
		return nil, err
	}

	// Assume all responses have a valid status code header.
	if code, _ := strconv.Atoi(r.responseHeaders[":status"]); !isGoodStatusCode(code) {
		var newHeaders []internalapi.Header
		var newBody []byte
		newHeaders, newBody, err = r.translator.ResponseError(r.responseHeaders, decodingResult.reader)
		if err != nil {
			return nil, fmt.Errorf("failed to transform response error: %w", err)
		}
		headerMutation, bodyMutation := mutationsFromTranslationResult(newHeaders, newBody)
		if r.span != nil {
			b := bodyMutation.GetBody()
			if b == nil {
				b = body.Body
			}
			r.span.EndSpanOnError(code, b)
		}
		recordRequestCompletionErr = true
		return &extprocv3.ProcessingResponse{
			Response: &extprocv3.ProcessingResponse_ResponseBody{
				ResponseBody: &extprocv3.BodyResponse{
					Response: &extprocv3.CommonResponse{
						HeaderMutation: headerMutation,
						BodyMutation:   bodyMutation,
					},
				},
			},
		}, nil
	}

	newHeaders, newBody, tokenUsage, responseModel, err := r.translator.ResponseBody(r.responseHeaders, decodingResult.reader, body.EndOfStream)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response: %w", err)
	}

	// Remove content-encoding header if original body encoded but was mutated in the processor.
	headerMutation, bodyMutation := mutationsFromTranslationResult(newHeaders, newBody)
	headerMutation = removeContentEncodingIfNeeded(headerMutation, bodyMutation, decodingResult.isEncoded)

	resp := &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_ResponseBody{
			ResponseBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: headerMutation,
					BodyMutation:   bodyMutation,
				},
			},
		},
	}

	// Accumulate token usage for rerank (input tokens; output tokens may be reported but not used for cost by default).
	r.costs.InputTokens += tokenUsage.InputTokens
	r.costs.OutputTokens += tokenUsage.OutputTokens
	r.costs.TotalTokens += tokenUsage.TotalTokens

	// Set the response model for metrics
	r.metrics.SetResponseModel(responseModel)

	// Update metrics with token usage (rerank records only input tokens in metrics package).
	r.metrics.RecordTokenUsage(ctx, tokenUsage.InputTokens, r.requestHeaders)

	if body.EndOfStream && len(r.config.requestCosts) > 0 {
		resp.DynamicMetadata, err = buildDynamicMetadata(r.config, &r.costs, r.requestHeaders, r.backendName)
		if err != nil {
			return nil, fmt.Errorf("failed to build dynamic metadata: %w", err)
		}
	}

	if body.EndOfStream && r.span != nil {
		r.span.EndSpan()
	}
	return resp, nil
}

// SetBackend implements [Processor.SetBackend].
func (r *rerankProcessorUpstreamFilter) SetBackend(ctx context.Context, b *filterapi.Backend, backendHandler backendauth.Handler, routeProcessor Processor) (err error) {
	defer func() {
		if err != nil {
			r.metrics.RecordRequestCompletion(ctx, false, r.requestHeaders)
		}
	}()
	rp, ok := routeProcessor.(*rerankProcessorRouterFilter)
	if !ok {
		panic("BUG: expected routeProcessor to be of type *rerankProcessorRouterFilter")
	}
	rp.upstreamFilterCount++
	r.metrics.SetBackend(b)
	r.modelNameOverride = b.ModelNameOverride
	r.backendName = b.Name
	r.originalRequestBody = rp.originalRequestBody
	r.originalRequestBodyRaw = rp.originalRequestBodyRaw
	r.onRetry = rp.upstreamFilterCount > 1
	r.span = rp.span
	if err = r.selectTranslator(b.Schema); err != nil {
		return fmt.Errorf("failed to select translator: %w", err)
	}
	r.handler = backendHandler
	r.headerMutator = headermutator.NewHeaderMutator(b.HeaderMutation, rp.requestHeaders)
	r.bodyMutator = bodymutator.NewBodyMutator(b.BodyMutation, rp.originalRequestBodyRaw)
	// Header-derived labels/CEL must be able to see the overridden request model.
	if r.modelNameOverride != "" {
		r.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = r.modelNameOverride
		// Update metrics with the overridden model
		r.metrics.SetRequestModel(r.modelNameOverride)
	}
	rp.upstreamFilter = r
	return
}

func parseCohereRerankV2Body(body *extprocv3.HttpBody) (modelName string, rb *cohereschema.RerankV2Request, err error) {
	var req cohereschema.RerankV2Request
	if err := json.Unmarshal(body.Body, &req); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	return req.Model, &req, nil
}
