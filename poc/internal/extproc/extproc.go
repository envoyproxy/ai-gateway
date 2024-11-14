package extproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/extproc/translators"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit"
)

// Type aliases of the types for convenience.
type (
	requestHeaders = *extprocv3.ProcessingRequest_RequestHeaders
	// requestBody is the alias of extprocv3.ProcessingRequest_RequestBody
	// which is the type of the request body processing request.
	requestBody     = *extprocv3.ProcessingRequest_RequestBody
	responseHeaders = *extprocv3.ProcessingRequest_ResponseHeaders
	// responseBody is the alias of extprocv3.ProcessingRequest_ResponseBody
	// which is the type of the response body processing request.
	responseBody = *extprocv3.ProcessingRequest_ResponseBody
	// response is the alias of extprocv3.ProcessingResponse which must be returned by the processor.
	response = *extprocv3.ProcessingResponse
)

// Server implements the external process server.
type Server struct {
	serverCtx context.Context
	logger    *slog.Logger

	// config is the current configuration of the external processor.
	config *config

	// rateLimitServerConn is the connection to the ratelimit server.
	rateLimitServerConn *grpc.ClientConn
}

// NewServer creates a new external processor server.
func NewServer(serverCtx context.Context, rateLimitAddr string, route *aigv1a1.LLMRoute, logger *slog.Logger) (*Server, error) {
	c, needRateLimitClient, err := newConfig(route)
	if err != nil {
		return nil, fmt.Errorf("failed to create configuration: %w", err)
	}

	var conn *grpc.ClientConn
	if needRateLimitClient {
		conn, err = grpc.NewClient(rateLimitAddr,
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, fmt.Errorf("failed to connect to ratelimit server: %w", err)
		}
	}

	srv := &Server{serverCtx: serverCtx, rateLimitServerConn: conn, logger: logger, config: c}
	return srv, nil
}

// Close implements [io.Closer].
func (s *Server) Close() {
	if s.rateLimitServerConn != nil {
		_ = s.rateLimitServerConn.Close()
	}
}

// Process implements [extprocv3.ExternalProcessorServer].
func (s *Server) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	ctx := stream.Context()

	p := &processor{config: s.config, logger: s.logger, conn: s.rateLimitServerConn}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-s.serverCtx.Done():
			return s.serverCtx.Err()
		default:
		}

		req, err := stream.Recv()
		if errors.Is(err, io.EOF) || status.Code(err) == codes.Canceled {
			return nil
		} else if err != nil {
			increaseProcessFailures("receive_stream_request")
			s.logger.Error("cannot receive stream request", slog.String("error", err.Error()))
			return status.Errorf(codes.Unknown, "cannot receive stream request: %v", err)
		}

		var resp response
		switch value := req.Request.(type) {
		case requestHeaders:
			increaseProcessTotal("request_headers")
			requestHdrs := req.GetRequestHeaders().Headers
			s.logger.Debug("request headers processing",
				slog.Any("request_headers", requestHdrs), slog.Any("metadata", req.MetadataContext))
			resp, err = p.processRequestHeaders(ctx, req.MetadataContext, requestHdrs)
			s.logger.Debug("request headers processed", slog.Any("response", resp))
		case requestBody:
			increaseProcessTotal("request_body")
			s.logger.Debug("request body processing", slog.Any("request", req),
				slog.Any("metadata", req.MetadataContext))
			resp, err = p.processRequestBody(ctx, value)
			s.logger.Debug("request body processed", slog.Any("response", resp))
		case responseHeaders:
			increaseProcessTotal("response_headers")
			responseHdrs := req.GetResponseHeaders().Headers
			s.logger.Debug("response headers processing", slog.Any("response_headers", responseHdrs),
				slog.Any("metadata", req.MetadataContext))
			resp, err = p.processResponseHeaders(ctx, req.MetadataContext, responseHdrs)
		case responseBody:
			increaseProcessTotal("response_body")
			s.logger.Debug("response body processing", slog.Any("request", req))
			resp, err = p.processResponseBody(ctx, req.MetadataContext, value)
			s.logger.Debug("response body processed", slog.Any("response", resp))
		default:
			s.logger.Error("unknown request type", slog.Any("request", value))
			increaseProcessTotal("unknown")
			return status.Errorf(codes.InvalidArgument, "unknown request type: %T", value)
		}
		if err != nil {
			s.logger.Error("cannot process request", slog.String("error", err.Error()))
			return status.Errorf(codes.Unknown, "cannot process request: %v", err)
		}
		if err := stream.Send(resp); err != nil {
			increaseProcessFailures("send_stream_response")
			s.logger.Error("cannot send response", slog.String("error", err.Error()))
			return status.Errorf(codes.Unknown, "cannot send response: %v", err)
		}
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

func newConfig(route *aigv1a1.LLMRoute) (c *config, needRateLimitClient bool, err error) {
	bn := len(route.Spec.Backends)
	ret := &config{
		ratelimitDomain:      ratelimit.Domain(route),
		backendIndex:         make(map[string]int, bn),
		translatorFactories:  make([]translators.TranslatorFactory, bn),
		perBackendRateLimits: make([]*aigv1a1.LLMTrafficPolicyRateLimit, bn),
	}
	for i := range route.Spec.Backends {
		backend := &route.Spec.Backends[i]
		ret.backendIndex[backend.Name()] = i
		factory, err := translators.NewTranslatorFactory(route.Spec.Schema, backend.Schema)
		if err != nil {
			return nil, false, fmt.Errorf("failed to create translator for backend[%d] %s: %w", i, backend.Name(), err)
		}
		ret.translatorFactories[i] = factory
		if backend.TrafficPolicy != nil && backend.TrafficPolicy.RateLimit != nil &&
			len(backend.TrafficPolicy.RateLimit.Rules) > 0 {

			rl := backend.TrafficPolicy.RateLimit
			ret.perBackendRateLimits[i] = rl

			for _, rule := range rl.Rules {
				for _, l := range rule.Limits {
					if l.Type == aigv1a1.RateLimitTypeToken {
						needRateLimitClient = true
					}
				}
			}
		}
	}
	return ret, needRateLimitClient, nil
}
