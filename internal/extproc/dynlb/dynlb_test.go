// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package dynlb

import (
	"log/slog"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func Test_newDynamicLoadBalancer(t *testing.T) {
	addr := internaltesting.RequireNewTestDNSServer(t)
	f := &filterapi.DynamicLoadBalancing{
		Backends: []filterapi.DynamicLoadBalancingBackend{
			{
				IPs:  []string{"1.2.3.4"},
				Port: 8080,
			},
			{
				Hostnames: []string{"foo.io", "example.com"},
				Port:      9999,
			},
			{
				Hostnames: []string{"something.io"},
				Port:      4444,
			},
		},
		Models: []filterapi.DynamicLoadBalancingModel{},
	}

	_dlb, err := newDynamicLoadBalancer(t.Context(), slog.Default(), f, addr)
	require.NoError(t, err)
	dlb, ok := _dlb.(*dynamicLoadBalancer)
	require.True(t, ok)

	for _, m := range f.Models {
		require.Equal(t, m, dlb.models[m.Name])
	}
	require.ElementsMatch(t, []endpoint{
		{
			ipPort:  []byte("1.2.3.4:8080"),
			backend: &f.Backends[0].Backend,
		},
		{
			ipPort:   []byte("1.1.1.1:9999"),
			hostname: "foo.io",
			backend:  &f.Backends[1].Backend,
		},
		{
			ipPort:   []byte("2.2.2.2:9999"),
			hostname: "example.com",
			backend:  &f.Backends[1].Backend,
		},
		{
			ipPort:   []byte("3.3.3.3:4444"),
			hostname: "something.io",
			backend:  &f.Backends[2].Backend,
		},
		{
			ipPort:   []byte("4.4.4.4:4444"),
			hostname: "something.io",
			backend:  &f.Backends[2].Backend,
		},
	}, dlb.endpoints)
}

func TestDynamicLoadBalancingSelectChatCompletionsEndpoint(t *testing.T) {
	// TODO: currently this is mostly for test coverage, need to add more tests as we add more features.
	dlb := &dynamicLoadBalancer{
		logger: slog.Default(),
		endpoints: []endpoint{
			{ipPort: []byte("1.1.1.1:8080"), backend: &filterapi.Backend{Name: "foo"}, hostname: "foo.io"},
		},
		models: map[string]filterapi.DynamicLoadBalancingModel{"foo": {}},
	}
	t.Run("model name not found", func(t *testing.T) {
		_, _, err := dlb.SelectChatCompletionsEndpoint("aaaaaaaaaaaaa", nil)
		require.ErrorContains(t, err, "model aaaaaaaaaaaaa is not found in the dynamic load balancer")
	})
	t.Run("ok", func(t *testing.T) {
		backend, headers, err := dlb.SelectChatCompletionsEndpoint("foo", nil)
		require.NoError(t, err)
		require.Equal(t, &filterapi.Backend{Name: "foo"}, backend)
		require.Len(t, headers, 1)
		for _, h := range []*corev3.HeaderValueOption{
			{
				Header: &corev3.HeaderValue{
					Key:      originalDstHeaderName,
					RawValue: []byte("1.1.1.1:8080"),
				},
			},
		} {
			require.Contains(t, headers, h)
		}
	})
}
