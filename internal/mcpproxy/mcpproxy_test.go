// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"context"
	"encoding/base64"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

var (
	_ tracing.MCPSpan   = (*fakeSpan)(nil)
	_ tracing.MCPTracer = (*fakeTracer)(nil)
)

type fakeSpan struct {
	backends []string
	errType  string
	err      error
}

func (f *fakeSpan) RecordRouteToBackend(backend string, _ string, _ bool) {
	f.backends = append(f.backends, backend)
}

func (f *fakeSpan) EndSpan() {}

func (f *fakeSpan) EndSpanOnError(errType string, err error) {
	f.errType = errType
	f.err = err
}

type fakeTracer struct {
	span *fakeSpan
}

func (f *fakeTracer) StartSpanAndInjectMeta(context.Context, *jsonrpc.Request, mcp.Params, http.Header) tracing.MCPSpan {
	if f.span == nil {
		f.span = &fakeSpan{}
	}
	return f.span
}

var noopTracer = tracing.NoopMCPTracer{}

func TestNewMCPProxy(t *testing.T) {
	l := slog.Default()
	proxy, mux, err := NewMCPProxy(l, stubMetrics{}, noopTracer, NewPBKDF2AesGcmSessionCrypto("test", 100))

	require.NoError(t, err)
	require.NotNil(t, proxy)
	require.NotNil(t, mux)
}

func TestMCPProxy_HTTPMethods(t *testing.T) {
	l := slog.Default()
	_, mux, err := NewMCPProxy(l, stubMetrics{}, noopTracer, NewPBKDF2AesGcmSessionCrypto("test", 100))
	require.NoError(t, err)

	// Test unsupported method.
	req := httptest.NewRequest(http.MethodPatch, "/mcp", nil)
	rr := httptest.NewRecorder()

	mux.ServeHTTP(rr, req)

	require.Equal(t, http.StatusMethodNotAllowed, rr.Code)
	require.Contains(t, rr.Body.String(), "method not allowed")
}

func TestLoadConfig_NilMCPConfig(t *testing.T) {
	proxy, _, err := NewMCPProxy(slog.Default(), stubMetrics{}, noopTracer, NewPBKDF2AesGcmSessionCrypto("test", 100))
	require.NoError(t, err)

	config := &filterapi.Config{MCPConfig: nil}

	err = proxy.LoadConfig(t.Context(), config)
	require.NoError(t, err)
}

const (
	validInitializeResponse = `{
"jsonrpc": "2.0",
"id": 1,
"result": {
"protocolVersion": "2025-06-18",
"capabilities": {
"logging": {},
"prompts": {
"listChanged": true
},
"resources": {
"subscribe": true,
"listChanged": true
},
"tools": {
"listChanged": true
}
},
"serverInfo": {
"name": "ExampleServer",
"title": "Example Server Display Name",
"version": "1.0.0"
},
"instructions": "Optional instructions for the client"
}
}`
)

type perBackendCallCount struct {
	mu    sync.Mutex
	count map[string]int
}

func (p *perBackendCallCount) inc(key string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.count == nil {
		p.count = make(map[string]int)
	}
	p.count[key]++
	return p.count[key]
}

func (p *perBackendCallCount) get(key string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count[key]
}

func TestNewSession_Success(t *testing.T) {
	// Mock backend server that responds to initialization.
	var callCount perBackendCallCount
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend := r.Header.Get(internalapi.MCPBackendHeader)
		if callCount.inc(backend)%2 == 1 {
			// Initialize requests.
			w.Header().Set(sessionIDHeader, "test-session-123")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(validInitializeResponse))
		} else {
			// notifications/initialized requests.
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer backendServer.Close()

	proxy := newTestMCPProxy()
	proxy.backendListenerAddr = backendServer.URL

	s, err := proxy.newSession(t.Context(), &mcp.InitializeParams{}, "test-route", "", nil)

	require.NoError(t, err)
	require.NotNil(t, s)
	require.NotEmpty(t, s.clientGatewaySessionID())
}

