package extensionserver

import (
	"context"

	pb "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/go-logr/logr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/tetratelabs/ai-gateway/internal/protocov"
)

const (
	LLMRateLimitCluster = "llm_ratelimit_cluster"
)

// Server is the implementation of the EnvoyGatewayExtensionServer interface.
type Server struct {
	pb.UnimplementedEnvoyGatewayExtensionServer

	log logr.Logger

	llmRateLimitAddr string
	llmRateLimitPort uint32
}

// New creates a new instance of the extension server that implements the EnvoyGatewayExtensionServer interface.
func New(logger logr.Logger, llmRateLimitAddr string, llmRateLimitPort uint32) *Server {
	return &Server{
		log:              logger.WithName("extensionserver"),
		llmRateLimitAddr: llmRateLimitAddr,
		llmRateLimitPort: llmRateLimitPort,
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

// PostTranslateModify allows an extension to modify the clusters and secrets in the xDS config.
func (s *Server) PostTranslateModify(_ context.Context, req *pb.PostTranslateModifyRequest) (*pb.PostTranslateModifyResponse, error) {
	incReceivedEvents("PostTranslateModify")

	clusters := make([]*clusterv3.Cluster, len(req.Clusters), len(req.Clusters)+1)
	copy(clusters, req.Clusters)
	clusters = append(clusters, ratelimitCluster(s.llmRateLimitAddr, s.llmRateLimitPort))

	response := &pb.PostTranslateModifyResponse{
		Clusters: clusters,
		Secrets:  req.Secrets,
	}
	return response, nil
}

func ratelimitCluster(address string, port uint32) *clusterv3.Cluster {
	return &clusterv3.Cluster{
		Name:           LLMRateLimitCluster,
		ConnectTimeout: &durationpb.Duration{Seconds: 1},
		ClusterDiscoveryType: &clusterv3.Cluster_Type{
			Type: clusterv3.Cluster_LOGICAL_DNS,
		},
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			ClusterName: LLMRateLimitCluster,
			Endpoints: []*endpointv3.LocalityLbEndpoints{
				{
					LbEndpoints: []*endpointv3.LbEndpoint{
						{
							HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
								Endpoint: &endpointv3.Endpoint{
									Address: &corev3.Address{
										Address: &corev3.Address_SocketAddress{
											SocketAddress: &corev3.SocketAddress{
												Address: address,
												PortSpecifier: &corev3.SocketAddress_PortValue{
													PortValue: port,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		TypedExtensionProtocolOptions: map[string]*anypb.Any{
			// need to set the protocol to HTTP/2 for grpc backend
			"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": protocov.ToAny(&httpv3.HttpProtocolOptions{
				UpstreamProtocolOptions: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{
					ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
						ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{
							Http2ProtocolOptions: &corev3.Http2ProtocolOptions{},
						},
					},
				},
			}),
		},
	}
}
