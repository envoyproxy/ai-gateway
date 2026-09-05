// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendpolicy_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/backendpolicy"
	"github.com/envoyproxy/ai-gateway/internal/controller"
)

// newClient builds a client with the same field indexes the controllers register on the manager.
func newClient(t *testing.T, objects ...client.Object) client.Client {
	t.Helper()
	builder := fake.NewClientBuilder().WithScheme(controller.Scheme)
	require.NoError(t, controller.ApplyIndexing(t.Context(), func(_ context.Context, obj client.Object, field string, extractValue client.IndexerFunc) error {
		builder = builder.WithIndex(obj, field, extractValue)
		return nil
	}))
	return builder.WithObjects(objects...).Build()
}

// policy builds a BackendSecurityPolicy attached to the named backend, sourcing its credential
// from metadataNamespace when that is non-empty.
func policy(name, namespace, backendName, backendKind, metadataNamespace string) *aigv1b1.BackendSecurityPolicy {
	group := backendpolicy.AIServiceBackendGroup
	if backendKind == backendpolicy.InferencePoolKind {
		group = backendpolicy.InferencePoolGroup
	}
	p := &aigv1b1.BackendSecurityPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
		Spec: aigv1b1.BackendSecurityPolicySpec{
			Type: aigv1b1.BackendSecurityPolicyTypeAPIKey,
			TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{{
				Group: gwapiv1.Group(group),
				Kind:  gwapiv1.Kind(backendKind),
				Name:  gwapiv1.ObjectName(backendName),
			}},
		},
	}
	if metadataNamespace != "" {
		p.Spec.CredentialOverride = &aigv1b1.BackendSecurityPolicyCredentialOverride{
			FromDynamicMetadata: &aigv1b1.CredentialOverrideFromDynamicMetadata{Namespace: metadataNamespace},
		}
	}
	return p
}

func backendRef(name string, namespace *gwapiv1.Namespace) aigv1b1.AIGatewayRouteRuleBackendRef {
	return aigv1b1.AIGatewayRouteRuleBackendRef{Name: name, Namespace: namespace}
}

func inferencePoolRef(name string, namespace *gwapiv1.Namespace) aigv1b1.AIGatewayRouteRuleBackendRef {
	return aigv1b1.AIGatewayRouteRuleBackendRef{
		Name:      name,
		Namespace: namespace,
		Group:     ptr.To(backendpolicy.InferencePoolGroup),
		Kind:      ptr.To(backendpolicy.InferencePoolKind),
	}
}

func TestTargetingPolicies(t *testing.T) {
	c := newClient(t,
		policy("backend-policy", "ns", "apple", backendpolicy.AIServiceBackendKind, ""),
		policy("pool-policy", "ns", "apple", backendpolicy.InferencePoolKind, ""),
		policy("other-backend", "ns", "orange", backendpolicy.AIServiceBackendKind, ""),
		policy("other-namespace", "other-ns", "apple", backendpolicy.AIServiceBackendKind, ""),
	)

	// The kind disambiguates an AIServiceBackend from an InferencePool of the same name.
	got, err := backendpolicy.TargetingPolicies(t.Context(), c, "ns", "apple",
		backendpolicy.AIServiceBackendGroup, backendpolicy.AIServiceBackendKind)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "backend-policy", got[0].Name)

	got, err = backendpolicy.TargetingPolicies(t.Context(), c, "ns", "apple",
		backendpolicy.InferencePoolGroup, backendpolicy.InferencePoolKind)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "pool-policy", got[0].Name)

	// A policy only ever attaches to backends in its own namespace.
	got, err = backendpolicy.TargetingPolicies(t.Context(), c, "third-ns", "apple",
		backendpolicy.AIServiceBackendGroup, backendpolicy.AIServiceBackendKind)
	require.NoError(t, err)
	require.Empty(t, got)

	// Duplicates are returned rather than rejected here; callers decide what that means.
	c = newClient(t,
		policy("first", "ns", "apple", backendpolicy.AIServiceBackendKind, ""),
		policy("second", "ns", "apple", backendpolicy.AIServiceBackendKind, ""),
	)
	got, err = backendpolicy.TargetingPolicies(t.Context(), c, "ns", "apple",
		backendpolicy.AIServiceBackendGroup, backendpolicy.AIServiceBackendKind)
	require.NoError(t, err)
	require.Len(t, got, 2)
}

