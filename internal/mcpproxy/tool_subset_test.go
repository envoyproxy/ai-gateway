// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"net/http"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestToolSubset(t *testing.T) {
	tests := []struct {
		name   string
		setHdr bool
		value  string
		want   map[string]struct{}
	}{
		{name: "header absent -> nil (static fallback)", setHdr: false, want: nil},
		{name: "empty -> nil", setHdr: true, value: "", want: nil},
		{name: "whitespace -> nil", setHdr: true, value: "   ", want: nil},
		{name: "single", setHdr: true, value: "github__list_issues", want: map[string]struct{}{"github__list_issues": {}}},
		{name: "multiple + trim", setHdr: true, value: " github__list_issues , slack__post ", want: map[string]struct{}{"github__list_issues": {}, "slack__post": {}}},
		{name: "skip empty entries", setHdr: true, value: "a__b,,c__d,", want: map[string]struct{}{"a__b": {}, "c__d": {}}},
		{name: "dedup", setHdr: true, value: "a__b,a__b", want: map[string]struct{}{"a__b": {}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.setHdr {
				h.Set(internalapi.MCPToolSubsetHeader, tc.value)
			}
			require.Equal(t, tc.want, toolSubset(h))
		})
	}
}

// TestMergeToolsList_DynamicToolSubset verifies that when the x-ai-eg-mcp-tool-subset
// header is present it filters tools/list (taking precedence over the static per-backend
// toolSelector), and when it is absent the static selector is used.
func TestMergeToolsList_DynamicToolSubset(t *testing.T) {
	newProxy := func(hdr string) *mcpRequestContext {
		h := http.Header{}
		if hdr != "" {
			h.Set(internalapi.MCPToolSubsetHeader, hdr)
		}
		return &mcpRequestContext{
			requestHeaders: h,
			ProxyConfig: &ProxyConfig{
				mcpProxyConfig: &mcpProxyConfig{
					routes: map[filterapi.MCPRouteName]*mcpProxyConfigRoute{
						"r": {
							backends: map[filterapi.MCPBackendName]filterapi.MCPBackend{
								"b1": {Name: "b1"}, "b2": {Name: "b2"},
							},
							// Static config: only "t1" is allowed on b1; b2 has no selector (all allowed).
							toolSelectors: map[filterapi.MCPBackendName]*toolSelector{
								"b1": {include: map[string]struct{}{"t1": {}}},
							},
						},
					},
				},
			},
		}
	}
	// Fresh responses per call: mergeToolsList rewrites tool.Name in place, so the
	// *mcp.Tool pointers must not be shared across subtests.
	newResponses := func() []broadCastResponse[mcp.ListToolsResult] {
		return []broadCastResponse[mcp.ListToolsResult]{
			{backendName: "b1", res: mcp.ListToolsResult{Tools: []*mcp.Tool{{Name: "t1"}, {Name: "t2"}}}},
			{backendName: "b2", res: mcp.ListToolsResult{Tools: []*mcp.Tool{{Name: "t3"}}}},
		}
	}
	names := func(res mcp.ListToolsResult) []string {
		out := make([]string, 0, len(res.Tools))
		for _, tl := range res.Tools {
			out = append(out, tl.Name)
		}
		return out
	}
	s := &session{route: "r"}

	t.Run("no header -> static selector applies", func(t *testing.T) {
		got := newProxy("").mergeToolsList(s, newResponses())
		// b1: only t1 (static include); t2 excluded. b2: t3 (no selector).
		require.ElementsMatch(t, []string{"b1__t1", "b2__t3"}, names(got))
	})
	t.Run("header present -> overrides static (dynamic wins)", func(t *testing.T) {
		// Dynamic allows b1__t2 (NOT b1__t1) + b2__t3 -> the static include(t1) is ignored.
		got := newProxy("b1__t2,b2__t3").mergeToolsList(s, newResponses())
		require.ElementsMatch(t, []string{"b1__t2", "b2__t3"}, names(got))
	})
	t.Run("header present -> only listed tools", func(t *testing.T) {
		got := newProxy("b2__t3").mergeToolsList(s, newResponses())
		require.ElementsMatch(t, []string{"b2__t3"}, names(got))
	})
}
