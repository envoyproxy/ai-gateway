// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

func init() {
	SchemeBuilder.Register(&AIGatewayRoute{}, &AIGatewayRouteList{})
	SchemeBuilder.Register(&AIServiceBackend{}, &AIServiceBackendList{})
	SchemeBuilder.Register(&BackendSecurityPolicy{}, &BackendSecurityPolicyList{})
	SchemeBuilder.Register(&MCPRoute{}, &MCPRouteList{})
}

const GroupName = "aigateway.envoyproxy.io"

var (
	// schemeGroupVersion is group version used to register these objects.
	schemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}

	// GroupVersion is group version used to register these objects for client-gen
	GroupVersion = schemeGroupVersion

	// SchemeGroupVersion is group version used by the code generator
	SchemeGroupVersion = schemeGroupVersion

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: schemeGroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// Resource takes an unqualified resource and returns a Group qualified GroupResource
func Resource(resource string) schema.GroupResource {
	return GroupVersion.WithResource(resource).GroupResource()
}

// AddKnownTypes adds the list of known types to the given scheme for code generation.
func AddKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion,
		&AIGatewayRoute{},
		&AIGatewayRouteList{},
		&AIServiceBackend{},
		&AIServiceBackendList{},
		&BackendSecurityPolicy{},
		&BackendSecurityPolicyList{},
		&MCPRoute{},
		&MCPRouteList{},
	)
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
