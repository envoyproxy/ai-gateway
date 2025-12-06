// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"log/slog"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

func Test_toolSelector_Allows(t *testing.T) {
	reBa := regexp.MustCompile("^ba.*")
	tests := []struct {
		name     string
		selector toolSelector
		tools    []string
		want     []bool
	}{
		{
			name:     "no rules allows all",
			selector: toolSelector{},
			tools:    []string{"foo", "bar"},
			want:     []bool{true, true},
		},
		{
			name:     "include specific tool",
			selector: toolSelector{include: map[string]struct{}{"foo": {}}},
			tools:    []string{"foo", "bar"},
			want:     []bool{true, false},
		},
		{
			name:     "include regexp",
			selector: toolSelector{includeRegexps: []*regexp.Regexp{reBa}},
			tools:    []string{"bar", "foo"},
			want:     []bool{true, false},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, tool := range tt.tools {
				got := tt.selector.allows(tool)
				require.Equalf(t, tt.want[i], got, "tool: %s", tool)
			}
		})
	}
}

func TestLoadConfig_NilMCPConfig(t *testing.T) {
	proxy, _, err := NewMCPProxy(slog.Default(), stubMetrics{}, noopTracer, NewPBKDF2AesGcmSessionCrypto("test", 100))
	require.NoError(t, err)

	config := &filterapi.Config{MCPConfig: nil}

	err = proxy.LoadConfig(t.Context(), config)
	require.NoError(t, err)
}

func TestLoadConfig_BasicConfiguration(t *testing.T) {
	proxy := &ProxyConfig{
		mcpProxyConfig:     &mcpProxyConfig{},
		toolChangeSignaler: newMultiWatcherSignaler(),
	}

	config := &filterapi.Config{
		MCPConfig: &filterapi.MCPConfig{
			BackendListenerAddr: "http://localhost:8080",
			Routes: []filterapi.MCPRoute{
				{
					Name: "route1",
					Backends: []filterapi.MCPBackend{
						{Name: "backend1", Path: "/mcp1"},
						{
							Name: "backend2", Path: "/mcp2",
							ToolSelector: &filterapi.MCPToolSelector{
								Include:      []string{"tool1", "tool2"},
								IncludeRegex: []string{"^test.*"},
							},
						},
					},
				},
				{
					Name: "route2",
					Backends: []filterapi.MCPBackend{
						{Name: "backend3", Path: "/mcp3"},
						{Name: "backend4", Path: "/mcp4"},
					},
				},
			},
		},
	}

	err := proxy.LoadConfig(t.Context(), config)
	require.NoError(t, err)
	require.Equal(t, "http://localhost:8080", proxy.backendListenerAddr)
	require.Len(t, proxy.routes, 2)
	require.Contains(t, proxy.routes, filterapi.MCPRouteName("route1"))
	require.Contains(t, proxy.routes, filterapi.MCPRouteName("route2"))
	require.Len(t, proxy.routes["route1"].backends, 2)
	require.Len(t, proxy.routes["route2"].backends, 2)
	require.Contains(t, proxy.routes["route1"].backends, filterapi.MCPBackendName("backend1"))
	require.Contains(t, proxy.routes["route1"].backends, filterapi.MCPBackendName("backend2"))
	require.Contains(t, proxy.routes["route2"].backends, filterapi.MCPBackendName("backend3"))
	require.Contains(t, proxy.routes["route2"].backends, filterapi.MCPBackendName("backend4"))
	selector := proxy.routes["route1"].toolSelectors["backend2"]
	require.NotNil(t, selector)
	require.Contains(t, selector.include, "tool1")
	require.Contains(t, selector.include, "tool2")
	require.Len(t, selector.includeRegexps, 1)
	require.True(t, selector.includeRegexps[0].MatchString("test123"))
	require.False(t, selector.includeRegexps[0].MatchString("other"))
}

