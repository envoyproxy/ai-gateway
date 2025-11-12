// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"strconv"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/headermutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

func AudioTranscriptionProcessorFactory(f metrics.AudioTranscriptionMetricsFactory) ProcessorFactory {
	return func(config *processorConfig, requestHeaders map[string]string, logger *slog.Logger, tracing tracing.Tracing, isUpstreamFilter bool) (Processor, error) {
		logger = logger.With("processor", "audio-transcription", "isUpstreamFilter", fmt.Sprintf("%v", isUpstreamFilter))
		if !isUpstreamFilter {
			return &audioTranscriptionProcessorRouterFilter{
				config:         config,
				requestHeaders: requestHeaders,
				logger:         logger,
			}, nil
		}
		return &audioTranscriptionProcessorUpstreamFilter{
			config:         config,
			requestHeaders: requestHeaders,
			logger:         logger,
			metrics:        f(),
		}, nil
	}
}

type audioTranscriptionProcessorRouterFilter struct {
	passThroughProcessor
	upstreamFilter         Processor
	logger                 *slog.Logger
	config                 *processorConfig
	requestHeaders         map[string]string
	originalRequestBody    *openai.AudioTranscriptionRequest
	originalRequestBodyRaw []byte
	upstreamFilterCount    int
}

func (a *audioTranscriptionProcessorRouterFilter) ProcessResponseHeaders(ctx context.Context, headerMap *corev3.HeaderMap) (*extprocv3.ProcessingResponse, error) {
	if a.upstreamFilter != nil {
		return a.upstreamFilter.ProcessResponseHeaders(ctx, headerMap)
	}
	return a.passThroughProcessor.ProcessResponseHeaders(ctx, headerMap)
}

func (a *audioTranscriptionProcessorRouterFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	if a.upstreamFilter != nil {
		return a.upstreamFilter.ProcessResponseBody(ctx, body)
	}
	return a.passThroughProcessor.ProcessResponseBody(ctx, body)
}

func (a *audioTranscriptionProcessorRouterFilter) ProcessRequestBody(ctx context.Context, rawBody *extprocv3.HttpBody) (*extprocv3.ProcessingResponse, error) {
	contentType := a.requestHeaders["content-type"]
	model, body, err := parseAudioTranscriptionBody(rawBody, contentType)
	if err != nil {
		return nil, fmt.Errorf("failed to parse request body: %w", err)
	}

	a.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = model

	var additionalHeaders []*corev3.HeaderValueOption
	additionalHeaders = append(additionalHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: internalapi.ModelNameHeaderKeyDefault, RawValue: []byte(model)},
	}, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{Key: originalPathHeader, RawValue: []byte(a.requestHeaders[":path"])},
	})

	a.originalRequestBody = body
	a.originalRequestBodyRaw = rawBody.Body

	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{
			RequestBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation: &extprocv3.HeaderMutation{
						SetHeaders: additionalHeaders,
					},
					ClearRouteCache: true,
				},
			},
		},
	}, nil
}

type audioTranscriptionProcessorUpstreamFilter struct {
	logger                 *slog.Logger
	config                 *processorConfig
	requestHeaders         map[string]string
	responseHeaders        map[string]string
	responseEncoding       string
	modelNameOverride      internalapi.ModelNameOverride
	backendName            string
	handler                backendauth.Handler
	headerMutator          *headermutator.HeaderMutator
	originalRequestBodyRaw []byte
	originalRequestBody    *openai.AudioTranscriptionRequest
	translator             translator.AudioTranscriptionTranslator
	onRetry                bool
	costs                  translator.LLMTokenUsage
	metrics                metrics.AudioTranscriptionMetrics
}

func (a *audioTranscriptionProcessorUpstreamFilter) selectTranslator(out filterapi.VersionedAPISchema) error {
	switch out.Name {
	case filterapi.APISchemaOpenAI:
		a.translator = translator.NewAudioTranscriptionOpenAIToOpenAITranslator(out.Version, a.modelNameOverride)
	default:
		return fmt.Errorf("unsupported API schema: backend=%s", out)
	}
	return nil
}

