// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"context"
	"encoding/json"
	"time"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/filters/http/ext_proc/v3"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	gwaiev1a2 "sigs.k8s.io/gateway-api-inference-extension/api/v1alpha2"
)

// PostRouteModify allows an extension to modify routes after they are generated.
func (s *Server) PostRouteModify(_ context.Context, req *egextension.PostRouteModifyRequest) (*egextension.PostRouteModifyResponse, error) {
	if req.Route == nil {
		return nil, nil
	}
	s.log.Info("Called PostRouteModify", "route", req.Route.Name)
	// Check if we have backend extension resources (InferencePool resources).
	if req.PostRouteContext == nil || len(req.PostRouteContext.ExtensionResources) == 0 {
		// No backend extension resources, skip.
		return &egextension.PostRouteModifyResponse{Route: req.Route}, nil
	}

	// Parse InferencePool resources from BackendExtensionResources.
	var inferencePool *gwaiev1a2.InferencePool
	for _, resource := range req.PostRouteContext.ExtensionResources {
		// Unmarshal the unstructured bytes to get the resource.
		var unstructuredObj unstructured.Unstructured
		if err := json.Unmarshal(resource.UnstructuredBytes, &unstructuredObj); err != nil {
			s.log.Error(err, "failed to unmarshal extension resource")
			continue
		}

		// Check if this is an InferencePool resource.
		if unstructuredObj.GetAPIVersion() == "inference.networking.x-k8s.io/v1alpha2" &&
			unstructuredObj.GetKind() == "InferencePool" {
			// Convert unstructured to InferencePool.
			var pool gwaiev1a2.InferencePool
			if err := runtime.DefaultUnstructuredConverter.FromUnstructured(unstructuredObj.Object, &pool); err != nil {
				s.log.Error(err, "failed to convert unstructured to InferencePool",
					"name", unstructuredObj.GetName(), "namespace", unstructuredObj.GetNamespace())
				continue
			}
			inferencePool = &pool
			break // We only support one InferencePool per cluster based on CEL validation.
		}
	}

	// If we found an InferencePool, configure the route with the ext_proc per-route config.
	if inferencePool != nil {
		req.Route.GetRoute().HostRewriteSpecifier = &routev3.RouteAction_AutoHostRewrite{
			AutoHostRewrite: wrapperspb.Bool(false),
		}
		if req.Route.TypedPerFilterConfig == nil {
			req.Route.TypedPerFilterConfig = make(map[string]*anypb.Any)
		}
		override := &extprocv3.ExtProcPerRoute{
			Override: &extprocv3.ExtProcPerRoute_Overrides{
				Overrides: &extprocv3.ExtProcOverrides{
					GrpcService: &corev3.GrpcService{
						Timeout: durationpb.New(10 * time.Second),
						TargetSpecifier: &corev3.GrpcService_EnvoyGrpc_{
							EnvoyGrpc: &corev3.GrpcService_EnvoyGrpc{
								ClusterName: clusterNameExtProcForInferencePool(
									inferencePool.GetName(),
									inferencePool.GetNamespace(),
								),
								Authority: authorityForInferencePool(inferencePool),
							},
						},
					},
				},
			},
		}
		req.Route.TypedPerFilterConfig[extProcNameInferencePool] = mustToAny(override)
	}

	return &egextension.PostRouteModifyResponse{Route: req.Route}, nil
}
