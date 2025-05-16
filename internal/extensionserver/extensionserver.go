// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3http "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	upstream_codecv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/upstream_codec/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	httpv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"github.com/go-logr/logr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

// Server is the implementation of the EnvoyGatewayExtensionServer interface.
type Server struct {
	egextension.UnimplementedEnvoyGatewayExtensionServer
	log       logr.Logger
	k8sClient client.Client
}

const serverName = "envoy-gateway-extension-server"

// New creates a new instance of the extension server that implements the EnvoyGatewayExtensionServer interface.
func New(k8sClient client.Client, logger logr.Logger) *Server {
	logger = logger.WithName(serverName)
	return &Server{log: logger, k8sClient: k8sClient}
}

// Check implements [grpc_health_v1.HealthServer].
func (s *Server) Check(context.Context, *grpc_health_v1.HealthCheckRequest) (*grpc_health_v1.HealthCheckResponse, error) {
	return &grpc_health_v1.HealthCheckResponse{Status: grpc_health_v1.HealthCheckResponse_SERVING}, nil
}

// Watch implements [grpc_health_v1.HealthServer].
func (s *Server) Watch(*grpc_health_v1.HealthCheckRequest, grpc_health_v1.Health_WatchServer) error {
	return status.Error(codes.Unimplemented, "Watch is not implemented")
}

// List implements [grpc_health_v1.HealthServer].
func (s *Server) List(context.Context, *grpc_health_v1.HealthListRequest) (*grpc_health_v1.HealthListResponse, error) {
	return &grpc_health_v1.HealthListResponse{Statuses: map[string]*grpc_health_v1.HealthCheckResponse{
		serverName: {Status: grpc_health_v1.HealthCheckResponse_SERVING},
	}}, nil
}

// PostTranslateModify allows an extension to modify the clusters and secrets in the xDS config.
//
// Currently, this adds an ORIGINAL_DST cluster to the list of clusters unconditionally.
func (s *Server) PostTranslateModify(_ context.Context, req *egextension.PostTranslateModifyRequest) (*egextension.PostTranslateModifyResponse, error) {
	for _, cluster := range req.Clusters {
		s.maybeModifyCluster(cluster)
	}
	return &egextension.PostTranslateModifyResponse{Clusters: req.Clusters, Secrets: req.Secrets}, nil
}