func (a *audioTranscriptionProcessorUpstreamFilter) ProcessRequestHeaders(ctx context.Context, _ *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			a.metrics.RecordRequestCompletion(ctx, false, a.requestHeaders)
		}
	}()

	a.metrics.StartRequest(a.requestHeaders)
	a.metrics.SetOriginalModel(a.originalRequestBody.Model)
	reqModel := cmp.Or(a.requestHeaders[internalapi.ModelNameHeaderKeyDefault], a.originalRequestBody.Model)
	a.metrics.SetRequestModel(reqModel)

	headerMutation, bodyMutation, err := a.translator.RequestBody(a.originalRequestBodyRaw, a.originalRequestBody, a.onRetry)
	if err != nil {
		return nil, fmt.Errorf("failed to transform request: %w", err)
	}

	// Log the translated request body if body mutation occurred
	if bodyMutation != nil && bodyMutation.GetBody() != nil {
		a.logger.Info("translated request body",
			slog.String("backend", a.backendName),
			slog.String("original_model", a.originalRequestBody.Model),
			slog.String("translated_body", string(bodyMutation.GetBody())),
		)
	}

	if headerMutation == nil {
		headerMutation = &extprocv3.HeaderMutation{}
	}

	if h := a.headerMutator; h != nil {
		sets, removes := a.headerMutator.Mutate(a.requestHeaders, a.onRetry)
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
		a.requestHeaders[h.Header.Key] = string(h.Header.RawValue)
	}
	if h := a.handler; h != nil {
		var hdrs []internalapi.Header
		hdrs, err = h.Do(ctx, a.requestHeaders, bodyMutation.GetBody())
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

func (a *audioTranscriptionProcessorUpstreamFilter) ProcessRequestBody(context.Context, *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	panic("BUG: ProcessRequestBody should not be called in the upstream filter")
}

func (a *audioTranscriptionProcessorUpstreamFilter) ProcessResponseHeaders(ctx context.Context, headers *corev3.HeaderMap) (res *extprocv3.ProcessingResponse, err error) {
	defer func() {
		if err != nil {
			a.metrics.RecordRequestCompletion(ctx, false, a.requestHeaders)
		}
	}()

	a.responseHeaders = headersToMap(headers)
	if enc := a.responseHeaders["content-encoding"]; enc != "" {
		a.responseEncoding = enc
	}
	headerMutation, err := a.translator.ResponseHeaders(a.responseHeaders)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response headers: %w", err)
	}
	return &extprocv3.ProcessingResponse{Response: &extprocv3.ProcessingResponse_ResponseHeaders{
		ResponseHeaders: &extprocv3.HeadersResponse{
			Response: &extprocv3.CommonResponse{HeaderMutation: headerMutation},
		},
	}}, nil
}