func TestLoadConfig_ToolsChangedNotification(t *testing.T) {
	toolChangeSignaler := newMultiWatcherSignaler()
	watcher := toolChangeSignaler.Watch()

	// Initialize proxy with initial configuration directly
	proxy := &ProxyConfig{
		mcpProxyConfig: &mcpProxyConfig{
			backendListenerAddr: "http://localhost:8080",
			routes: map[filterapi.MCPRouteName]*mcpProxyConfigRoute{
				"route1": {
					backends: map[filterapi.MCPBackendName]filterapi.MCPBackend{
						"backend1": {Name: "backend1", Path: "/mcp1"},
					},
					toolSelectors: map[filterapi.MCPBackendName]*toolSelector{},
				},
			},
		},
		toolChangeSignaler: toolChangeSignaler,
	}

	// Update with a different backend (tools changed)
	config := &filterapi.Config{
		MCPConfig: &filterapi.MCPConfig{
			BackendListenerAddr: "http://localhost:8080",
			Routes: []filterapi.MCPRoute{
				{
					Name: "route1",
					Backends: []filterapi.MCPBackend{
						{Name: "backend1", Path: "/mcp1"},
						{Name: "backend2", Path: "/mcp2"}, // Added backend
					},
				},
			},
		},
	}

	err := proxy.LoadConfig(t.Context(), config)
	require.NoError(t, err)

	// Should receive tools changed notification
	select {
	case <-watcher:
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected tools changed notification but didn't receive one")
	}
}

func TestLoadConfig_NoToolsChangedNotification(t *testing.T) {
	toolChangeSignaler := newMultiWatcherSignaler()
	watcher := toolChangeSignaler.Watch()

	// Initialize proxy with initial configuration directly
	proxy := &ProxyConfig{
		mcpProxyConfig: &mcpProxyConfig{
			backendListenerAddr: "http://localhost:8080",
			routes: map[filterapi.MCPRouteName]*mcpProxyConfigRoute{
				"route1": {
					backends: map[filterapi.MCPBackendName]filterapi.MCPBackend{
						"backend1": {Name: "backend1", Path: "/mcp1"},
					},
					toolSelectors: map[filterapi.MCPBackendName]*toolSelector{},
				},
			},
		},
		toolChangeSignaler: toolChangeSignaler,
	}

	// Update with same backends but different BackendListenerAddr (tools NOT changed)
	config := &filterapi.Config{
		MCPConfig: &filterapi.MCPConfig{
			BackendListenerAddr: "http://localhost:9090", // Different address
			Routes: []filterapi.MCPRoute{
				{
					Name: "route1",
					Backends: []filterapi.MCPBackend{
						{Name: "backend1", Path: "/mcp1"}, // Same backend
					},
				},
			},
		},
	}

	err := proxy.LoadConfig(t.Context(), config)
	require.NoError(t, err)

	// Should NOT receive tools changed notification
	select {
	case <-watcher:
		t.Fatal("unexpected tools changed notification")
	case <-time.After(100 * time.Millisecond):
		// Expected - no notification
	}
}

func TestLoadConfig_InvalidRegex(t *testing.T) {
	proxy := &ProxyConfig{
		mcpProxyConfig:     &mcpProxyConfig{},
		toolChangeSignaler: newMultiWatcherSignaler(),
	}

	config := &filterapi.Config{
		MCPConfig: &filterapi.MCPConfig{
			BackendListenerAddr: "http://localhost:8080",
			Routes: []filterapi.MCPRoute{
				{
					Name: "route1",
					Backends: []filterapi.MCPBackend{
						{
							Name: "backend1",
							Path: "/mcp1",
							ToolSelector: &filterapi.MCPToolSelector{
								IncludeRegex: []string{"[invalid"}, // Invalid regex
							},
						},
					},
				},
			},
		},
	}

	err := proxy.LoadConfig(t.Context(), config)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to compile include regex")
}

