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
	"sync"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
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
