// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"unicode/utf8"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/cel-go/cel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/envoyproxy/ai-gateway/filterapi"
	"github.com/envoyproxy/ai-gateway/filterapi/x"
	"github.com/envoyproxy/ai-gateway/internal/extproc/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/router"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
)

var (
	sensitiveHeaderRedactedValue = []byte("[REDACTED]")
	sensitiveHeaderKeys          = []string{"authorization"}
)

// Server implements the external processor server.
type Server struct {
	logger     *slog.Logger
	config     *processorConfig
	processors map[string]ProcessorFactory
}

// NewServer creates a new external processor server.
func NewServer(logger *slog.Logger) (*Server, error) {
	srv := &Server{
		logger:     logger,
		processors: make(map[string]ProcessorFactory),
	}
	return srv, nil
}

// LoadConfig updates the configuration of the external processor.
func (s *Server) LoadConfig(ctx context.Context, config *filterapi.Config) error {
	rt, err := router.New(config, x.NewCustomRouter)
	if err != nil {
		return fmt.Errorf("cannot create router: %w", err)
	}

	var (
		backendAuthHandlers = make(map[string]backendauth.Handler)
		declaredModels      []string
	)
	for _, r := range config.Rules {
		for _, b := range r.Backends {
			if b.Auth != nil {
				backendAuthHandlers[b.Name], err = backendauth.NewHandler(ctx, b.Auth)
				if err != nil {
					return fmt.Errorf("cannot create backend auth handler: %w", err)
				}
			}
		}
		// Collect declared models from configured header routes. These will be used to
		// serve requests to the /v1/models endpoint.
		// TODO(nacx): note that currently we only support exact matching in the headers. When
		// header matching is extended, this will need to be updated.
		for _, h := range r.Headers {
			// If explicitly set to something that is not an exact match, skip.
			// If not set, we assume it's an exact match.
			//
			// Also, we only care about the AIModel header to declare models.
			if (h.Type != nil && *h.Type != gwapiv1.HeaderMatchExact) || string(h.Name) != config.ModelNameHeaderKey {
				continue
			}
			declaredModels = append(declaredModels, h.Value)
		}
	}

	costs := make([]processorConfigRequestCost, 0, len(config.LLMRequestCosts))
	for i := range config.LLMRequestCosts {
		c := &config.LLMRequestCosts[i]
		var prog cel.Program
		if c.CEL != "" {
			prog, err = llmcostcel.NewProgram(c.CEL)
			if err != nil {
				return fmt.Errorf("cannot create CEL program for cost: %w", err)
			}
		}
		costs = append(costs, processorConfigRequestCost{LLMRequestCost: c, celProg: prog})
	}

	newConfig := &processorConfig{
		uuid:                     config.UUID,
		schema:                   config.Schema,
		router:                   rt,
		selectedBackendHeaderKey: config.SelectedBackendHeaderKey,
		modelNameHeaderKey:       config.ModelNameHeaderKey,
		backendAuthHandlers:      backendAuthHandlers,
		metadataNamespace:        config.MetadataNamespace,
		requestCosts:             costs,
		declaredModels:           declaredModels,
	}
	s.config = newConfig // This is racey, but we don't care.
	return nil
}

// Register a new processor for the given request path.
func (s *Server) Register(path string, newProcessor ProcessorFactory) {
	s.processors[path] = newProcessor
}

// processorForPath returns the processor for the given path.
// Only exact path matching is supported currently
func (s *Server) processorForPath(requestHeaders map[string]string) (Processor, error) {
	path := requestHeaders[":path"]
	newProcessor, ok := s.processors[path]
	if !ok {
		return nil, fmt.Errorf("no processor defined for path: %v", path)
	}
	return newProcessor(s.config, requestHeaders, s.logger)
}

