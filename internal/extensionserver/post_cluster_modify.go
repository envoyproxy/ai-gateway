// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	httpconnectionmanagerv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/network/http_connection_manager/v3"
	tlsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	upstreamsv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/upstreams/http/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/structpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/utils/ptr"
	gwaiev1a2 "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const (
	// internalMetadataInferencePoolKey is the key used to store the inference pool metadata.
	// This is only used within the extension server for InferencePool cluster identification.
	internalMetadataInferencePoolKey = "per_route_rule_inference_pool"

	// defaultEndpointPickerPort is the default port for Gateway API Inference Extension endpoint picker services.
	// This port is commonly used by EPP (Endpoint Picker Protocol) services as defined in the
	// Gateway API Inference Extension specification and examples.
	// See: https://gateway-api-inference-extension.sigs.k8s.io/
	defaultEndpointPickerPort = 9002
)

// PostClusterModify is called by Envoy Gateway to allow extensions to modify clusters after they are generated.
// This method specifically handles InferencePool backend references by configuring clusters with ORIGINAL_DST
// type and header-based load balancing for endpoint picker integration.
//
// The method processes BackendExtensionResources to find InferencePool resources and configures
// the corresponding clusters to work with the Gateway API Inference Extension's endpoint picker pattern.
func (s *Server) PostClusterModify(_ context.Context, req *egextension.PostClusterModifyRequest) (*egextension.PostClusterModifyResponse, error) {
	if req.Cluster == nil {
		return nil, nil
	}
	s.log.Info("Called PostClusterModify", "cluster", req.Cluster.Name)
	// Check if we have backend extension resources (InferencePool resources).
	// If no extension resources are present, this is a regular AIServiceBackend cluster.
	if req.PostClusterContext == nil || len(req.PostClusterContext.BackendExtensionResources) == 0 {
		// No backend extension resources, skip modification and return cluster as-is.
		return &egextension.PostClusterModifyResponse{Cluster: req.Cluster}, nil
	}

	// Parse InferencePool resources from BackendExtensionResources.
	// BackendExtensionResources contains unstructured Kubernetes resources that were
	// referenced in the AIGatewayRoute's BackendRefs with non-empty Group and Kind fields.
	var inferencePool *gwaiev1a2.InferencePool
	for _, resource := range req.PostClusterContext.BackendExtensionResources {
		// Unmarshal the unstructured bytes to get the Kubernetes resource.
		// The resource is stored as JSON bytes in the extension context.
		var unstructuredObj unstructured.Unstructured
		if err := json.Unmarshal(resource.UnstructuredBytes, &unstructuredObj); err != nil {
			s.log.Error(err, "failed to unmarshal extension resource", "resource_size", len(resource.UnstructuredBytes))
			continue
		}

		// Check if this is an InferencePool resource from the Gateway API Inference Extension.
		// We only process InferencePool resources; other extension resources are ignored.
		if unstructuredObj.GetAPIVersion() == "inference.networking.x-k8s.io/v1alpha2" &&
			unstructuredObj.GetKind() == "InferencePool" {
			// Convert unstructured object to strongly-typed InferencePool.
			// This allows us to access the InferencePool's spec fields safely.
			var pool gwaiev1a2.InferencePool
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, &pool); err != nil {
				s.log.Error(err, "failed to convert unstructured to InferencePool",
					"name", unstructuredObj.GetName(), "namespace", unstructuredObj.GetNamespace())
				continue
			}
			inferencePool = &pool
			// We only support one InferencePool per cluster based on CEL validation rules.
			// Multiple InferencePool backends per rule are not allowed.
			break
		}
	}

	// If we found an InferencePool, configure the cluster for ORIGINAL_DST.
	if inferencePool != nil {
		s.handleInferencePoolCluster(req.Cluster, inferencePool)
	}

	return &egextension.PostClusterModifyResponse{Cluster: req.Cluster}, nil
}

