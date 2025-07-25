// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

func init() {
	SchemeBuilder.Register(&AIGatewayRoute{}, &AIGatewayRouteList{})
	SchemeBuilder.Register(&AIServiceBackend{}, &AIServiceBackendList{})
	SchemeBuilder.Register(&BackendSecurityPolicy{}, &BackendSecurityPolicyList{})
}

const GroupName = "aigateway.envoyproxy.io"

var (
	// schemeGroupVersion is group version used to register these objects.
	schemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: "v1alpha1"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = &scheme.Builder{GroupVersion: schemeGroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)
