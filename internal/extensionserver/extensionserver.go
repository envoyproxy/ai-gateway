// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"

	pb "github.com/envoyproxy/gateway/proto/extension"
	"github.com/go-logr/logr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// Server is the implementation of the EnvoyGatewayExtensionServer interface.
type Server struct {
	pb.UnimplementedEnvoyGatewayExtensionServer
	log logr.Logger
}

// New creates a new instance of the extension server that implements the EnvoyGatewayExtensionServer interface.
func New(logger logr.Logger) *Server {
	logger = logger.WithName("envoy-gateway-extension-server")
	return &Server{log: logger}
}

// Check implements [grpc_health_v1.HealthServer].
func (s *Server) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

// Watch implements [grpc_health_v1.HealthServer].
func (s *Server) Watch(*grpc_health_v1.HealthCheckRequest, grpc_health_v1.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}