// handleInferencePoolCluster modifies clusters that have InferencePool backends to work with the
// Gateway API Inference Extension's endpoint picker pattern.
//
// This function configures the cluster with ORIGINAL_DST type and header-based load balancing,
// which allows the endpoint picker service to dynamically determine the destination endpoint
// for each request by setting the x-gateway-destination-endpoint header.
//
// The ORIGINAL_DST cluster type tells Envoy to route requests to the destination specified
// in the x-gateway-destination-endpoint header, enabling dynamic endpoint selection by the EPP.
func (s *Server) handleInferencePoolCluster(cluster *clusterv3.Cluster, inferencePool *gwaiev1a2.InferencePool) {
	s.log.Info("Handling InferencePool cluster with resource", "cluster_name", cluster.Name, "inference_pool", inferencePool.Name)

	// Configure cluster for ORIGINAL_DST with header-based load balancing.
	// ORIGINAL_DST type allows Envoy to route to destinations specified in HTTP headers.
	cluster.ClusterDiscoveryType = &clusterv3.Cluster_Type{Type: clusterv3.Cluster_ORIGINAL_DST}

	// CLUSTER_PROVIDED load balancing policy is required for ORIGINAL_DST clusters.
	cluster.LbPolicy = clusterv3.Cluster_CLUSTER_PROVIDED

	// Set a reasonable connection timeout. This is quite long to accommodate AI workloads.
	cluster.ConnectTimeout = durationpb.New(1000 * time.Second)

	// Configure original destination load balancer to use the x-gateway-destination-endpoint HTTP header.
	// The endpoint picker service will set this header to specify the target backend endpoint.
	cluster.LbConfig = &clusterv3.Cluster_OriginalDstLbConfig_{
		OriginalDstLbConfig: &clusterv3.Cluster_OriginalDstLbConfig{
			UseHttpHeader:  true,
			HttpHeaderName: "x-gateway-destination-endpoint",
		},
	}

	// Clear load balancing policy since we're using ORIGINAL_DST.
	cluster.LoadBalancingPolicy = nil

	// Remove EDS (Endpoint Discovery Service) config since we are using ORIGINAL_DST.
	// With ORIGINAL_DST, endpoints are determined dynamically via headers, not EDS.
	cluster.EdsClusterConfig = nil

	// Add InferencePool metadata to the cluster for reference by other components.
	buildMetadataForInferencePool(cluster, inferencePool)

	s.log.Info("Configured cluster for InferencePool with ORIGINAL_DST",
		"cluster_name", cluster.Name,
		"inference_pool", inferencePool.Name,
		"namespace", inferencePool.Namespace)
}

// buildMetadataForInferencePool adds InferencePool metadata to the cluster for reference by other components.
// This metadata is used to identify InferencePool clusters and extract configuration information
// needed for building external processor clusters and HTTP filters.
//
// The metadata includes the InferencePool's namespace, name, endpoint picker service name, and port,
// encoded as a string in the format: "namespace/name/serviceName/port".
func buildMetadataForInferencePool(cluster *clusterv3.Cluster, inferencePool *gwaiev1a2.InferencePool) {
	// Initialize cluster metadata structure if not present.
	if cluster.Metadata == nil {
		cluster.Metadata = &corev3.Metadata{}
	}
	if cluster.Metadata.FilterMetadata == nil {
		cluster.Metadata.FilterMetadata = make(map[string]*structpb.Struct)
	}

	// Get or create the internal metadata namespace for AI Gateway.
	m, ok := cluster.Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace]
	if !ok {
		m = &structpb.Struct{}
		cluster.Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace] = m
	}
	if m.Fields == nil {
		m.Fields = make(map[string]*structpb.Value)
	}

	// Store InferencePool reference as metadata for later retrieval.
	// The reference includes all information needed to build EPP clusters and filters.
	m.Fields[internalMetadataInferencePoolKey] = structpb.NewStringValue(
		internalapi.ClusterRefInferencePool(
			inferencePool.Namespace,
			inferencePool.Name,
			string(inferencePool.Spec.EndpointPickerConfig.ExtensionRef.Name),
			portForInferencePool(inferencePool),
		),
	)
}