func TestLoadConfig_ToolSelectorChange(t *testing.T) {
	toolChangeSignaler := newMultiWatcherSignaler()
	watcher := toolChangeSignaler.Watch()

	// Initialize proxy with initial configuration directly
	proxy := &ProxyConfig{
		mcpProxyConfig: &mcpProxyConfig{
			backendListenerAddr: "http://localhost:8080",
			routes: map[filterapi.MCPRouteName]*mcpProxyConfigRoute{
				"route1": {
					backends: map[filterapi.MCPBackendName]filterapi.MCPBackend{
						"backend1": {Name: "backend1", Path: "/mcp1"},
					},
					toolSelectors: map[filterapi.MCPBackendName]*toolSelector{
						"backend1": {
							include: map[string]struct{}{"tool1": {}},
						},
					},
				},
			},
		},
		toolChangeSignaler: toolChangeSignaler,
	}

	// Update with different tool selector (tools changed)
	config := &filterapi.Config{
		MCPConfig: &filterapi.MCPConfig{
			BackendListenerAddr: "http://localhost:8080",
			Routes: []filterapi.MCPRoute{
				{
					Name: "route1",
					Backends: []filterapi.MCPBackend{
						{
							Name: "backend1",
							Path: "/mcp1",
							ToolSelector: &filterapi.MCPToolSelector{
								Include: []string{"tool1", "tool2"}, // Different tools
							},
						},
					},
				},
			},
		},
	}

	// Start watcher goroutines to make sure all of them are notified
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Go(func() {
			select {
			case <-watcher: // Expected
			case <-time.After(100 * time.Millisecond):
				t.Fatal("expected tools changed notification but didn't receive one")
			}
		})
	}

	err := proxy.LoadConfig(t.Context(), config)
	require.NoError(t, err)

	wg.Wait()
}

func TestLoadConfig_ToolOrderDoesNotMatter(t *testing.T) {
	toolChangeSignaler := newMultiWatcherSignaler()
	watcher := toolChangeSignaler.Watch()

	// Initialize proxy with initial configuration directly
	proxy := &ProxyConfig{
		mcpProxyConfig: &mcpProxyConfig{
			backendListenerAddr: "http://localhost:8080",
			routes: map[filterapi.MCPRouteName]*mcpProxyConfigRoute{
				"route1": {
					backends: map[filterapi.MCPBackendName]filterapi.MCPBackend{
						"backend1": {Name: "backend1", Path: "/mcp1"},
					},
					toolSelectors: map[filterapi.MCPBackendName]*toolSelector{
						"backend1": {
							include: map[string]struct{}{
								"tool-a": {},
								"tool-b": {},
								"tool-c": {},
							},
							includeRegexps: []*regexp.Regexp{
								regexp.MustCompile("^prefix.*"),
								regexp.MustCompile(".*suffix$"),
								regexp.MustCompile("^exact$"),
							},
						},
					},
				},
			},
		},
		toolChangeSignaler: toolChangeSignaler,
	}

	// Update with same tools and regexps but in different order
	config := &filterapi.Config{
		MCPConfig: &filterapi.MCPConfig{
			BackendListenerAddr: "http://localhost:8080",
			Routes: []filterapi.MCPRoute{
				{
					Name: "route1",
					Backends: []filterapi.MCPBackend{
						{
							Name: "backend1",
							Path: "/mcp1",
							ToolSelector: &filterapi.MCPToolSelector{
								Include:      []string{"tool-c", "tool-a", "tool-b"},        // Different order
								IncludeRegex: []string{"^exact$", ".*suffix$", "^prefix.*"}, // Different order
							},
						},
					},
				},
			},
		},
	}

	err := proxy.LoadConfig(t.Context(), config)
	require.NoError(t, err)

	// Should NOT receive tools changed notification since same tools, just different order
	select {
	case <-watcher:
		t.Fatal("unexpected tools changed notification when only order changed")
	case <-time.After(100 * time.Millisecond):
		// Expected - no notification
	}

	// Verify the tool selector still works correctly regardless of order
	route := proxy.routes["route1"]
	require.NotNil(t, route)
	selector := route.toolSelectors["backend1"]
	require.NotNil(t, selector)
	require.Contains(t, selector.include, "tool-a")
	require.Contains(t, selector.include, "tool-b")
	require.Contains(t, selector.include, "tool-c")
	require.Len(t, selector.includeRegexps, 3)
}