func TestCredentialOverrideMetadataNamespaces(t *testing.T) {
	c := newClient(t,
		policy("apple-md", "ns", "apple", backendpolicy.AIServiceBackendKind, "envoy.filters.http.ext_authz"),
		policy("banana-md", "ns", "banana", backendpolicy.AIServiceBackendKind, "aaa.custom"),
		// Same namespace as apple's: deduplicated in the result.
		policy("cherry-md", "ns", "cherry", backendpolicy.AIServiceBackendKind, "envoy.filters.http.ext_authz"),
		// No credential override at all.
		policy("apple-plain", "ns", "plain", backendpolicy.AIServiceBackendKind, ""),
		// Attached to a backend nothing below references.
		policy("unrelated", "ns", "unrelated", backendpolicy.AIServiceBackendKind, "zzz.unrelated"),
		policy("pool-md", "pool-ns", "my-pool", backendpolicy.InferencePoolKind, "pool.metadata"),
	)

	for _, tc := range []struct {
		name string
		refs []aigv1b1.AIGatewayRouteRuleBackendRef
		want []string
	}{
		{name: "no refs"},
		{
			name: "sorted, deduplicated union of the refs' policies",
			refs: []aigv1b1.AIGatewayRouteRuleBackendRef{
				backendRef("apple", nil), backendRef("banana", nil), backendRef("cherry", nil),
			},
			want: []string{"aaa.custom", "envoy.filters.http.ext_authz"},
		},
		{
			name: "a backend with no policy contributes nothing",
			refs: []aigv1b1.AIGatewayRouteRuleBackendRef{backendRef("plain", nil), backendRef("missing", nil)},
		},
		{
			name: "the same backend listed twice is resolved once",
			refs: []aigv1b1.AIGatewayRouteRuleBackendRef{backendRef("apple", nil), backendRef("apple", nil)},
			want: []string{"envoy.filters.http.ext_authz"},
		},
		{
			name: "a cross-namespace ref resolves in the namespace it names",
			refs: []aigv1b1.AIGatewayRouteRuleBackendRef{inferencePoolRef("my-pool", ptr.To[gwapiv1.Namespace]("pool-ns"))},
			want: []string{"pool.metadata"},
		},
		{
			name: "an InferencePool ref does not pick up an AIServiceBackend policy of the same name",
			refs: []aigv1b1.AIGatewayRouteRuleBackendRef{inferencePoolRef("apple", nil)},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := backendpolicy.NewResolver(c).CredentialOverrideMetadataNamespaces(t.Context(), "ns", tc.refs)
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}

	// No field index registered: the controllers are handed a plain uncached client by the envtest
	// harness, and an indexed List would be rejected by the API server with "field label not
	// supported".
	unindexed := fake.NewClientBuilder().WithScheme(controller.Scheme).
		WithObjects(policy("apple-md", "ns", "apple", backendpolicy.AIServiceBackendKind, "envoy.filters.http.ext_authz")).
		Build()
	got, err := backendpolicy.NewResolver(unindexed).CredentialOverrideMetadataNamespaces(t.Context(), "ns",
		[]aigv1b1.AIGatewayRouteRuleBackendRef{backendRef("apple", nil)})
	require.NoError(t, err)
	require.Equal(t, []string{"envoy.filters.http.ext_authz"}, got)

	// A policy being deleted is excluded: the finalizer-time resync is the last reconcile the
	// deletion triggers, so it has to compute the post-deletion state or the namespace stays in
	// the annotation and the forwarding list forever. TargetingPolicies drops it from the filter
	// config in the same sweep, so the extproc stops requiring the metadata too.
	deleting := policy("deleting", "ns", "apple", backendpolicy.AIServiceBackendKind, "deleting.namespace")
	deleting.Finalizers = []string{"aigateway.envoyproxy.io/finalizer"}
	c = newClient(t, deleting)
	require.NoError(t, c.Delete(t.Context(), deleting)) // The finalizer keeps it in the store.
	got, err = backendpolicy.NewResolver(c).CredentialOverrideMetadataNamespaces(t.Context(), "ns",
		[]aigv1b1.AIGatewayRouteRuleBackendRef{backendRef("apple", nil)})
	require.NoError(t, err)
	require.Empty(t, got)
	terminating, err := backendpolicy.TargetingPolicies(t.Context(), c, "ns", "apple",
		backendpolicy.AIServiceBackendGroup, backendpolicy.AIServiceBackendKind)
	require.NoError(t, err)
	require.Empty(t, terminating)
}

func TestRouteBackendRefs(t *testing.T) {
	require.Empty(t, backendpolicy.RouteBackendRefs(&aigv1b1.AIGatewayRoute{}))

	refs := backendpolicy.RouteBackendRefs(&aigv1b1.AIGatewayRoute{
		Spec: aigv1b1.AIGatewayRouteSpec{Rules: []aigv1b1.AIGatewayRouteRule{
			{BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{backendRef("apple", nil), backendRef("banana", nil)}},
			{},
			{BackendRefs: []aigv1b1.AIGatewayRouteRuleBackendRef{inferencePoolRef("my-pool", nil)}},
		}},
	})
	require.Len(t, refs, 3)
	require.Equal(t, "apple", refs[0].Name)
	require.Equal(t, "banana", refs[1].Name)
	require.Equal(t, "my-pool", refs[2].Name)
}

func TestCredentialOverrideMetadataNamespacesByRef(t *testing.T) {
	c := newClient(t,
		policy("apple-md", "ns", "apple", backendpolicy.AIServiceBackendKind, "envoy.filters.http.ext_authz"),
		policy("apple-md2", "ns", "apple", backendpolicy.AIServiceBackendKind, "zzz.custom"),
		// Same namespace as apple's first: it must still show up under banana, since the
		// HTTPRoute annotation has to change when a namespace spreads to another backend.
		policy("banana-md", "ns", "banana", backendpolicy.AIServiceBackendKind, "envoy.filters.http.ext_authz"),
		policy("pool-md", "ns", "my-pool", backendpolicy.InferencePoolKind, "pool.metadata"),
	)

	got, err := backendpolicy.NewResolver(c).CredentialOverrideMetadataNamespacesByRef(t.Context(), "ns",
		[]aigv1b1.AIGatewayRouteRuleBackendRef{
			backendRef("apple", nil),
			backendRef("banana", nil),
			backendRef("plain", nil),
			// Same name as the pool policy's target, but the AIServiceBackend kind: no match.
			backendRef("my-pool", nil),
			inferencePoolRef("my-pool", nil),
		})
	require.NoError(t, err)
	require.Equal(t, [][]string{
		{"envoy.filters.http.ext_authz", "zzz.custom"},
		{"envoy.filters.http.ext_authz"},
		nil,
		nil,
		{"pool.metadata"},
	}, got)
}

// A Resolver must List each namespace once, no matter how many clusters resolve through it: the
// extension server runs one lookup per cluster per translation, and re-listing every policy for
// every cluster is the cost the Resolver exists to avoid.
func TestResolverListsEachNamespaceOnce(t *testing.T) {
	lists := 0
	c := fake.NewClientBuilder().WithScheme(controller.Scheme).
		WithObjects(policy("apple-md", "ns", "apple", backendpolicy.AIServiceBackendKind, "envoy.filters.http.ext_authz")).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				lists++
				return cl.List(ctx, list, opts...)
			},
		}).Build()

	r := backendpolicy.NewResolver(c)
	for range 5 {
		got, err := r.CredentialOverrideMetadataNamespaces(t.Context(), "ns",
			[]aigv1b1.AIGatewayRouteRuleBackendRef{backendRef("apple", nil)})
		require.NoError(t, err)
		require.Equal(t, []string{"envoy.filters.http.ext_authz"}, got)
	}
	require.Equal(t, 1, lists)
}