func TestNewSession_NoBackend(t *testing.T) {
	proxy := newTestMCPProxy()

	s, err := proxy.newSession(t.Context(), &mcp.InitializeParams{}, "test-route", "", nil)
	require.ErrorContains(t, err, `failed to create MCP session to any backend`)
	require.Nil(t, s)
}

func TestNewSession_SSE(t *testing.T) {
	// Mock backend server that responds to initialization.
	var callCount perBackendCallCount
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend := r.Header.Get(internalapi.MCPBackendHeader)
		if callCount.inc(backend)%2 == 1 {
			// Odd calls: initialize requests.
			w.Header().Set(sessionIDHeader, "test-session-123")
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`event: message
id: Z4WAGVSUUFAJCOUNHNPZWRHCEU_0
data: {"jsonrpc":"2.0","id":"ff3964c5-4c79-4567-96e2-29e905754e58","result":{"capabilities":{"logging":{},"tools":{"listChanged":true}},"protocolVersion":"2025-06-18","serverInfo":{"name":"dumb-echo-server","version":"0.1.0"}}}

`))
		} else {
			// Even calls: notifications/initialized requests.
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer backendServer.Close()

	proxy := newTestMCPProxy()
	proxy.backendListenerAddr = backendServer.URL

	s, err := proxy.newSession(t.Context(), &mcp.InitializeParams{}, "test-route", "", nil)

	require.NoError(t, err)
	require.NotNil(t, s)
	require.NotEmpty(t, s.clientGatewaySessionID())
}

func TestSessionFromID_ValidID(t *testing.T) {
	proxy := newTestMCPProxy()

	// Create a valid session ID.
	sessionID := secureID(t, proxy, "@@backend1:"+base64.StdEncoding.EncodeToString([]byte("test-session")))
	eventID := secureID(t, proxy, "@@backend1:"+base64.StdEncoding.EncodeToString([]byte("_1")))
	session, err := proxy.sessionFromID(secureClientToGatewaySessionID(sessionID), secureClientToGatewayEventID(eventID))

	require.NoError(t, err)
	require.NotNil(t, session)
	require.Equal(t, secureClientToGatewaySessionID(sessionID), session.clientGatewaySessionID())
}

func TestSessionFromID_InvalidID(t *testing.T) {
	proxy := newTestMCPProxy()

	// Create an invalid session ID.
	sessionID := secureID(t, proxy, "invalid-session-id")
	s, err := proxy.sessionFromID(secureClientToGatewaySessionID(sessionID), "")

	require.Error(t, err)
	require.Nil(t, s)
}

func TestInitializeSession_Success(t *testing.T) {
	// Mock backend server.
	var callCount perBackendCallCount
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend := r.Header.Get(internalapi.MCPBackendHeader)
		if callCount.inc(backend) == 1 {
			// First call: initialize.
			w.Header().Set(sessionIDHeader, "test-session-123")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(validInitializeResponse))
		} else {
			// Second call: notifications/initialized.
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	defer backendServer.Close()

	proxy := newTestMCPProxy()
	proxy.backendListenerAddr = backendServer.URL

	res, err := proxy.initializeSession(t.Context(), "route1", filterapi.MCPBackend{Name: "test-backend", Path: "/a/b/c"}, &mcp.InitializeParams{})

	require.NoError(t, err)
	require.Equal(t, gatewayToMCPServerSessionID("test-session-123"), res.sessionID)
	require.Equal(t, 2, callCount.get("test-backend"))
}

func TestInitializeSession_InitializeFailure(t *testing.T) {
	// Mock backend server that fails initialization.
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("initialization failed"))
	}))
	defer backendServer.Close()

	proxy := newTestMCPProxy()
	proxy.backendListenerAddr = backendServer.URL

	sessionID, err := proxy.initializeSession(t.Context(), "route1", filterapi.MCPBackend{Name: "test-backend", Path: "/a/b/c"}, &mcp.InitializeParams{})

	require.Error(t, err)
	require.Empty(t, sessionID)
	require.Contains(t, err.Error(), "failed with status code")
}