// maybeModifyCluster mainly does two things:
//   - Populates the cluster endpoint metadata per backend. This is a workaround until
//     https://github.com/envoyproxy/gateway/issues/5523 as well as the endpoint set level metadata is supported in the extproc.
//   - Insert the upstream external processor filter to the list of filters. https://github.com/envoyproxy/gateway/issues/5881
//
// The result will look almost similar to envoy.yaml in the tests/extproc tests. Please refer to the config file for more details.
func (s *Server) maybeModifyCluster(cluster *clusterv3.Cluster) {
	// The cluster name is in the format "httproute/<namespace>/<name>/rule/<index_of_rule>".
	// We need to extract the namespace and name from the cluster name.
	parts := strings.Split(cluster.Name, "/")
	if len(parts) != 5 || parts[0] != "httproute" {
		s.log.Info("non-ai-gateway cluster name", "cluster_name", cluster.Name)
		return
	}
	httpRouteNamespace := parts[1]
	httpRouteName := parts[2]
	httpRouteRuleIndexStr := parts[4]
	httpRouteRuleIndex, err := strconv.Atoi(httpRouteRuleIndexStr)
	if err != nil {
		s.log.Error(err, "failed to parse HTTPRoute rule index",
			"cluster_name", cluster.Name, "rule_index", httpRouteRuleIndexStr)
		return
	}
	// Get the HTTPRoute object from the cluster name.
	var aigwRoute aigv1a1.AIGatewayRoute
	err = s.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: httpRouteNamespace, Name: httpRouteName}, &aigwRoute)
	if err != nil {
		s.log.Error(err, "failed to get AIGatewayRoute object",
			"namespace", httpRouteNamespace, "name", httpRouteName)
		return
	}
	// Get the backend from the HTTPRoute object.
	if httpRouteRuleIndex >= len(aigwRoute.Spec.Rules) {
		s.log.Info("HTTPRoute rule index out of range",
			"cluster_name", cluster.Name, "rule_index", httpRouteRuleIndexStr)
		return
	}

	// To determine if the target route is InferencePool, we check the header match value.
	var httpRoute gwapiv1.HTTPRoute
	err = s.k8sClient.Get(context.Background(), client.ObjectKey{Namespace: httpRouteNamespace, Name: httpRouteName}, &httpRoute)
	if err != nil {
		s.log.Error(err, "failed to get HTTPRoute object",
			"namespace", httpRouteNamespace, "name", httpRouteName)
		return
	}
	httpRouteRule := &httpRoute.Spec.Rules[httpRouteRuleIndex]
	if len(httpRouteRule.Matches) == 1 && len(httpRouteRule.Matches[0].Headers) == 1 { // This is not an http route created by the AIGatewayRoute, so skip it.
		hdrMatch := &httpRouteRule.Matches[0].Headers[0]
		if strings.Contains(hdrMatch.Value, "inferencepool") {
			s.maybeModifyClusterForInferencePool(cluster)
			return
		}
	}
	if cluster.LoadAssignment == nil {
		s.log.Info("LoadAssignment is nil", "cluster_name", cluster.Name)
		return
	}
	aigwRouteRule := &aigwRoute.Spec.Rules[httpRouteRuleIndex]
	if len(cluster.LoadAssignment.Endpoints) != len(aigwRouteRule.BackendRefs) {
		s.log.Info("LoadAssignment endpoints length does not match backend refs length",
			"cluster_name", cluster.Name, "endpoints_length", len(cluster.LoadAssignment.Endpoints), "backend_refs_length", len(httpRouteRule.BackendRefs))
		return
	}
	// Populate the metadata for each endpoint in the LoadAssignment.
	for i, endpoints := range cluster.LoadAssignment.Endpoints {
		backendRef := aigwRouteRule.BackendRefs[i]
		name := backendRef.Name
		namespace := aigwRoute.Namespace
		// We populate the same metadata for all endpoints in the LoadAssignment.
		// This is because currently, an extproc cannot retrieve the endpoint set level metadata.
		for _, endpoint := range endpoints.LbEndpoints {
			if endpoint.Metadata == nil {
				endpoint.Metadata = &corev3.Metadata{}
			}
			if endpoint.Metadata.FilterMetadata == nil {
				endpoint.Metadata.FilterMetadata = make(map[string]*structpb.Struct)
			}
			m, ok := endpoint.Metadata.FilterMetadata["aigateawy.envoy.io"]
			if !ok {
				m = &structpb.Struct{}
				endpoint.Metadata.FilterMetadata["aigateawy.envoy.io"] = m
			}
			if m.Fields == nil {
				m.Fields = make(map[string]*structpb.Value)
			}
			m.Fields["backend_name"] = structpb.NewStringValue(fmt.Sprintf("%s.%s", name, namespace))
		}
	}

	if cluster.TypedExtensionProtocolOptions == nil {
		cluster.TypedExtensionProtocolOptions = make(map[string]*anypb.Any)
	}
	const httpProtocolOptions = "envoy.extensions.upstreams.http.v3.HttpProtocolOptions"
	var po *httpv3.HttpProtocolOptions
	if raw, ok := cluster.TypedExtensionProtocolOptions[httpProtocolOptions]; ok {
		po = &httpv3.HttpProtocolOptions{}
		if err = raw.UnmarshalTo(po); err != nil {
			s.log.Error(err, "failed to unmarshal HttpProtocolOptions", "cluster_name", cluster.Name)
			return
		}
	} else {
		po = &httpv3.HttpProtocolOptions{}
		po.UpstreamProtocolOptions = &httpv3.HttpProtocolOptions_ExplicitHttpConfig_{ExplicitHttpConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig{
			ProtocolConfig: &httpv3.HttpProtocolOptions_ExplicitHttpConfig_HttpProtocolOptions{},
		}}
	}

	const upstreamExtProcNameAIGateway = "envoy.filters.http.ext_proc/aigateway"
	for _, filter := range po.HttpFilters {
		if filter.Name == upstreamExtProcNameAIGateway {
			// Nothing to do, the filter is already there.
			return
		}
	}

	extProcConfig := &extprocv3http.ExternalProcessor{}
	extProcConfig.AllowModeOverride = true
	extProcConfig.RequestAttributes = []string{"xds.upstream_host_metadata"}
	extProcConfig.ProcessingMode = &extprocv3http.ProcessingMode{
		RequestHeaderMode:  extprocv3http.ProcessingMode_SEND,
		RequestBodyMode:    extprocv3http.ProcessingMode_BUFFERED,
		ResponseHeaderMode: extprocv3http.ProcessingMode_SEND,
		ResponseBodyMode:   extprocv3http.ProcessingMode_BUFFERED,
	}
	extProcConfig.GrpcService = &corev3.GrpcService{
		TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
			EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{
				ClusterName: fmt.Sprintf(
					"envoyextensionpolicy/%s/ai-eg-route-extproc-%s/extproc/0",
					aigwRoute.Namespace,
					aigwRoute.Name,
				),
			},
		},
		Timeout: durationpb.New(30 * time.Second),
	}
	extProcConfig.MetadataOptions = &extprocv3http.MetadataOptions{
		ReceivingNamespaces: &extprocv3http.MetadataOptions_MetadataNamespaces{
			Untyped: []string{aigv1a1.AIGatewayFilterMetadataNamespace},
		},
	}
	extProcFilter := &httpconnectionmanagerv3.HttpFilter{
		Name:       upstreamExtProcNameAIGateway,
		ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{TypedConfig: mustToAny(extProcConfig)},
	}

	if len(po.HttpFilters) > 0 {
		// Insert the ext_proc filter before the last filter since the last one is always the upstream codec filter.
		last := po.HttpFilters[len(po.HttpFilters)-1]
		po.HttpFilters = po.HttpFilters[:len(po.HttpFilters)-1]
		po.HttpFilters = append(po.HttpFilters, extProcFilter, last)
	} else {
		po.HttpFilters = append(po.HttpFilters, extProcFilter)
		// We always need the upstream_code filter as a last filter.
		upstreamCodec := &httpconnectionmanagerv3.HttpFilter{}
		upstreamCodec.Name = "envoy.filters.http.upstream_codec"
		upstreamCodec.ConfigType = &httpconnectionmanagerv3.HttpFilter_TypedConfig{
			TypedConfig: mustToAny(&upstream_codecv3.UpstreamCodec{}),
		}
		po.HttpFilters = append(po.HttpFilters, upstreamCodec)
	}
	cluster.TypedExtensionProtocolOptions[httpProtocolOptions] = mustToAny(po)
}

