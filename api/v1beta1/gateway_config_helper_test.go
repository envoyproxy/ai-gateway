// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package v1beta1

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGatewayConfig_RateLimitSourceNamespaces(t *testing.T) {
	newConfig := func(namespaces ...string) *GatewayConfig {
		gc := &GatewayConfig{}
		for _, ns := range namespaces {
			gc.Spec.GlobalRateLimits = append(gc.Spec.GlobalRateLimits, RateLimitOverride{
				MetadataKey: "key",
				Source: RateLimitOverrideSource{
					FromMetadata: RateLimitMetadataSource{Namespace: ns, Key: "k"},
				},
			})
		}
		return gc
	}

	tests := []struct {
		name     string
		config   *GatewayConfig
		expected []string
	}{
		{name: "no rate limits returns nil", config: &GatewayConfig{}, expected: nil},
		{name: "only empty namespaces returns nil", config: newConfig("", ""), expected: nil},
		{name: "single namespace", config: newConfig("ns-a"), expected: []string{"ns-a"}},
		{name: "duplicates are de-duplicated", config: newConfig("ns-a", "ns-a"), expected: []string{"ns-a"}},
		{name: "result is sorted and skips empties", config: newConfig("ns-b", "", "ns-a"), expected: []string{"ns-a", "ns-b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.config.RateLimitSourceNamespaces())
		})
	}
}
