// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package backendpolicy resolves the BackendSecurityPolicies attached to the backends an
// AIGatewayRoute references.
//
// It is deliberately a leaf package. Both the controllers and the xDS extension server need this
// resolution, and the extension server must not depend on internal/controller: that package pulls
// in the credential rotators, the admission webhook and the whole controller-runtime manager
// surface, and a controller -> extensionserver dependency would then be a cycle.
package backendpolicy

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const (
	// IndexBackendToTargetingPolicy is the field index mapping IndexKeyFor of a backend to the
	// BackendSecurityPolicies whose targetRefs select that backend, registered by
	// controller.ApplyIndexing. It lives here alongside the lookup that uses it.
	IndexBackendToTargetingPolicy = "AIServiceBackendToTargetingBackendSecurityPolicy"

	// AIServiceBackendGroup and AIServiceBackendKind identify an AIServiceBackend targetRef.
	AIServiceBackendGroup = "aigateway.envoyproxy.io"
	AIServiceBackendKind  = "AIServiceBackend"
	// InferencePoolGroup and InferencePoolKind identify an InferencePool targetRef.
	InferencePoolGroup = "inference.networking.k8s.io"
	InferencePoolKind  = "InferencePool"
)

// TargetingPolicies returns every BackendSecurityPolicy that selects the backend called name, of
// the given group/kind, in the given namespace. A policy only ever attaches to backends in its own
// namespace, so the lookup is namespace-scoped.
//
// The API documents at most one policy per backend, but that is not enforced at admission time, so
// callers get the full list and decide what a duplicate means for them.
//
// This goes through IndexBackendToTargetingPolicy and therefore needs a client whose cache has
// that index registered. Callers that may run against a plain client want
// CredentialOverrideMetadataNamespaces, which matches in memory instead.
func TargetingPolicies(ctx context.Context, c client.Client, namespace, name, group, kind string) ([]*aigv1b1.BackendSecurityPolicy, error) {
	var list aigv1b1.BackendSecurityPolicyList
	if err := c.List(ctx, &list, client.InNamespace(namespace),
		client.MatchingFields{IndexBackendToTargetingPolicy: IndexKeyFor(name, namespace)}); err != nil {
		return nil, fmt.Errorf("failed to list BackendSecurityPolicies for %s %s/%s: %w", kind, namespace, name, err)
	}
	key := backendKey{group: group, kind: kind, name: name}
	var matching []*aigv1b1.BackendSecurityPolicy
	for i := range list.Items {
		policy := &list.Items[i]
		if !policy.DeletionTimestamp.IsZero() {
			// Treat terminating policies as gone. The finalizer-time resync is the LAST reconcile
			// a deletion triggers (the post-deletion event hits the controller's NotFound
			// early-return), so it has to compute the post-deletion state or nothing ever will.
			// The filter config and the forwarding namespaces both resolve through this package,
			// so they drop the policy together and the extproc stops requiring the metadata in
			// the same sweep that stops forwarding it.
			continue
		}
		if policyTargets(policy, key) {
			matching = append(matching, policy)
		}
	}
	return matching, nil
}

// IndexKeyFor is the IndexBackendToTargetingPolicy key of a backend; the index writer in
// internal/controller must produce the same format.
func IndexKeyFor(name, namespace string) string {
	return fmt.Sprintf("%s.%s", name, namespace)
}

// backendKey identifies a backend a policy's targetRef can select.
type backendKey struct{ group, kind, name string }

// Resolver answers credential override lookups, memoizing the per-namespace policy List. One
// Resolver spans one resolution pass: the extension server creates one per translation so a pass
// over N clusters costs one List per involved namespace instead of one per cluster, and every
// cluster sees the same snapshot. It is not safe for concurrent use and not meant to outlive the
// pass; the informer cache behind the client is the long-lived layer.
type Resolver struct {
	client client.Client
	// policies are the FromDynamicMetadata-bearing policies by namespace, nil until listed.
	policies map[string][]*aigv1b1.BackendSecurityPolicy
}

// NewResolver returns a Resolver reading through the given client.
func NewResolver(c client.Client) *Resolver {
	return &Resolver{client: c, policies: make(map[string][]*aigv1b1.BackendSecurityPolicy)}
}

// CredentialOverrideMetadataNamespaces returns the sorted, distinct
// credentialOverride.fromDynamicMetadata namespaces of the policies attached to the given backend
// references. routeNamespace is the AIGatewayRoute's namespace, used for refs that don't name one.
//
// The result is scoped to the passed refs on purpose. Envoy copies every listed namespace into the
// MetadataContext of every ProcessingRequest for the clusters it is applied to, so a cluster-wide
// union would ship one backend's JWT claims to every other backend's processor.
func (r *Resolver) CredentialOverrideMetadataNamespaces(ctx context.Context, routeNamespace string, refs []aigv1b1.AIGatewayRouteRuleBackendRef) ([]string, error) {
	perRef, err := r.CredentialOverrideMetadataNamespacesByRef(ctx, routeNamespace, refs)
	if err != nil {
		return nil, err
	}
	union := make(map[string]struct{})
	for _, namespaces := range perRef {
		for _, namespace := range namespaces {
			union[namespace] = struct{}{}
		}
	}
	return slices.Sorted(maps.Keys(union)), nil
}