func (s *Server) maybeModifyClusterForInferencePool(cluster *clusterv3.Cluster) {
	name := cluster.Name
	*cluster = clusterv3.Cluster{
		Name:                 name,
		ClusterDiscoveryType: &clusterv3.Cluster_Type{Type: clusterv3.Cluster_ORIGINAL_DST},
		LbPolicy:             clusterv3.Cluster_CLUSTER_PROVIDED,
		LbConfig: &clusterv3.Cluster_OriginalDstLbConfig_{
			OriginalDstLbConfig: &clusterv3.Cluster_OriginalDstLbConfig{
				// Default header name to be used for the original destination.
				// https://github.com/kubernetes-sigs/gateway-api-inference-extension/blob/c2e3fa9e5a46962374f3428374adfd8d4898696d/pkg/epp/server/runserver.go#L63
				UseHttpHeader: true, HttpHeaderName: "x-gateway-destination-endpoint",
			},
		},
		ConnectTimeout: &durationpb.Duration{Seconds: 60},
	}
}

func mustToAny(msg proto.Message) *anypb.Any {
	b, err := proto.Marshal(msg)
	if err != nil {
		panic(fmt.Sprintf("BUG: failed to marshal message: %v", err))
	}
	const envoyAPIPrefix = "type.googleapis.com/"
	return &anypb.Any{
		TypeUrl: envoyAPIPrefix + string(msg.ProtoReflect().Descriptor().FullName()),
		Value:   b,
	}
}
