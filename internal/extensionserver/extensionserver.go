// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"

	pb "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/go-logr/logr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
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

const (
	originalDstClusterName = "original_destination_cluster"
	originalDstHeaderName  = "x-ai-eg-original-dst"
)

// PostTranslateModify allows an extension to modify the clusters and secrets in the xDS config.
//
// Currently, this adds an ORIGINAL_DST cluster to the list of clusters unconditionally.
func (s *Server) PostTranslateModify(_ context.Context, req *pb.PostTranslateModifyRequest) (*pb.PostTranslateModifyResponse, error) {
	for _, cluster := range req.Clusters {
		if cluster.Name == originalDstClusterName {
			// The cluster already exists, no need to add it again.
			s.log.Info("original_dst cluster already exists in the list of clusters")
			return nil, nil
		}
	}
	req.Clusters = append(req.Clusters, &clusterv3.Cluster{
		Name:                 originalDstClusterName,
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_ORIGINAL_DST},
		LbPolicy:             clusterv3.Cluster_CLUSTER_PROVIDED,
		LbConfig: &clusterv3.Cluster_OriginalDstLbConfig_{
			OriginalDstLbConfig: &clusterv3.Cluster_OriginalDstLbConfig{
				UseHttpHeader: true, HttpHeaderName: originalDstHeaderName,
			},
		},
		ConnectTimeout:  &durationpb.Duration{Seconds: 60},
		DnsLookupFamily: clusterv3.Cluster_V4_ONLY,
	})
	response := &pb.PostTranslateModifyResponse{
		Clusters: req.Clusters,
		Secrets:  req.Secrets,
	}
	s.log.Info("Added original_dst cluster to the list of clusters")
	return response, nil
}

// PostVirtualHostModify allows an extension to modify the virtual hosts in the xDS config.
//
// Currently, this adds a route that matches on the x-use-original-dst header to the virtual host.
func (s *Server) PostVirtualHostModify(_ context.Context, req *pb.PostVirtualHostModifyRequest) (*pb.PostVirtualHostModifyResponse, error) {
	if req.VirtualHost == nil {
		return nil, nil
	}
	for _, route := range req.VirtualHost.Routes {
		if route.Name == originalDstClusterName {
			// The route already exists, no need to add it again.
			s.log.Info("original_dst route already exists in the virtual host", "virtual_host", req.VirtualHost.Name)
			return nil, nil
		}
	}
	req.VirtualHost.Routes = append(req.VirtualHost.Routes, &routev3.Route{
		Name: originalDstClusterName,
		Match: &routev3.RouteMatch{
			Headers: []*routev3.HeaderMatcher{
				{
					Name: "x-ai-eg-use-original-dst",
					HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
						StringMatch: &matcherv3.StringMatcher{
							MatchPattern: &matcherv3.StringMatcher_Exact{Exact: "true"},
						},
					},
				},
			},
		},
	})
	s.log.Info("Added original_dst route to the virtual host", "virtual_host", req.VirtualHost.Name)
	return &pb.PostVirtualHostModifyResponse{VirtualHost: req.VirtualHost}, nil
}
