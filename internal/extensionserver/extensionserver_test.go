// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"bytes"
	"log/slog"
	"testing"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	endpointv3 "github.com/envoyproxy/go-control-plane/envoy/config/endpoint/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
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

const udsPath = "/tmp/uds/test.sock"

func TestNew(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath)
	require.NotNil(t, s)
}

func TestCheck(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath)
	_, err := s.Check(t.Context(), nil)
	require.NoError(t, err)
}

func TestWatch(t *testing.T) {
	logger := logr.Discard()
	s := New(newFakeClient(), logger, udsPath)
	err := s.Watch(nil, nil)
	require.Error(t, err)
	require.Equal(t, "rpc error: code = Unimplemented desc = Watch is not implemented", err.Error())
}

func TestServerPostTranslateModify(t *testing.T) {
	t.Run("existing", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		req := &egextension.PostTranslateModifyRequest{Clusters: []*clusterv3.Cluster{{Name: ExtProcUDSClusterName}}}
		res, err := s.PostTranslateModify(t.Context(), req)
		require.Equal(t, &egextension.PostTranslateModifyResponse{
			Clusters: req.Clusters, Secrets: req.Secrets,
		}, res)
		require.NoError(t, err)
	})
	t.Run("not existing", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		res, err := s.PostTranslateModify(t.Context(), &egextension.PostTranslateModifyRequest{
			Clusters: []*clusterv3.Cluster{{Name: "foo"}},
		})
		require.NotNil(t, res)
		require.NoError(t, err)
		require.Len(t, res.Clusters, 2)
		require.Equal(t, "foo", res.Clusters[0].Name)
		require.Equal(t, ExtProcUDSClusterName, res.Clusters[1].Name)
	})
}

func TestServerPostVirtualHostModify(t *testing.T) {
	t.Run("nil virtual host", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		res, err := s.PostVirtualHostModify(t.Context(), &egextension.PostVirtualHostModifyRequest{})
		require.Nil(t, res)
		require.NoError(t, err)
	})
	t.Run("zero routes", func(t *testing.T) {
		s := New(newFakeClient(), logr.Discard(), udsPath)
		res, err := s.PostVirtualHostModify(t.Context(), &egextension.PostVirtualHostModifyRequest{
			VirtualHost: &routev3.VirtualHost{},
		})
		require.Nil(t, res)
		require.NoError(t, err)
	})
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
						{Name: "aaa", Priority: ptr.To[uint32](0)},
						{Name: "bbb", Priority: ptr.To[uint32](1)},
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
			s := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath)
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
					{
						LbEndpoints: []*endpointv3.LbEndpoint{
							{},
						},
					},
				},
			},
		}
		var buf bytes.Buffer
		s := New(c, logr.FromSlogHandler(slog.NewTextHandler(&buf, &slog.HandlerOptions{})), udsPath)
		s.maybeModifyCluster(cluster)
		require.Empty(t, buf.String())

		require.Len(t, cluster.LoadAssignment.Endpoints, 2)
		require.Len(t, cluster.LoadAssignment.Endpoints[0].LbEndpoints, 1)
		require.Equal(t, uint32(0), cluster.LoadAssignment.Endpoints[0].Priority)
		require.Equal(t, uint32(1), cluster.LoadAssignment.Endpoints[1].Priority)
		md := cluster.LoadAssignment.Endpoints[0].LbEndpoints[0].Metadata
		require.NotNil(t, md)
		require.Len(t, md.FilterMetadata, 1)
		mmd, ok := md.FilterMetadata["aigateway.envoy.io"]
		require.True(t, ok)
		require.Len(t, mmd.Fields, 1)
		require.Equal(t, "aaa.ns", mmd.Fields["backend_name"].GetStringValue())
	})
}
