package extproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/envoyproxy/ai-gateway/extprocconfig"
	"github.com/envoyproxy/ai-gateway/internal/extproc/router"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
)

// Server implements the external process server.
type Server struct {
	logger *slog.Logger
	config *processorConfig
}

// NewServer creates a new external processor server.
func NewServer(logger *slog.Logger) (*Server, error) {
	srv := &Server{logger: logger}
	return srv, nil
}

func (s *Server) SetConfig(config *extprocconfig.Config) error {
	bodyParser, err := router.NewRequestBodyParser(config.InputSchema)
	if err != nil {
		return fmt.Errorf("cannot create request body parser: %w", err)
	}
	rt, err := router.NewRouter(config)
	if err != nil {
		return fmt.Errorf("cannot create router: %w", err)
	}

	factories := make(map[extprocconfig.VersionedAPISchema]translator.Factory)
	for _, r := range config.Rules {
		for _, b := range r.Backends {
			if _, ok := factories[b.OutputSchema]; !ok {
				factories[b.OutputSchema], err = translator.NewFactory(config.InputSchema, b.OutputSchema)
				if err != nil {
					return fmt.Errorf("cannot create translator factory: %w", err)
				}
			}
		}
	}

	s.config = &processorConfig{
		bodyParser: bodyParser, router: rt,
		backendRoutingHeaderKey: config.BackendRoutingHeaderKey,
		factories:               factories,
	}
	return nil
}

// Process implements [extprocv3.ExternalProcessorServer].
func (s *Server) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	ctx := stream.Context()
	p := &processor{config: s.config}
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

		resp, err := s.processMsg(ctx, p, req)
		if err != nil {
			s.logger.Error("cannot process request", slog.String("error", err.Error()))
			return status.Errorf(codes.Unknown, "cannot process request: %v", err)
		}
		if err := stream.Send(resp); err != nil {
			s.logger.Error("cannot send response", slog.String("error", err.Error()))
			return status.Errorf(codes.Unknown, "cannot send response: %v", err)
		}
	}
}

func (s *Server) processMsg(ctx context.Context, p *processor, req *extprocv3.ProcessingRequest) (*extprocv3.ProcessingResponse, error) {
	switch value := req.Request.(type) {
	case *extprocv3.ProcessingRequest_RequestHeaders:
		requestHdrs := req.GetRequestHeaders().Headers
		s.logger.Debug("request headers processing", slog.Any("request_headers", requestHdrs))
		resp, err := p.processRequestHeaders(ctx, requestHdrs)
		if err != nil {
			return nil, fmt.Errorf("cannot process request headers: %w", err)
		}
		s.logger.Debug("request headers processed", slog.Any("response", resp))
		return resp, nil
	case *extprocv3.ProcessingRequest_RequestBody:
		s.logger.Debug("request body processing", slog.Any("request", req))
		resp, err := p.processRequestBody(ctx, value.RequestBody)
		s.logger.Debug("request body processed", slog.Any("response", resp))
		if err != nil {
			return nil, fmt.Errorf("cannot process request body: %w", err)
		}
		return resp, nil
	case *extprocv3.ProcessingRequest_ResponseHeaders:
		responseHdrs := req.GetResponseHeaders().Headers
		s.logger.Debug("response headers processing", slog.Any("response_headers", responseHdrs))
		resp, err := p.processResponseHeaders(ctx, responseHdrs)
		if err != nil {
			return nil, fmt.Errorf("cannot process response headers: %w", err)
		}
		s.logger.Debug("response headers processed", slog.Any("response", resp))
		return resp, nil
	case *extprocv3.ProcessingRequest_ResponseBody:
		s.logger.Debug("response body processing", slog.Any("request", req))
		resp, err := p.processResponseBody(ctx, value.ResponseBody)
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