func TestInitializeSession_NotificationsInitializedFailure(t *testing.T) {
	// Mock backend server.
	var callCount perBackendCallCount
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend := r.Header.Get(internalapi.MCPBackendHeader)
		if callCount.inc(backend) == 1 {
			// First call: initialize - success.
			w.Header().Set(sessionIDHeader, "test-session-123")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(validInitializeResponse))
		} else {
			// Second call: notifications/initialized - failure.
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("notifications/initialized failed"))
		}
	}))
	defer backendServer.Close()

	proxy := newTestMCPProxy()
	proxy.backendListenerAddr = backendServer.URL

	sessionID, err := proxy.initializeSession(t.Context(), "route1", filterapi.MCPBackend{Name: "test-backend", Path: "/aaaaaaaaaaaaaa"}, &mcp.InitializeParams{})

	require.Error(t, err)
	require.Empty(t, sessionID)
	require.Contains(t, err.Error(), "notifications/initialized request failed")
}

func TestInvokeJSONRPCRequest_Success(t *testing.T) {
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/aaaaaaaaaaaaaa", r.URL.Path)
		require.Equal(t, "test-backend", r.Header.Get("x-ai-eg-mcp-backend"))
		require.Equal(t, "test-session", r.Header.Get(sessionIDHeader))
		require.Equal(t, "application/json", r.Header.Get("Content-Type"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result": "success"}`))
	}))
	defer backendServer.Close()

	m := newTestMCPProxy()
	m.backendListenerAddr = backendServer.URL
	resp, err := m.invokeJSONRPCRequest(t.Context(), "route1", filterapi.MCPBackend{Name: "test-backend", Path: "/aaaaaaaaaaaaaa"}, &compositeSessionEntry{
		sessionID: "test-session",
	}, &jsonrpc.Request{})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

func TestInvokeJSONRPCRequest_NoSessionID(t *testing.T) {
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check the path equals /mcp.
		require.Equal(t, "/mcp", r.URL.Path)
		require.Equal(t, "test-backend", r.Header.Get("x-ai-eg-mcp-backend"))
		require.Empty(t, r.Header.Get(sessionIDHeader))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result": "success"}`))
	}))
	defer backendServer.Close()

	m := newTestMCPProxy()
	m.backendListenerAddr = backendServer.URL
	resp, err := m.invokeJSONRPCRequest(t.Context(), "route1", filterapi.MCPBackend{Name: "test-backend", Path: "/mcp"}, &compositeSessionEntry{
		sessionID: "",
	}, &jsonrpc.Request{})

	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())
}

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

func TestLoadConfig_BasicConfiguration(t *testing.T) {
	proxy := &ProxyConfig{
		mcpProxyConfig:   &mcpProxyConfig{},
		toolsChangedChan: make(chan struct{}, 1),
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
	// Create a channel we can receive from in the test
	toolsChangedChan := make(chan struct{}, 1)

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
		toolsChangedChan: toolsChangedChan,
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
	case <-toolsChangedChan:
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected tools changed notification but didn't receive one")
	}
}

func TestLoadConfig_NoToolsChangedNotification(t *testing.T) {
	// Create a channel we can receive from in the test
	toolsChangedChan := make(chan struct{}, 1)

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
		toolsChangedChan: toolsChangedChan,
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
	case <-toolsChangedChan:
		t.Fatal("unexpected tools changed notification")
	case <-time.After(100 * time.Millisecond):
		// Expected - no notification
	}
}

func TestLoadConfig_InvalidRegex(t *testing.T) {
	proxy := &ProxyConfig{
		mcpProxyConfig:   &mcpProxyConfig{},
		toolsChangedChan: make(chan struct{}, 1),
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
	// Create a channel we can receive from in the test
	toolsChangedChan := make(chan struct{}, 1)

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
		toolsChangedChan: toolsChangedChan,
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

	err := proxy.LoadConfig(t.Context(), config)
	require.NoError(t, err)

	// Should receive tools changed notification
	select {
	case <-toolsChangedChan:
		// Expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected tools changed notification but didn't receive one")
	}
}

func TestLoadConfig_ToolOrderDoesNotMatter(t *testing.T) {
	// Create a channel we can receive from in the test
	toolsChangedChan := make(chan struct{}, 1)

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
		toolsChangedChan: toolsChangedChan,
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
	case <-toolsChangedChan:
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
