// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"k8s.io/utils/ptr"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

// UnresolvedBackendRefReason says why a backendRef could not be resolved. The values match the
// Gateway API route condition reasons for the equivalent failures.
type UnresolvedBackendRefReason string

const (
	// UnresolvedBackendRefNotFound means the referenced AIServiceBackend does not exist.
	UnresolvedBackendRefNotFound UnresolvedBackendRefReason = "BackendNotFound"
	// UnresolvedBackendRefNotPermitted means a cross-namespace reference has no ReferenceGrant.
	UnresolvedBackendRefNotPermitted UnresolvedBackendRefReason = "RefNotPermitted"
)

// UnresolvedBackendRef records a backendRef of an AIGatewayRoute that could not be resolved to a
// backend to route to.
type UnresolvedBackendRef struct {
	// RuleIndex and BackendRefIndex locate the reference within the AIGatewayRoute spec.
	RuleIndex, BackendRefIndex int
	// Namespace and Name identify what was referenced.
	Namespace, Name string
	// Reason says why it could not be resolved.
	Reason UnresolvedBackendRefReason
	// Message carries the underlying error.
	Message string
}

// String renders the reference the way it is addressed in the AIGatewayRoute spec, so that the
// status message points at the field to fix.
func (u UnresolvedBackendRef) String() string {
	return fmt.Sprintf("rules[%d].backendRefs[%d] (%s/%s): %s: %s",
		u.RuleIndex, u.BackendRefIndex, u.Namespace, u.Name, u.Reason, u.Message)
}

// unresolvedBackendRefsError reports that the HTTPRoute was written but some backendRefs in it
// could not be resolved. It is not a sync failure: the route is programmed and the backends that
// did resolve serve traffic normally.
type unresolvedBackendRefsError struct {
	refs []UnresolvedBackendRef
}

func (e *unresolvedBackendRefsError) Error() string {
	parts := make([]string, len(e.refs))
	for i, r := range e.refs {
		parts[i] = r.String()
	}
	return fmt.Sprintf("%d unresolved backend reference(s): %s", len(e.refs), strings.Join(parts, "; "))
}

// markUnresolved records on an external processor backend why it could not be configured.
//
// The entry is still emitted rather than dropped. Its name is the one Envoy will label the
// endpoint with, so leaving it out turns a misconfigured backend into an unknown one at request
// time, which reads as a fault in the gateway rather than something to fix in the AIGatewayRoute.
// The first reason wins, since it is the one that caused everything after it.
func markUnresolved(b *filterapi.Backend, err error) {
	if b.Unresolved == "" {
		b.Unresolved = err.Error()
	}
}

// unresolvedBackendPlaceholderPrefix prefixes the name of every placeholder Backend reference.
const unresolvedBackendPlaceholderPrefix = "ai-eg-unresolved"

// unresolvedBackendRefPlaceholder builds the backendRef to put in the generated HTTPRoute in place
// of one that could not be resolved.
//
// The reference has to stay at its original index. Three components independently index a route
// rule's backendRefs — this controller, the Gateway controller that writes the external processor
// config, and the extension server that labels the endpoints Envoy Gateway generates — and they
// agree only as long as index j means the same backend to all of them. Omitting an entry, or
// declining to write the HTTPRoute at all, shifts every reference after it.
//
// It deliberately names a Backend that does not exist, in the AIGatewayRoute's own namespace so
// that Envoy Gateway reports it as BackendNotFound rather than RefNotPermitted. Envoy Gateway then
// keeps the rule, gives this reference no endpoints, and sends its share of the traffic to the
// invalid-backend-cluster, which is a 500. The name is derived from the route so a real Backend
// cannot plausibly collide with it.
func unresolvedBackendRefPlaceholder(routeNamespace, routeName string, ruleIndex, backendRefIndex int, weight *int32) gwapiv1.HTTPBackendRef {
	// Hash the route name rather than embed it, since a route name can be up to 253 characters and
	// the placeholder still has to be a valid object name.
	sum := sha256.Sum256([]byte(routeNamespace + "/" + routeName))
	name := fmt.Sprintf("%s-%s-r%d-b%d",
		unresolvedBackendPlaceholderPrefix, hex.EncodeToString(sum[:])[:8], ruleIndex, backendRefIndex)
	ns := gwapiv1.Namespace(routeNamespace)
	return gwapiv1.HTTPBackendRef{BackendRef: gwapiv1.BackendRef{
		BackendObjectReference: gwapiv1.BackendObjectReference{
			Group:     ptr.To[gwapiv1.Group]("gateway.envoyproxy.io"),
			Kind:      ptr.To[gwapiv1.Kind]("Backend"),
			Name:      gwapiv1.ObjectName(name),
			Namespace: &ns,
		},
		// Keep the original weight so the share of traffic that fails is the share that was
		// configured for this backend.
		Weight: weight,
	}}
}