// Process implements [extprocv3.ExternalProcessorServer].
func (s *Server) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	s.logger.Debug("handling a new stream", slog.Any("config_uuid", s.config.uuid))
	ctx := stream.Context()

	// The processor will be instantiated when the first message containing the request headers is received.
	// The :path header is used to determine the processor to use, based on the registered ones.
	//
	// If this extproc filter is invoked without going through a RequestHeaders phase, that means
	// an earlier filter has already processed the request headers/bodies and decided to terminate
	// the request by sending an immediate response. In this case, we will use the passThroughProcessor
	// to pass the request through without any processing as there would be nothing to process from AI Gateway's perspective.
	var p Processor = passThroughProcessor{}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		req, err := stream.Recv()
		if errors.Is(err, io.EOF) || status.Code(err) == codes.Canceled {
			return nil
		} else if err != nil {
			s.logger.Error("cannot receive stream request", slog.String("error", err.Error()))
			return status.Errorf(codes.Unknown, "cannot receive stream request: %v", err)
		}

		// If we're processing the request headers, read the :path header to instantiate the
		// right processor.
		// Note that `req.GetRequestHeaders()` will only return non-nil if the request is
		// of type `ProcessingRequest_RequestHeaders`, so this will be executed only once per
		// request, and the processor will be instantiated only once.
		if headers := req.GetRequestHeaders().GetHeaders(); headers != nil {
			p, err = s.processorForPath(headersToMap(headers))
			if err != nil {
				s.logger.Error("cannot get processor", slog.String("error", err.Error()))
				return status.Error(codes.NotFound, err.Error())
			}
		}

		// At this point, p is guaranteed to be a valid processor either from the concrete processor or the passThroughProcessor.

		resp, err := s.processMsg(ctx, p, req)
		if err != nil {
			s.logger.Error("error processing request message", slog.String("error", err.Error()))
			return status.Errorf(codes.Unknown, "error processing request message: %v", err)
		}
		if err := stream.Send(resp); err != nil {
			s.logger.Error("cannot send response", slog.String("error", err.Error()))
			return status.Errorf(codes.Unknown, "cannot send response: %v", err)
		}
	}
}

func (s *Server) processMsg(ctx context.Context, p Processor, req *extprocv3.ProcessingRequest) (*extprocv3.ProcessingResponse, error) {
	switch value := req.Request.(type) {
	case *extprocv3.ProcessingRequest_RequestHeaders:
		requestHdrs := req.GetRequestHeaders().Headers
		// If DEBUG log level is enabled, filter sensitive headers before logging.
		if s.logger.Enabled(ctx, slog.LevelDebug) {
			filteredHdrs := filterSensitiveHeadersForLogging(requestHdrs, sensitiveHeaderKeys)
			s.logger.Debug("request headers processing", slog.Any("request_headers", filteredHdrs))
		}
		resp, err := p.ProcessRequestHeaders(ctx, requestHdrs)
		if err != nil {
			return nil, fmt.Errorf("cannot process request headers: %w", err)
		}
		s.logger.Debug("request headers processed", slog.Any("response", resp))
		return resp, nil
	case *extprocv3.ProcessingRequest_RequestBody:
		s.logger.Debug("request body processing", slog.Any("request", req))
		resp, err := p.ProcessRequestBody(ctx, value.RequestBody)
		// If DEBUG log level is enabled, filter sensitive body before logging.
		if s.logger.Enabled(ctx, slog.LevelDebug) {
			filteredBody := filterSensitiveBodyForLogging(resp, s.logger, sensitiveHeaderKeys)
			s.logger.Debug("request body processed", slog.Any("response", filteredBody))
		}
		if err != nil {
			return nil, fmt.Errorf("cannot process request body: %w", err)
		}
		return resp, nil
	case *extprocv3.ProcessingRequest_ResponseHeaders:
		responseHdrs := req.GetResponseHeaders().Headers
		s.logger.Debug("response headers processing", slog.Any("response_headers", responseHdrs))
		resp, err := p.ProcessResponseHeaders(ctx, responseHdrs)
		if err != nil {
			return nil, fmt.Errorf("cannot process response headers: %w", err)
		}
		s.logger.Debug("response headers processed", slog.Any("response", resp))
		return resp, nil
	case *extprocv3.ProcessingRequest_ResponseBody:
		s.logger.Debug("response body processing", slog.Any("request", req))
		resp, err := p.ProcessResponseBody(ctx, value.ResponseBody)
		s.logger.Debug("response body processed", slog.Any("response", resp))
		if err != nil {
			return nil, fmt.Errorf("cannot process response body: %w", err)
		}
		return resp, nil
	default:
		s.logger.Error("unknown request type", slog.Any("request", value))
		return nil, fmt.Errorf("unknown request type: %T", value)
	}
}

