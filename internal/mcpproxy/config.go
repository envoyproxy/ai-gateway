// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"context"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

type (
	// ProxyConfig holds the main MCP proxy configuration.
	ProxyConfig struct {
		*mcpProxyConfig
		toolChangeSignaler changeSignaler // signals tool changes to active sessions.
	}

	mcpProxyConfig struct {
		backendListenerAddr string
		routes              map[filterapi.MCPRouteName]*mcpProxyConfigRoute // route name -> backends of that route.
	}

	mcpProxyConfigRoute struct {
		backends      map[filterapi.MCPBackendName]filterapi.MCPBackend
		toolSelectors map[filterapi.MCPBackendName]*toolSelector
	}

	// toolSelector filters tools using include patterns with exact matches or regular expressions.
	toolSelector struct {
		include        map[string]struct{}
		includeRegexps []*regexp.Regexp
	}

	// changeSignaler is an interface for signaling configuration changes to multiple
	// watchers.
	changeSignaler interface {
		// Watch returns a channel that is closed then the configuration changes.
		// The channel should be obtained by calling this method every time when used in a loop,
		// because it will be closed and recreated after each signal is sent.
		Watch() <-chan struct{}
		// Signal all watchers that the configuration has changed.
		Signal()
	}

	multiWatcherSignaler struct {
		mu     sync.Mutex
		notify chan struct{}
	}
)

func (m *mcpProxyConfig) sameTools(other *mcpProxyConfig) bool {
	if m == nil || other == nil {
		return m == other
	}
	return maps.EqualFunc(m.routes, other.routes, func(a, b *mcpProxyConfigRoute) bool {
		return a.sameTools(b)
	})
}

func (m *mcpProxyConfigRoute) sameTools(other *mcpProxyConfigRoute) bool {
	if m == nil || other == nil {
		return m == other
	}
	if !equalKeys(m.backends, other.backends) {
		return false
	}
	return maps.EqualFunc(m.toolSelectors, other.toolSelectors, func(a, b *toolSelector) bool {
		return a.sameTools(b)
	})
}

var sortRegexpAsString = func(a, b *regexp.Regexp) int { return strings.Compare(a.String(), b.String()) }

func equalKeys[K comparable, V any](m1, m2 map[K]V) bool {
	return maps.EqualFunc(m1, m2, func(_, _ V) bool { return true })
}

func (t *toolSelector) sameTools(other *toolSelector) bool {
	if t == nil || other == nil {
		return t == other
	}
	if !equalKeys(t.include, other.include) {
		return false
	}
	slices.SortFunc(t.includeRegexps, sortRegexpAsString)
	slices.SortFunc(other.includeRegexps, sortRegexpAsString)
	return slices.EqualFunc(t.includeRegexps, other.includeRegexps,
		func(a, b *regexp.Regexp) bool {
			return a.String() == b.String()
		})
}

func (t *toolSelector) allows(tool string) bool {
	// Check include filters - if no filter, allow all; if filter exists, allow only matches
	if len(t.include) > 0 {
		_, ok := t.include[tool]
		return ok
	}
	if len(t.includeRegexps) > 0 {
		for _, re := range t.includeRegexps {
			if re.MatchString(tool) {
				return true
			}
		}
		return false
	}
	// No filters, allow all
	return true
}

// LoadConfig implements [extproc.ConfigReceiver.LoadConfig] which will be called
// when the configuration is updated on the file system.
func (p *ProxyConfig) LoadConfig(_ context.Context, config *filterapi.Config) error {
	newConfig := &mcpProxyConfig{}
	mcpConfig := config.MCPConfig
	if config.MCPConfig == nil {
		return nil
	}

	// Talk to the backend MCP listener on the local Envoy instance.
	newConfig.backendListenerAddr = mcpConfig.BackendListenerAddr

	// Build a map of routes to backends.
	// Each route has its own set of backends. For a given downstream request,
	// the MCP proxy initializes sessions only with the backends tied to that route.
	newConfig.routes = make(map[filterapi.MCPRouteName]*mcpProxyConfigRoute, len(mcpConfig.Routes))

	for _, route := range mcpConfig.Routes {
		r := &mcpProxyConfigRoute{
			backends:      make(map[filterapi.MCPBackendName]filterapi.MCPBackend, len(route.Backends)),
			toolSelectors: make(map[filterapi.MCPBackendName]*toolSelector, len(route.Backends)),
		}
		for _, backend := range route.Backends {
			r.backends[backend.Name] = backend
			if s := backend.ToolSelector; s != nil {
				ts := &toolSelector{
					include: make(map[string]struct{}),
				}
				for _, tool := range s.Include {
					ts.include[tool] = struct{}{}
				}
				for _, expr := range s.IncludeRegex {
					re, err := regexp.Compile(expr)
					if err != nil {
						return fmt.Errorf("failed to compile include regex %q for backend %q in route %q: %w", expr, backend.Name, route.Name, err)
					}
					ts.includeRegexps = append(ts.includeRegexps, re)
				}
				r.toolSelectors[backend.Name] = ts
			}
		}
		newConfig.routes[route.Name] = r
	}

	toolsChanged := !p.sameTools(newConfig)
	p.mcpProxyConfig = newConfig // This is racy, but we don't care.
	if toolsChanged {
		p.toolChangeSignaler.Signal()
	}

	return nil
}

// newMultiWatcherSignaler creates a new multi-watcher signaler.
func newMultiWatcherSignaler() *multiWatcherSignaler {
	return &multiWatcherSignaler{
		notify: make(chan struct{}),
	}
}

// Watch returns a channel that is closed then the configuration changes.
// The channel should be obtained by calling this method every time when used in a loop,
// because it will be closed and recreated after each signal is sent.
func (m *multiWatcherSignaler) Watch() <-chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.notify
}

// Signal notifies all watchers of a configuration change.
func (m *multiWatcherSignaler) Signal() {
	m.mu.Lock()
	defer m.mu.Unlock()
	close(m.notify)                // Wake everyone
	m.notify = make(chan struct{}) // Create a new channel for future updates
}