// getInferencePoolByMetadata returns the InferencePool from the cluster metadata.
func getInferencePoolByMetadata(cluster *clusterv3.Cluster) *gwaiev1a2.InferencePool {
	var metadata string
	if cluster.Metadata != nil && cluster.Metadata.FilterMetadata != nil {
		m, ok := cluster.Metadata.FilterMetadata[internalapi.InternalEndpointMetadataNamespace]
		if ok && m.Fields != nil {
			v, ok := m.Fields[internalMetadataInferencePoolKey]
			if ok {
				metadata = v.GetStringValue()
			}
		}
	}

	result := strings.Split(metadata, "/")
	if len(result) != 4 {
		return nil
	}
	ns := result[0]
	name := result[1]
	serviceName := result[2]
	port, err := strconv.ParseInt(result[3], 10, 32)
	if err != nil {
		return nil
	}
	return &gwaiev1a2.InferencePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: ns,
		},
		Spec: gwaiev1a2.InferencePoolSpec{
			EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
				ExtensionRef: &gwaiev1a2.Extension{
					ExtensionReference: gwaiev1a2.ExtensionReference{
						Name:       gwaiev1a2.ObjectName(serviceName),
						PortNumber: ptr.To(gwaiev1a2.PortNumber(port)),
					},
				},
			},
		},
	}
}

// buildClustersForInferencePoolEndpointPickers builds and returns a "STRICT_DNS" cluster
// for each InferencePool's endpoint picker service.
func buildClustersForInferencePoolEndpointPickers(clusters []*clusterv3.Cluster) []*clusterv3.Cluster {
	result := make([]*clusterv3.Cluster, 0, len(clusters))
	for _, cluster := range clusters {
		if pool := getInferencePoolByMetadata(cluster); pool != nil {
			result = append(result, buildExtProcClusterForInferencePoolEndpointPicker(pool))
		}
	}

	return result
}

// buildExtProcClusterForInferencePoolEndpointPicker builds and returns a "STRICT_DNS" cluster
// for connecting to the InferencePool's endpoint picker service.
func buildExtProcClusterForInferencePoolEndpointPicker(pool *gwaiev1a2.InferencePool) *clusterv3.Cluster {
	if pool == nil {
		panic("InferencePool cannot be nil")
	}
	if pool.Spec.EndpointPickerConfig.ExtensionRef == nil {
		panic("InferencePool ExtensionRef cannot be nil")
	}

	name := clusterNameExtProcForInferencePool(pool.GetName(), pool.GetNamespace())
	c := &clusterv3.Cluster{
		Name:           name,
		ConnectTimeout: durationpb.New(10 * time.Second),
		ClusterDiscoveryType: &clusterv3.Cluster_Type{
			Type: clusterv3.Cluster_STRICT_DNS,
		},
		LbPolicy: clusterv3.Cluster_LEAST_REQUEST,
		// Ensure Envoy accepts untrusted certificates.
		TransportSocket: &corev3.TransportSocket{
			Name: "envoy.transport_sockets.tls",
			ConfigType: &corev3.TransportSocket_TypedConfig{
				TypedConfig: func() *anypb.Any {
					tlsCtx := &tlsv3.UpstreamTlsContext{
						CommonTlsContext: &tlsv3.CommonTlsContext{
							ValidationContextType: &tlsv3.CommonTlsContext_ValidationContext{},
						},
					}
					anyTLS := mustToAny(tlsCtx)
					return anyTLS
				}(),
			},
		},
		LoadAssignment: &endpointv3.ClusterLoadAssignment{
			ClusterName: name,
			Endpoints: []*endpointv3.LocalityLbEndpoints{{
				LbEndpoints: []*endpointv3.LbEndpoint{{
					HealthStatus: corev3.HealthStatus_HEALTHY,
					HostIdentifier: &endpointv3.LbEndpoint_Endpoint{
						Endpoint: &endpointv3.Endpoint{
							Address: &corev3.Address{
								Address: &corev3.Address_SocketAddress{
									SocketAddress: &corev3.SocketAddress{
										Address:  dnsNameForInferencePool(pool),
										Protocol: corev3.SocketAddress_TCP,
										PortSpecifier: &corev3.SocketAddress_PortValue{
											PortValue: portForInferencePool(pool),
										},
									},
								},
							},
						},
					},
				}},
			}},
		},
	}

	http2Opts := &upstreamsv3.HttpProtocolOptions{
		UpstreamProtocolOptions: &upstreamsv3.HttpProtocolOptions_ExplicitHttpConfig_{
			ExplicitHttpConfig: &upstreamsv3.HttpProtocolOptions_ExplicitHttpConfig{
				ProtocolConfig: &upstreamsv3.HttpProtocolOptions_ExplicitHttpConfig_Http2ProtocolOptions{
					Http2ProtocolOptions: &corev3.Http2ProtocolOptions{},
				},
			},
		},
	}

	anyHTTP2 := mustToAny(http2Opts)
	c.TypedExtensionProtocolOptions = map[string]*anypb.Any{
		"envoy.extensions.upstreams.http.v3.HttpProtocolOptions": anyHTTP2,
	}

	return c
}

