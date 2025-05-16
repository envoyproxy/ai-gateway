// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"bytes"
	"log/slog"
	"testing"

	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/controller"
)

func newFakeClient() client.Client {
	builder := fake.NewClientBuilder().WithScheme(controller.Scheme).
		WithStatusSubresource(&aigv1a1.AIGatewayRoute{}).
		WithStatusSubresource(&aigv1a1.AIServiceBackend{}).
		WithStatusSubresource(&aigv1a1.BackendSecurityPolicy{})
	return builder.Build()
}

func TestNew(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger)
	require.NotNil(t, s)
}

func TestCheck(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger)
	_, err := s.Check(t.Context(), nil)
	require.NoError(t, err)
}

func TestWatch(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger)
	err := s.Watch(nil, nil)
	require.Error(t, err)
	require.Equal(t, "rpc error: code = Unimplemented desc = Watch is not implemented", err.Error())
}

func Test_maybeModifyCluster(t *testing.T) {
	c := newFakeClient()

	// Create some fake AIGatewayRoute objects.
	err := c.Create(t.Context(), &aigv1a1.AIGatewayRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "myroute",
			Namespace: "ns",
		},
		Spec: aigv1a1.AIGatewayRouteSpec{
			Rules: []aigv1a1.AIGatewayRouteRule{
				{
					BackendRefs: []aigv1a1.AIGatewayRouteRuleBackendRef{
						{Name: "aaa"},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	for _, tc := range []struct {
		c      *clusterv3.Cluster
		errLog string
	}{
		{c: &clusterv3.Cluster{}, errLog: "non-ai-gateway cluster name"},
		{c: &clusterv3.Cluster{
			Name: "httproute/ns/name/rule/invalid",
		}, errLog: "failed to parse HTTPRoute rule index"},
		{c: &clusterv3.Cluster{
			Name: "httproute/ns/nonexistent/rule/0",
		}, errLog: `failed to get AIGatewayRoute object`},
		{c: &clusterv3.Cluster{
			Name: "httproute/ns/myroute/rule/99999",
		}, errLog: `HTTPRoute rule index out of range`},
		{c: &clusterv3.Cluster{
			Name: "httproute/ns/myroute/rule/0",
		}, errLog: `LoadAssignment is nil`},
		{c: &clusterv3.Cluster{
			Name:           "httproute/ns/myroute/rule/0",
			LoadAssignment: &endpointv3.ClusterLoadAssignment{},
		}, errLog: `LoadAssignment endpoints length does not match backend refs length`},
	} {
		t.Run("error/"+tc.errLog, func(t *testing.T) {
			var buf bytes.Buffer
			s := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})))
			s.maybeModifyCluster(tc.c)
			t.Logf("buf: %s", buf.String())
			require.Contains(t, buf.String(), tc.errLog)
		})
	}
	t.Run("ok", func(t *testing.T) {
		cluster := &clusterv3.Cluster{
			Name: "httproute/ns/myroute/rule/0",
			LoadAssignment: &endpointv3.ClusterLoadAssignment{
				Endpoints: []*endpointv3.LocalityLbEndpoints{
					{
						LbEndpoints: []*endpointv3.LbEndpoint{
							{},
						},
					},
				},
			},
		}
		var buf bytes.Buffer
		s := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})))
		s.maybeModifyCluster(cluster)
		require.Empty(t, buf.String())

		require.Len(t, cluster.LoadAssignment.Endpoints, 1)
		require.Len(t, cluster.LoadAssignment.Endpoints[0].LbEndpoints, 1)
		md := cluster.LoadAssignment.Endpoints[0].LbEndpoints[0].Metadata
		require.NotNil(t, md)
		require.Len(t, md.FilterMetadata, 1)
		mmd, ok := md.FilterMetadata["aigateawy.envoy.io"]
		require.True(t, ok)
		require.Len(t, mmd.Fields, 1)
		require.Equal(t, "aaa.ns", mmd.Fields["backend_name"].GetStringValue())
	})
}