// CredentialOverrideMetadataNamespacesByRef returns, for each of refs in order, the sorted,
// distinct credentialOverride.fromDynamicMetadata namespaces of the policies attached to that
// ref's backend. A ref with no such policy gets nil.
//
// The HTTPRoute annotation needs this per-backend form rather than the flat union: the extension
// server applies the namespaces per cluster, so a policy change that moves a namespace between
// two of a route's backends must change the annotation even when the union stays the same, or
// Envoy Gateway never re-translates and the gaining backend's cluster keeps its old list.
//
// This lists per namespace and matches in memory rather than going through
// IndexBackendToTargetingPolicy: it is one List for the whole ref set instead of one per ref, and
// it works with a plain uncached client, which is what the envtest harness hands the controllers.
//
// Terminating policies are excluded, same as TargetingPolicies and for the same reason: the
// finalizer-time resync is the last reconcile a deletion triggers, so it must compute the
// post-deletion state. Both the filter config and the forwarding namespaces resolve through this
// package, so the extproc stops requiring the metadata in the same sweep that stops forwarding it.
func (r *Resolver) CredentialOverrideMetadataNamespacesByRef(ctx context.Context, routeNamespace string, refs []aigv1b1.AIGatewayRouteRuleBackendRef) ([][]string, error) {
	perRef := make([][]string, len(refs))
	for i := range refs {
		ref := &refs[i]
		key := backendKey{group: AIServiceBackendGroup, kind: AIServiceBackendKind, name: ref.Name}
		if ref.IsInferencePool() {
			key.group, key.kind = InferencePoolGroup, InferencePoolKind
		}
		policies, err := r.overridePolicies(ctx, ref.GetNamespace(routeNamespace))
		if err != nil {
			return nil, err
		}
		namespaces := make(map[string]struct{})
		for _, policy := range policies {
			md := policy.Spec.CredentialOverride.FromDynamicMetadata
			if md == nil || md.Namespace == "" || !policyTargets(policy, key) {
				continue
			}
			namespaces[md.Namespace] = struct{}{}
		}
		if len(namespaces) > 0 {
			perRef[i] = slices.Sorted(maps.Keys(namespaces))
		}
	}
	return perRef, nil
}

// overridePolicies returns the namespace's policies with a credentialOverride (either source),
// listing at most once per namespace per Resolver. A policy only ever attaches to backends in its
// own namespace, so this covers every ref pointing there.
func (r *Resolver) overridePolicies(ctx context.Context, namespace string) ([]*aigv1b1.BackendSecurityPolicy, error) {
	if policies, listed := r.policies[namespace]; listed {
		return policies, nil
	}
	var list aigv1b1.BackendSecurityPolicyList
	if err := r.client.List(ctx, &list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("failed to list BackendSecurityPolicies in namespace %s: %w", namespace, err)
	}
	var withOverride []*aigv1b1.BackendSecurityPolicy
	for i := range list.Items {
		policy := &list.Items[i]
		if !policy.DeletionTimestamp.IsZero() {
			continue
		}
		if policy.Spec.CredentialOverride != nil {
			withOverride = append(withOverride, policy)
		}
	}
	r.policies[namespace] = withOverride
	return withOverride, nil
}

// policyTargets reports whether one of the policy's targetRefs selects the backend.
func policyTargets(policy *aigv1b1.BackendSecurityPolicy, key backendKey) bool {
	for _, target := range policy.Spec.TargetRefs {
		if (backendKey{group: string(target.Group), kind: string(target.Kind), name: string(target.Name)}) == key {
			return true
		}
	}
	return false
}

// RouteBackendRefs returns every backend reference in the route, in rule order.
func RouteBackendRefs(route *aigv1b1.AIGatewayRoute) []aigv1b1.AIGatewayRouteRuleBackendRef {
	var refs []aigv1b1.AIGatewayRouteRuleBackendRef
	for i := range route.Spec.Rules {
		refs = append(refs, route.Spec.Rules[i].BackendRefs...)
	}
	return refs
}