func TestCredentialOverrideStripHeaders(t *testing.T) {
	headerPolicy := func(name, backend, header string, poolKind bool, t2 ...aigv1b1.BackendSecurityPolicyType) *aigv1b1.BackendSecurityPolicy {
		kind := backendpolicy.AIServiceBackendKind
		group := backendpolicy.AIServiceBackendGroup
		if poolKind {
			kind, group = backendpolicy.InferencePoolKind, backendpolicy.InferencePoolGroup
		}
		typ := aigv1b1.BackendSecurityPolicyTypeAPIKey
		if len(t2) > 0 {
			typ = t2[0]
		}
		return &aigv1b1.BackendSecurityPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "ns"},
			Spec: aigv1b1.BackendSecurityPolicySpec{
				Type: typ,
				TargetRefs: []gwapiv1a2.LocalPolicyTargetReference{{
					Group: gwapiv1a2.Group(group), Kind: gwapiv1a2.Kind(kind), Name: gwapiv1.ObjectName(backend),
				}},
				CredentialOverride: &aigv1b1.BackendSecurityPolicyCredentialOverride{
					FromRequestHeaders: &aigv1b1.CredentialOverrideFromRequestHeaders{Header: header},
				},
			},
		}
	}
	c := newClient(t,
		headerPolicy("apple-default", "apple", "", false),               // Default x-aigw-api-key.
		headerPolicy("apple-dup", "apple", "X-AIGW-API-KEY", false),     // Lowercased duplicate.
		headerPolicy("banana-custom", "banana", "x-tenant-cred", false), // Custom name.
		headerPolicy("unreferenced", "unrelated", "x-other-gw", false),  // Not in refs: excluded.
		headerPolicy("reserved", "apple", "x-ai-eg-bad", false),         // Unstrippable: skipped.
		headerPolicy("aws-pool", "my-pool", "", true, aigv1b1.BackendSecurityPolicyTypeAWSCredentials),
		policy("apple-md", "ns", "apple", backendpolicy.AIServiceBackendKind, "envoy.filters.http.ext_authz"), // Metadata source: no headers.
	)

	got, err := backendpolicy.NewResolver(c).CredentialOverrideStripHeaders(t.Context(), "ns",
		[]aigv1b1.AIGatewayRouteRuleBackendRef{
			backendRef("apple", nil), backendRef("banana", nil), inferencePoolRef("my-pool", nil),
		})
	require.NoError(t, err)
	require.Equal(t, []string{
		"x-aigw-api-key",
		"x-aigw-aws-access-key-id",
		"x-aigw-aws-secret-access-key",
		"x-aigw-aws-session-token",
		"x-tenant-cred",
	}, got)
}

