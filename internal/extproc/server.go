package extproc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/cel-go/cel"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"github.com/envoyproxy/ai-gateway/extprocapi"
	"github.com/envoyproxy/ai-gateway/filterconfig"
	"github.com/envoyproxy/ai-gateway/internal/extproc/backendauth"
	"github.com/envoyproxy/ai-gateway/internal/extproc/router"
	"github.com/envoyproxy/ai-gateway/internal/extproc/translator"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
)

// Server implements the external process server.
type Server[P ProcessorIface] struct {
	logger       *slog.Logger
	config       *processorConfig
	newProcessor func(*processorConfig, *slog.Logger) P
}

// NewServer creates a new external processor server.
func NewServer[P ProcessorIface](logger *slog.Logger, newProcessor func(*processorConfig, *slog.Logger) P) (*Server[P], error) {
	srv := &Server[P]{logger: logger, newProcessor: newProcessor}
	return srv, nil
}

// LoadConfig updates the configuration of the external processor.
func (s *Server[P]) LoadConfig(config *filterconfig.Config) error {
	bodyParser, err := router.NewRequestBodyParser(config.Schema)
	if err != nil {
		return fmt.Errorf("cannot create request body parser: %w", err)
	}
	rt, err := router.NewRouter(config, extprocapi.NewCustomRouter)
	if err != nil {
		return fmt.Errorf("cannot create router: %w", err)
	}

	factories := make(map[filterconfig.VersionedAPISchema]translator.Factory)
	backendAuthHandlers := make(map[string]backendauth.Handler)
	for _, r := range config.Rules {
		for _, b := range r.Backends {
			if _, ok := factories[b.Schema]; !ok {
				factories[b.Schema], err = translator.NewFactory(config.Schema, b.Schema, config.MonitorContinuousUsageStats)
				if err != nil {
					return fmt.Errorf("cannot create translator factory: %w", err)
				}
			}

			if b.Auth != nil {
				h, err := backendauth.NewHandler(b.Auth)
				if err != nil {
					return fmt.Errorf("cannot create backend auth handler: %w", err)
				}
				backendAuthHandlers[b.Name] = h
			}
		}
	}

	costs := make([]processorConfigRequestCost, 0, len(config.LLMRequestCosts))
	for i := range config.LLMRequestCosts {
		c := &config.LLMRequestCosts[i]
		var prog cel.Program
		if c.CELExpression != "" {
			prog, err = llmcostcel.NewProgram(c.CELExpression)
			if err != nil {
				return fmt.Errorf("cannot create CEL program for cost: %w", err)
			}
		}
		costs = append(costs, processorConfigRequestCost{LLMRequestCost: c, celProg: prog})
	}

	newConfig := &processorConfig{
		bodyParser: bodyParser, router: rt,
		selectedBackendHeaderKey: config.SelectedBackendHeaderKey,
		modelNameHeaderKey:       config.ModelNameHeaderKey,
		factories:                factories,
		backendAuthHandlers:      backendAuthHandlers,
		metadataNamespace:        config.MetadataNamespace,
		requestCosts:             costs,
	}
	s.config = newConfig // This is racey, but we don't care.
	return nil
}

// Process implements [extprocv3.ExternalProcessorServer].
func (s *Server[P]) Process(stream extprocv3.ExternalProcessor_ProcessServer) error {
	p := s.newProcessor(s.config, s.logger)
	return s.process(p, stream)
}

func (s *Server[P]) process(p P, stream extprocv3.ExternalProcessor_ProcessServer) error {
	ctx := stream.Context()
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
			s.logger.Error("error processing request message", slog.String("error", err.Error()))
			return status.Errorf(codes.Unknown, "error processing request message: %v", err)
		}
		if err := stream.Send(resp); err != nil {
			s.logger.Error("cannot send response", slog.String("error", err.Error()))
			return status.Errorf(codes.Unknown, "cannot send response: %v", err)
		}
	}
}

func (s *Server[P]) processMsg(ctx context.Context, p P, req *extprocv3.ProcessingRequest) (*extprocv3.ProcessingResponse, error) {
	switch value := req.Request.(type) {
	case *extprocv3.ProcessingRequest_RequestHeaders:
		requestHdrs := req.GetRequestHeaders().Headers
		s.logger.Debug("request headers processing", slog.Any("request_headers", requestHdrs))
		resp, err := p.ProcessRequestHeaders(ctx, requestHdrs)
		if err != nil {
			return nil, fmt.Errorf("cannot process request headers: %w", err)
		}
		s.logger.Debug("request headers processed", slog.Any("response", resp))
		return resp, nil
	case *extprocv3.ProcessingRequest_RequestBody:
		s.logger.Debug("request body processing", slog.Any("request", req))
		resp, err := p.ProcessRequestBody(ctx, value.RequestBody)
		s.logger.Debug("request body processed", slog.Any("response", resp))
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
func (s *Server[P]) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

// Watch implements [grpc_health_v1.HealthServer].
func (s *Server[P]) Watch(*grpc_health_v1.HealthCheckRequest, grpc_health_v1.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}