// CredentialOverrideStripHeaders returns the sorted, distinct fromRequestHeaders input headers of
// the policies attached to the given backend references. These are the headers the extproc must
// strip from every backend so the injected per-request credential never egresses; scoping to the
// refs keeps one gateway's policies from stripping headers off another gateway's traffic, and the
// BSP -> backend -> route -> gateway sync chain regenerates the filter config whenever the set
// changes. Policies with reserved header names contribute nothing; they are rejected elsewhere
// and cannot be stripped anyway.
func (r *Resolver) CredentialOverrideStripHeaders(ctx context.Context, routeNamespace string, refs []aigv1b1.AIGatewayRouteRuleBackendRef) ([]string, error) {
	headers := make(map[string]struct{})
	for i := range refs {
		ref := &refs[i]
		key := backendKey{group: AIServiceBackendGroup, kind: AIServiceBackendKind, name: ref.Name}
		if ref.IsInferencePool() {
			key.group, key.kind = InferencePoolGroup, InferencePoolKind
		}
		policies, err := r.overridePolicies(ctx, ref.GetNamespace(routeNamespace))
		if err != nil {
			return nil, err
		}
		for _, policy := range policies {
			if policy.Spec.CredentialOverride.FromRequestHeaders == nil || !policyTargets(policy, key) {
				continue
			}
			names, err := OverrideInputHeadersFromSpec(&policy.Spec)
			if err != nil {
				continue
			}
			for _, h := range names {
				headers[h] = struct{}{}
			}
		}
	}
	return slices.Sorted(maps.Keys(headers)), nil
}

// DefaultOverrideHeaderName returns the default x-aigw-* header name for the given auth type.
// For AWSCredentials this is a prefix, not a full header name: three names are derived from it.
// See internalapi.AWSCredentialOverrideHeaderNames.
func DefaultOverrideHeaderName(t aigv1b1.BackendSecurityPolicyType) string {
	switch t {
	case aigv1b1.BackendSecurityPolicyTypeAPIKey:
		return "x-aigw-api-key"
	case aigv1b1.BackendSecurityPolicyTypeAnthropicAPIKey:
		return "x-aigw-anthropic-api-key"
	case aigv1b1.BackendSecurityPolicyTypeAzureAPIKey:
		return "x-aigw-azure-api-key"
	case aigv1b1.BackendSecurityPolicyTypeAzureCredentials:
		return "x-aigw-azure-access-token"
	case aigv1b1.BackendSecurityPolicyTypeGCPCredentials:
		return "x-aigw-gcp-access-token"
	case aigv1b1.BackendSecurityPolicyTypeAWSCredentials:
		return internalapi.AWSCredentialOverrideHeaderPrefix
	default:
		return ""
	}
}

// DefaultOverrideMetadataKey returns the default metadata key for the given auth type. Same as the
// header name except for AWSCredentials, whose metadata value is one struct holding all three inputs.
func DefaultOverrideMetadataKey(t aigv1b1.BackendSecurityPolicyType) string {
	if t == aigv1b1.BackendSecurityPolicyTypeAWSCredentials {
		return internalapi.AWSCredentialOverrideMetadataKey
	}
	return DefaultOverrideHeaderName(t)
}

// ResolvedOverrideHeaderName returns the lowercased header name (or prefix, for AWS) of a
// fromRequestHeaders source, defaulting per auth type.
func ResolvedOverrideHeaderName(t aigv1b1.BackendSecurityPolicyType, src *aigv1b1.CredentialOverrideFromRequestHeaders) string {
	h := src.Header
	if h == "" {
		h = DefaultOverrideHeaderName(t)
	}
	return strings.ToLower(h)
}

// OverrideInputHeaders returns the headers a trusted filter injects for this auth type, which are
// also the ones to strip. Three for AWS, one for the rest.
func OverrideInputHeaders(t aigv1b1.BackendSecurityPolicyType, headerName string) []string {
	if t == aigv1b1.BackendSecurityPolicyTypeAWSCredentials {
		accessKeyID, secretAccessKey, sessionToken := internalapi.AWSCredentialOverrideHeaderNames(headerName)
		return []string{accessKeyID, secretAccessKey, sessionToken}
	}
	return []string{headerName}
}

// OverrideInputHeadersFromSpec derives and validates the input header names for a policy's
// fromRequestHeaders source; nil, nil for policies without one. This is the single
// derive-and-validate path shared by the policy controller's status check, the filter config
// resolution, and the strip resolution, so what gets rejected, resolved, and stripped cannot
// drift apart.
func OverrideInputHeadersFromSpec(spec *aigv1b1.BackendSecurityPolicySpec) ([]string, error) {
	o := spec.CredentialOverride
	if o == nil || o.FromRequestHeaders == nil {
		return nil, nil
	}
	headers := OverrideInputHeaders(spec.Type, ResolvedOverrideHeaderName(spec.Type, o.FromRequestHeaders))
	if err := ValidateOverrideInputHeaders(headers); err != nil {
		return nil, err
	}
	return headers, nil
}

// ValidateOverrideInputHeaders rejects header names the extproc's header mutator refuses to
// mutate, since the strip for those would silently not happen. The CRD has a CEL rule for the
// same check, but that does not cover pre-existing objects or standalone mode.
func ValidateOverrideInputHeaders(headers []string) error {
	for _, h := range headers {
		if internalapi.IsReservedRequestHeader(h) {
			return fmt.Errorf("credentialOverride input header %q uses a reserved name that cannot be stripped before the request reaches the backend", h)
		}
	}
	return nil
}