// Check implements [grpc_health_v1.HealthServer].
func (s *Server) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

// Watch implements [grpc_health_v1.HealthServer].
func (s *Server) Watch(*grpc_health_v1.HealthCheckRequest, grpc_health_v1.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}

// filterSensitiveHeadersForLogging filters out sensitive headers from the provided HeaderMap for logging.
// Specifically, it redacts the value of the "authorization" header and logs this action.
// This returns a slice of [slog.Attr] of headers, where the value of sensitive headers is redacted.
func filterSensitiveHeadersForLogging(headers *corev3.HeaderMap, sensitiveKeys []string) []slog.Attr {
	if headers == nil {
		return nil
	}
	filteredHeaders := make([]slog.Attr, len(headers.Headers))
	for i, header := range headers.Headers {
		// We convert the header key to lowercase to make the comparison case-insensitive but we don't modify the original header.
		if slices.Contains(sensitiveKeys, strings.ToLower(header.GetKey())) {
			filteredHeaders[i] = slog.String(header.GetKey(), string(sensitiveHeaderRedactedValue))
		} else {
			if len(header.Value) > 0 {
				filteredHeaders[i] = slog.String(header.GetKey(), header.Value)
			} else if utf8.Valid(header.RawValue) {
				filteredHeaders[i] = slog.String(header.GetKey(), string(header.RawValue))
			}
		}
	}
	return filteredHeaders
}

// filterSensitiveBodyForLogging filters out sensitive information from the response body.
// It creates a copy of the response body to avoid modifying the original body,
// as the API Key is needed for the request. The function returns a new
// ProcessingResponse with the filtered body for logging.
func filterSensitiveBodyForLogging(resp *extprocv3.ProcessingResponse, logger *slog.Logger, sensitiveKeys []string) *extprocv3.ProcessingResponse {
	if resp == nil {
		return &extprocv3.ProcessingResponse{}
	}
	original, ok := resp.Response.(*extprocv3.ProcessingResponse_RequestBody)
	if !ok {
		// Meaning this is the immediate response, that doesn't need to be filtered.
		return resp
	}
	originalHeaderMutation := original.RequestBody.Response.GetHeaderMutation()
	redactedHeaderMutation := &extprocv3.HeaderMutation{
		RemoveHeaders: originalHeaderMutation.GetRemoveHeaders(),
		SetHeaders:    make([]*corev3.HeaderValueOption, 0, len(originalHeaderMutation.GetSetHeaders())),
	}
	for _, setHeader := range originalHeaderMutation.GetSetHeaders() {
		// We convert the header key to lowercase to make the comparison case-insensitive, but we don't modify the original header.
		if slices.Contains(sensitiveKeys, strings.ToLower(setHeader.Header.GetKey())) {
			logger.Debug("filtering sensitive header", slog.String("header_key", setHeader.Header.Key))
			redactedHeaderMutation.SetHeaders = append(redactedHeaderMutation.SetHeaders, &corev3.HeaderValueOption{
				Header: &corev3.HeaderValue{
					Key:      setHeader.Header.Key,
					RawValue: sensitiveHeaderRedactedValue,
				},
			})
		} else {
			redactedHeaderMutation.SetHeaders = append(redactedHeaderMutation.SetHeaders, setHeader)
		}
	}
	return &extprocv3.ProcessingResponse{
		Response: &extprocv3.ProcessingResponse_RequestBody{
			RequestBody: &extprocv3.BodyResponse{
				Response: &extprocv3.CommonResponse{
					HeaderMutation:  redactedHeaderMutation,
					BodyMutation:    original.RequestBody.Response.GetBodyMutation(),
					ClearRouteCache: original.RequestBody.Response.GetClearRouteCache(),
				},
			},
		},
		ModeOverride: resp.ModeOverride,
	}
}

// headersToMap converts a [corev3.HeaderMap] to a Go map for easier processing.
func headersToMap(headers *corev3.HeaderMap) map[string]string {
	// TODO: handle multiple headers with the same key.
	hdrs := make(map[string]string)
	for _, h := range headers.GetHeaders() {
		if len(h.Value) > 0 {
			hdrs[h.GetKey()] = h.Value
		} else if utf8.Valid(h.RawValue) {
			hdrs[h.GetKey()] = string(h.RawValue)
		}
	}
	return hdrs
}