// dummyHTTPFilterForInferencePool returns a dummy HTTP filter for InferencePool.
func dummyHTTPFilterForInferencePool() *httpconnectionmanagerv3.HttpFilter {
	pool := &gwaiev1a2.InferencePool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "dummy",
			Namespace: "dummy",
		},
		Spec: gwaiev1a2.InferencePoolSpec{
			EndpointPickerConfig: gwaiev1a2.EndpointPickerConfig{
				ExtensionRef: &gwaiev1a2.Extension{
					ExtensionReference: gwaiev1a2.ExtensionReference{
						Name: "dummy",
					},
				},
			},
		},
	}

	poolFilter := buildHTTPFilterForInferencePool(pool)
	return &httpconnectionmanagerv3.HttpFilter{
		Name:       extProcNameInferencePool,
		ConfigType: &httpconnectionmanagerv3.HttpFilter_TypedConfig{TypedConfig: mustToAny(poolFilter)},
		Disabled:   true,
	}
}

// buildHTTPFilterForInferencePool returns the HTTP filter for the given InferencePool.
func buildHTTPFilterForInferencePool(pool *gwaiev1a2.InferencePool) *extprocv3.ExternalProcessor {
	return &extprocv3.ExternalProcessor{
		GrpcService: &corev3.GrpcService{
			TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
				EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{
					ClusterName: clusterNameExtProcForInferencePool(
						pool.GetName(),
						pool.GetNamespace(),
					),
					Authority: authorityForInferencePool(pool),
				},
			},
		},
		ProcessingMode: &extprocv3.ProcessingMode{
			RequestHeaderMode:   extprocv3.ProcessingMode_SEND,
			RequestBodyMode:     extprocv3.ProcessingMode_FULL_DUPLEX_STREAMED,
			RequestTrailerMode:  extprocv3.ProcessingMode_SEND,
			ResponseBodyMode:    extprocv3.ProcessingMode_FULL_DUPLEX_STREAMED,
			ResponseHeaderMode:  extprocv3.ProcessingMode_SEND,
			ResponseTrailerMode: extprocv3.ProcessingMode_SEND,
		},
		MessageTimeout:   durationpb.New(5 * time.Second),
		FailureModeAllow: false,
	}
}

// authorityForInferencePool formats the gRPC authority based on the given InferencePool.
func authorityForInferencePool(pool *gwaiev1a2.InferencePool) string {
	ns := pool.GetNamespace()
	svc := pool.Spec.EndpointPickerConfig.ExtensionRef.Name
	return fmt.Sprintf("%s.%s.svc:%d", svc, ns, portForInferencePool(pool))
}

// dnsNameForInferencePool formats the DNS name based on the given InferencePool.
func dnsNameForInferencePool(pool *gwaiev1a2.InferencePool) string {
	ns := pool.GetNamespace()
	svc := pool.Spec.EndpointPickerConfig.ExtensionRef.Name
	return fmt.Sprintf("%s.%s.svc", svc, ns)
}

// portForInferencePool returns the port number for the given InferencePool.
func portForInferencePool(pool *gwaiev1a2.InferencePool) uint32 {
	if p := pool.Spec.ExtensionRef.ExtensionReference.PortNumber; p == nil {
		return defaultEndpointPickerPort
	}
	portNumber := *pool.Spec.ExtensionRef.ExtensionReference.PortNumber
	if portNumber < 0 || portNumber > 65535 {
		return defaultEndpointPickerPort // fallback to default port.
	}
	// Safe conversion: portNumber is validated to be in range [0, 65535].
	return uint32(portNumber) // #nosec G115
}

// clusterNameExtProcForInferencePool returns the name of the ext_proc cluster for the given InferencePool.
func clusterNameExtProcForInferencePool(name, ns string) string {
	return fmt.Sprintf("endpointpicker_%s_%s_ext_proc", name, ns)
}
