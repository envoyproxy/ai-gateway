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
// The entry is still emitted rather than dropped: its name is the one Envoy labels the endpoint
// with, so omitting it turns a misconfigured backend into an unknown one at request time. The first
// reason wins, since it caused everything after it.
func markUnresolved(b *filterapi.Backend, err error) {
	if b.Unresolved == "" {
		b.Unresolved = err.Error()
	}
}

// unresolvedBackendPlaceholderPrefix prefixes the name of every placeholder Backend reference.
const unresolvedBackendPlaceholderPrefix = "ai-eg-unresolved"

// unresolvedBackendRefPlaceholder builds the backendRef to put in the generated HTTPRoute in place
// of one that could not be resolved. The reference is still reported through the route's
// ResolvedRefs condition.
//
// It must stay at its original index: this controller, the Gateway controller writing the external
// processor config, and the extension server labelling endpoints all index a rule's backendRefs
// independently, and agree only while index j means the same backend to each. Dropping the entry,
// or skipping the HTTPRoute entirely, shifts every reference after it.
//
// It names a Backend that cannot exist, in the route's own namespace so Envoy Gateway reports
// BackendNotFound rather than RefNotPermitted. The weight is zero so the rule does not count as
// having an invalid backend: a non-zero weight flips NeedsClusterPerSetting, giving one cluster per
// backend instead of one cluster with a locality each, which turns backendRef.Priority failover
// into a weighted split (see site/docs/capabilities/traffic/provider-fallback.md).
func unresolvedBackendRefPlaceholder(routeNamespace, routeName string, ruleIndex, backendRefIndex int) gwapiv1.HTTPBackendRef {
	// Hash the route name rather than embed it: names run to 253 chars, the placeholder must stay valid.
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
		Weight: ptr.To[int32](0),
	}}
}
