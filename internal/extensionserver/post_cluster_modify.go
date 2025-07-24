// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"fmt"
	"time"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	"google.golang.org/protobuf/types/known/durationpb"
	gwaiev1a2 "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// clusterRefInferencePool generates a unique reference for an InferencePool cluster.
func clusterRefInferencePool(namespace, name, serviceName string, servicePort uint32) string {
	return fmt.Sprintf("%s/%s/%s/%d", namespace, name, serviceName, servicePort)
}

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

	// Check if we have backend extension resources (InferencePool resources).
	// If no extension resources are present, this is a regular AIServiceBackend cluster.
	if req.PostClusterContext == nil || len(req.PostClusterContext.BackendExtensionResources) == 0 {
		// No backend extension resources, skip modification and return cluster as-is.
		return &egextension.PostClusterModifyResponse{Cluster: req.Cluster}, nil
	}

	// Parse InferencePools resources from BackendExtensionResources.
	// BackendExtensionResources contains unstructured Kubernetes resources that were
	// referenced in the AIGatewayRoute's BackendRefs with non-empty Group and Kind fields.
	// If we found an InferencePool, configure the cluster for ORIGINAL_DST.
	if inferencePools := s.constructInferencePoolsFrom(req.PostClusterContext.BackendExtensionResources); inferencePools != nil {
		if len(inferencePools) != 1 {
			panic("BUG: at most one inferencepool can be referenced per route rule")
		}
		s.handleInferencePoolCluster(req.Cluster, inferencePools[0])
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
	// Configure cluster for ORIGINAL_DST with header-based load balancing.
	// ORIGINAL_DST type allows Envoy to route to destinations specified in HTTP headers.
	cluster.ClusterDiscoveryType = &clusterv3.Cluster_Type{Type: clusterv3.Cluster_ORIGINAL_DST}

	// CLUSTER_PROVIDED load balancing policy is required for ORIGINAL_DST clusters.
	cluster.LbPolicy = clusterv3.Cluster_CLUSTER_PROVIDED

	// Set a reasonable connection timeout. This is quite long to accommodate AI workloads.
	cluster.ConnectTimeout = durationpb.New(10 * time.Second)

	// Configure original destination load balancer to use the x-gateway-destination-endpoint HTTP header.
	// The endpoint picker service will set this header to specify the target backend endpoint.
	cluster.LbConfig = &clusterv3.Cluster_OriginalDstLbConfig_{
		OriginalDstLbConfig: &clusterv3.Cluster_OriginalDstLbConfig{
			UseHttpHeader:  true,
			HttpHeaderName: internalapi.EndpointPickerHeaderKey,
		},
	}

	// Clear load balancing policy since we're using ORIGINAL_DST.
	cluster.LoadBalancingPolicy = nil

	// Remove EDS (Endpoint Discovery Service) config since we are using ORIGINAL_DST.
	// With ORIGINAL_DST, endpoints are determined dynamically via headers, not EDS.
	cluster.EdsClusterConfig = nil

	// Add InferencePool metadata to the cluster for reference by other components.
	buildEPPMetadataForCluster(cluster, inferencePool)
}