func TestOverrideInputHeadersFromSpec(t *testing.T) {
	// No override / metadata source: nothing, no error.
	headers, err := backendpolicy.OverrideInputHeadersFromSpec(&aigv1b1.BackendSecurityPolicySpec{Type: aigv1b1.BackendSecurityPolicyTypeAPIKey})
	require.NoError(t, err)
	require.Nil(t, headers)

	// Defaulted and lowercased.
	headers, err = backendpolicy.OverrideInputHeadersFromSpec(&aigv1b1.BackendSecurityPolicySpec{
		Type: aigv1b1.BackendSecurityPolicyTypeAPIKey,
		CredentialOverride: &aigv1b1.BackendSecurityPolicyCredentialOverride{
			FromRequestHeaders: &aigv1b1.CredentialOverrideFromRequestHeaders{Header: "X-Custom"},
		},
	})
	require.NoError(t, err)
	require.Equal(t, []string{"x-custom"}, headers)

	// AWS derives three names from the prefix.
	headers, err = backendpolicy.OverrideInputHeadersFromSpec(&aigv1b1.BackendSecurityPolicySpec{
		Type: aigv1b1.BackendSecurityPolicyTypeAWSCredentials,
		CredentialOverride: &aigv1b1.BackendSecurityPolicyCredentialOverride{
			FromRequestHeaders: &aigv1b1.CredentialOverrideFromRequestHeaders{},
		},
	})
	require.NoError(t, err)
	require.Len(t, headers, 3)

	// Reserved names are rejected.
	_, err = backendpolicy.OverrideInputHeadersFromSpec(&aigv1b1.BackendSecurityPolicySpec{
		Type: aigv1b1.BackendSecurityPolicyTypeAPIKey,
		CredentialOverride: &aigv1b1.BackendSecurityPolicyCredentialOverride{
			FromRequestHeaders: &aigv1b1.CredentialOverrideFromRequestHeaders{Header: "x-ai-eg-tenant-key"},
		},
	})
	require.ErrorContains(t, err, "reserved name")
}
