// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"testing"

	egextension "github.com/envoyproxy/gateway/proto/extension"
	clusterv3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	routev3 "github.com/envoyproxy/go-control-plane/envoy/config/route/v3"
	matcherv3 "github.com/envoyproxy/go-control-plane/envoy/type/matcher/v3"
	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	logger := logr.Discard()
	s := New(logger)
	require.NotNil(t, s)
}

func TestCheck(t *testing.T) {
	logger := logr.Discard()
	s := New(logger)
	_, err := s.Check(t.Context(), nil)
	require.NoError(t, err)
}

func TestWatch(t *testing.T) {
	logger := logr.Discard()
	s := New(logger)
	err := s.Watch(nil, nil)
	require.Error(t, err)
	require.Equal(t, "rpc error: code = Unimplemented desc = Watch is not implemented", err.Error())
}

func TestServerPostTranslateModify(t *testing.T) {
	t.Run("existing", func(t *testing.T) {
		s := New(logr.Discard())
		res, err := s.PostTranslateModify(t.Context(), &egextension.PostTranslateModifyRequest{
			Clusters: []*clusterv3.Cluster{
				{Name: OriginalDstClusterName},
			},
		})
		require.Nil(t, res)
		require.NoError(t, err)
	})
	t.Run("not existing", func(t *testing.T) {
		s := New(logr.Discard())
		res, err := s.PostTranslateModify(t.Context(), &egextension.PostTranslateModifyRequest{
			Clusters: []*clusterv3.Cluster{
				{Name: "foo"},
			},
		})
		require.NotNil(t, res)
		require.NoError(t, err)
		require.Len(t, res.Clusters, 2)
		require.Equal(t, "foo", res.Clusters[0].Name)
		require.Equal(t, OriginalDstClusterName, res.Clusters[1].Name)
	})
}

func TestServerPostVirtualHostModify(t *testing.T) {
	t.Run("nil virtual host", func(t *testing.T) {
		s := New(logr.Discard())
		res, err := s.PostVirtualHostModify(t.Context(), &egextension.PostVirtualHostModifyRequest{})
		require.Nil(t, res)
		require.NoError(t, err)
	})
	t.Run("zero routes", func(t *testing.T) {
		s := New(logr.Discard())
		res, err := s.PostVirtualHostModify(t.Context(), &egextension.PostVirtualHostModifyRequest{
			VirtualHost: &routev3.VirtualHost{},
		})
		require.Nil(t, res)
		require.NoError(t, err)
	})
	t.Run("route", func(t *testing.T) {
		s := New(logr.Discard())
		res, err := s.PostVirtualHostModify(t.Context(), &egextension.PostVirtualHostModifyRequest{
			VirtualHost: &routev3.VirtualHost{
				Routes: []*routev3.Route{
					{Name: "foo", Match: &routev3.RouteMatch{
						Headers: []*routev3.HeaderMatcher{
							{
								Name: "x-ai-eg-selected-backend",
								HeaderMatchSpecifier: &routev3.HeaderMatcher_StringMatch{
									StringMatch: &matcherv3.StringMatcher{
										MatchPattern: &matcherv3.StringMatcher_Exact{
											Exact: OriginalDstClusterName,
										},
									},
								},
							},
						},
					}},
				},
			},
		})
		require.NotNil(t, res)
		require.NoError(t, err)
		// Original route should be first per the code comment.
		require.Equal(t, "foo", res.VirtualHost.Routes[0].Name)
		// Ensure that the action has been updated.
		require.Equal(t, OriginalDstClusterName, res.VirtualHost.Routes[0].Action.(*routev3.Route_Route).
			Route.ClusterSpecifier.(*routev3.RouteAction_Cluster).Cluster)
	})
}