func (a *audioTranscriptionProcessorUpstreamFilter) ProcessResponseBody(ctx context.Context, body *extprocv3.HttpBody) (res *extprocv3.ProcessingResponse, err error) {
	recordRequestCompletionErr := false
	defer func() {
		if err != nil || recordRequestCompletionErr {
			a.metrics.RecordRequestCompletion(ctx, false, a.requestHeaders)
			return
		}
		if body.EndOfStream {
			a.metrics.RecordRequestCompletion(ctx, true, a.requestHeaders)
		}
	}()

	decodingResult, err := decodeContentIfNeeded(body.Body, a.responseEncoding)
	if err != nil {
		return nil, err
	}

	if code, _ := strconv.Atoi(a.responseHeaders[":status"]); !isGoodStatusCode(code) {
		var headerMutation *extprocv3.HeaderMutation
		var bodyMutation *extprocv3.BodyMutation
		headerMutation, bodyMutation, err = a.translator.ResponseError(a.responseHeaders, decodingResult.reader)
		if err != nil {
			return nil, fmt.Errorf("failed to transform response error: %w", err)
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

	headerMutation, bodyMutation, tokenUsage, responseModel, err := a.translator.ResponseBody(a.responseHeaders, decodingResult.reader, body.EndOfStream)
	if err != nil {
		return nil, fmt.Errorf("failed to transform response: %w", err)
	}

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

	a.costs.InputTokens += tokenUsage.InputTokens
	a.costs.TotalTokens += tokenUsage.TotalTokens

	a.metrics.SetResponseModel(responseModel)
	a.metrics.RecordTokenUsage(ctx, uint32(tokenUsage.InputTokens), a.requestHeaders)

	if body.EndOfStream && len(a.config.requestCosts) > 0 {
		resp.DynamicMetadata, err = buildDynamicMetadata(a.config, &a.costs, a.requestHeaders, a.backendName)
		if err != nil {
			return nil, fmt.Errorf("failed to build dynamic metadata: %w", err)
		}
	}

	return resp, nil
}

func (a *audioTranscriptionProcessorUpstreamFilter) SetBackend(ctx context.Context, b *filterapi.Backend, backendHandler backendauth.Handler, routeProcessor Processor) (err error) {
	defer func() {
		if err != nil {
			a.metrics.RecordRequestCompletion(ctx, false, a.requestHeaders)
		}
	}()
	rp, ok := routeProcessor.(*audioTranscriptionProcessorRouterFilter)
	if !ok {
		panic("BUG: expected routeProcessor to be of type *audioTranscriptionProcessorRouterFilter")
	}
	rp.upstreamFilterCount++
	a.metrics.SetBackend(b)
	a.modelNameOverride = b.ModelNameOverride
	a.backendName = b.Name
	a.originalRequestBody = rp.originalRequestBody
	a.originalRequestBodyRaw = rp.originalRequestBodyRaw
	a.onRetry = rp.upstreamFilterCount > 1
	if err = a.selectTranslator(b.Schema); err != nil {
		return fmt.Errorf("failed to select translator: %w", err)
	}
	a.handler = backendHandler
	a.headerMutator = headermutator.NewHeaderMutator(b.HeaderMutation, rp.requestHeaders)
	if a.modelNameOverride != "" {
		a.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = a.modelNameOverride
		a.metrics.SetRequestModel(a.modelNameOverride)
	}
	rp.upstreamFilter = a
	return
}

func parseAudioTranscriptionBody(body *extprocv3.HttpBody, contentType string) (modelName string, rb *openai.AudioTranscriptionRequest, err error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", nil, fmt.Errorf("failed to parse content-type: %w", err)
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		boundary, ok := params["boundary"]
		if !ok {
			return "", nil, fmt.Errorf("multipart content-type missing boundary")
		}

		req := &openai.AudioTranscriptionRequest{}
		reader := multipart.NewReader(bytes.NewReader(body.Body), boundary)
		
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", nil, fmt.Errorf("failed to read multipart part: %w", err)
			}

			formName := part.FormName()
			switch formName {
			case "model":
				modelBytes, err := io.ReadAll(part)
				if err != nil {
					return "", nil, fmt.Errorf("failed to read model field: %w", err)
				}
				req.Model = string(modelBytes)
			case "language":
				langBytes, err := io.ReadAll(part)
				if err != nil {
					return "", nil, fmt.Errorf("failed to read language field: %w", err)
				}
				req.Language = string(langBytes)
			case "prompt":
				promptBytes, err := io.ReadAll(part)
				if err != nil {
					return "", nil, fmt.Errorf("failed to read prompt field: %w", err)
				}
				req.Prompt = string(promptBytes)
			case "response_format":
				formatBytes, err := io.ReadAll(part)
				if err != nil {
					return "", nil, fmt.Errorf("failed to read response_format field: %w", err)
				}
				req.ResponseFormat = string(formatBytes)
			case "temperature":
				tempBytes, err := io.ReadAll(part)
				if err != nil {
					return "", nil, fmt.Errorf("failed to read temperature field: %w", err)
				}
				var temp float64
				if err := json.Unmarshal(tempBytes, &temp); err == nil {
					req.Temperature = &temp
				}
			case "file":
			default:
			}
			part.Close()
		}

		if req.Model == "" {
			req.Model = "whisper-1"
		}

		return req.Model, req, nil
	}

	var req openai.AudioTranscriptionRequest
	if err := json.Unmarshal(body.Body, &req); err != nil {
		return "", nil, fmt.Errorf("failed to unmarshal body: %w", err)
	}
	return req.Model, &req, nil
}

