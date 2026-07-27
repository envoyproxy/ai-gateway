// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"testing"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fake2 "k8s.io/client-go/kubernetes/fake"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

// TestAIBackendController_backendToAIServiceBackends covers the egv1a1.Backend ->
// AIServiceBackend requeue mapping, so a Backend FQDN edit regenerates the extproc
// config (the signing host).
func TestAIBackendController_backendToAIServiceBackends(t *testing.T) {
	fakeClient := requireNewFakeClientWithIndexes(t)
	eventChan := internaltesting.NewControllerEventChan[*aigv1b1.AIGatewayRoute]()
	c := NewAIServiceBackendController(fakeClient, fake2.NewClientset(), ctrl.Log, eventChan.Ch)

	beRef := func(name string) gwapiv1.BackendObjectReference {
		return gwapiv1.BackendObjectReference{
			Group: ptr.To(gwapiv1.Group("gateway.envoyproxy.io")),
			Kind:  ptr.To(gwapiv1.Kind("Backend")),
			Name:  gwapiv1.ObjectName(name),
		}
	}
	mkASB := func(name, ns string, ref gwapiv1.BackendObjectReference) *aigv1b1.AIServiceBackend {
		return &aigv1b1.AIServiceBackend{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       aigv1b1.AIServiceBackendSpec{BackendRef: ref},
		}
	}
	require.NoError(t, fakeClient.Create(t.Context(), mkASB("asb1", "a", beRef("bedrock"))))
	require.NoError(t, fakeClient.Create(t.Context(), mkASB("asb2", "a", beRef("bedrock"))))
	require.NoError(t, fakeClient.Create(t.Context(), mkASB("other", "a", beRef("openai"))))      // different Backend
	require.NoError(t, fakeClient.Create(t.Context(), mkASB("elsewhere", "b", beRef("bedrock")))) // different ns

	reqs := c.backendToAIServiceBackends(t.Context(),
		&egv1a1.Backend{ObjectMeta: metav1.ObjectMeta{Name: "bedrock", Namespace: "a"}})
	names := map[string]bool{}
	for _, r := range reqs {
		require.Equal(t, "a", r.Namespace)
		names[r.Name] = true
	}
	require.Len(t, reqs, 2, "only the two ns=a AIServiceBackends referencing Backend bedrock")
	require.True(t, names["asb1"] && names["asb2"])
	require.False(t, names["other"] || names["elsewhere"])

	// A Backend nobody references -> no requeue.
	require.Empty(t, c.backendToAIServiceBackends(t.Context(),
		&egv1a1.Backend{ObjectMeta: metav1.ObjectMeta{Name: "ghost", Namespace: "a"}}))
	// Non-Backend object -> nil.
	require.Nil(t, c.backendToAIServiceBackends(t.Context(), &aigv1b1.AIServiceBackend{}))
}
