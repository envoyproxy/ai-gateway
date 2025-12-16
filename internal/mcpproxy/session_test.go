// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
)

// stubMetrics implements metrics.MCPMetrics with no-ops.
type stubMetrics struct{}

func (s stubMetrics) WithRequestAttributes(_ *http.Request) metrics.MCPMetrics            { return s }
func (stubMetrics) RecordRequestDuration(_ context.Context, _ time.Time, _ mcpsdk.Params) {}
func (stubMetrics) RecordRequestErrorDuration(_ context.Context, _ time.Time, _ metrics.MCPErrorType, _ mcpsdk.Params) {
}
func (stubMetrics) RecordMethodCount(_ context.Context, _ string, _ mcpsdk.Params)               {}
func (stubMetrics) RecordMethodFailedCount(_ context.Context, _ string, _ mcpsdk.Params)         {}
func (stubMetrics) RecordMethodErrorCount(_ context.Context, _ string, _ mcpsdk.Params)          {}
func (stubMetrics) RecordInitializationDuration(_ context.Context, _ time.Time, _ mcpsdk.Params) {}
func (stubMetrics) RecordClientCapabilities(_ context.Context, _ *mcpsdk.ClientCapabilities, _ mcpsdk.Params) {
}

func (stubMetrics) RecordServerCapabilities(_ context.Context, _ *mcpsdk.ServerCapabilities, _ mcpsdk.Params) {
}
func (stubMetrics) RecordProgress(_ context.Context, _ mcpsdk.Params) {}

func TestBackendSessionIDs_Success(t *testing.T) {
	backendA := "backendA"
	backendB := "backendB"
	idA := "session-a"
	idB := "session-b"
	routeName := "some-route"
	composite := clientToGatewaySessionID(routeName + "@" + "subject" + "@" + backendA + ":" + base64.StdEncoding.EncodeToString([]byte(idA)) + "," + backendB + ":" + base64.StdEncoding.EncodeToString([]byte(idB)))
	m, route, err := composite.backendSessionIDs()
	require.NoError(t, err)
	require.Equal(t, routeName, route)
	require.Equal(t, idA, string(m[backendA].sessionID))
	require.Equal(t, idB, string(m[backendB].sessionID))
}

func TestBackendSessionIDs_Errors(t *testing.T) {
	for _, tc := range []struct {
		input  clientToGatewaySessionID
		expErr string
	}{
		// Without two '@' characters.
		{input: "no_at_chars", expErr: `invalid session ID: missing '@' separator`},
		// Only one '@' character.
		{input: "one@at_char", expErr: `invalid session ID: missing '@' separator`},
		// No ':'.
		{input: "@@missing_colon", expErr: `invalid session ID: missing ':' separator in backend session ID part "missing_colon"`},
		// Empty backend.
		{input: "@@:YWJj", expErr: "empty backend name"},
		{input: "@@backend:not-base64", expErr: `invalid session ID: failed to base64 decode session ID in part "backend:not-base64"`},
	} {
		t.Run(string(tc.input), func(t *testing.T) {
			_, _, err := tc.input.backendSessionIDs()
			require.ErrorContains(t, err, tc.expErr)
		})
	}
}

func TestSession_Close(t *testing.T) {
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes.Add(1)
			if r.Header.Get(internalapi.MCPBackendHeader) == "backend1" || r.Header.Get(internalapi.MCPBackendHeader) == "backend2" {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	proxy := newTestMCPProxy()
	proxy.backendListenerAddr = server.URL
	s := &session{
		proxy: proxy,
		perBackendSessions: map[filterapi.MCPBackendName]*compositeSessionEntry{
			"backend1": {
				sessionID: "s1",
			},
			"backend2": {
				sessionID: "s2",
			},
		},
		route: "test-route",
	}
	err := s.Close()
	require.NoError(t, err)
	require.Equal(t, int32(2), deletes.Load())
}

func TestHandleNotificationsPerBackend_SSE(t *testing.T) {
	// Provide two SSE events with valid JSON-RPC requests then close.
	id1, _ := jsonrpc.MakeID("1")
	id2, _ := jsonrpc.MakeID("2")
	msg1, _ := jsonrpc.EncodeMessage(&jsonrpc.Request{Method: "ping", ID: id1})
	msg2, _ := jsonrpc.EncodeMessage(&jsonrpc.Request{Method: "pong", ID: id2})
	sseBody := "event: ping\n" + "data: " + string(msg1) + "\n\n" + "event: pong\n" + "data: " + string(msg2) + "\n\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Header.Get("Accept") != "text/event-stream, application/json" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunkSize := len(sseBody) / 3
		for i := 0; i < len(sseBody); i += chunkSize {
			end := i + chunkSize
			if end > len(sseBody) {
				end = len(sseBody)
			}
			_, _ = w.Write([]byte(sseBody[i:end]))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			time.Sleep(10 * time.Millisecond)
		}
	}))
	defer server.Close()
	l := slog.Default()
	proxy := &MCPProxy{mcpProxyConfig: &mcpProxyConfig{backendListenerAddr: server.URL}, l: l, metrics: stubMetrics{}}
	s := &session{proxy: proxy}
	ch := make(chan *sseEvent, 10)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	err := s.sendRequestPerBackend(ctx, ch, "route1", filterapi.MCPBackend{Name: "backend1"}, &compositeSessionEntry{
		sessionID: "sess1",
	}, http.MethodGet, nil)
	require.NoError(t, err)
	close(ch)
	count := 0
	for range ch {
		count++
	}
	require.Equal(t, 2, count, "expected 2 events")
}

func TestSession_StreamNotifications(t *testing.T) {
	tests := []struct {
		name              string
		eventInterval     time.Duration
		deadline          time.Duration
		heartbeatInterval time.Duration
		wantHeartbeats    bool
	}{
		// the default heartbeat interval is 1 second, but the events will come faster, so
		// we don't expect any heartbeats.
		{"fast events", 10 * time.Millisecond, 5 * time.Second, 10 * time.Second, false},
		// configure a heartbeat interval faster than the event interval, so we expect heartbeats.
		{"slow events", 20 * time.Millisecond, 5 * time.Second, 10 * time.Millisecond, true},
		// disable heartbeats. Even though events come in slowly, we don't expect heartbeats.
		{"no heartbeats", 20 * time.Millisecond, 5 * time.Second, 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Override the default heartbeat interval for testing.
			originalHeartbeatInterval := heartbeatInterval
			heartbeatInterval = tc.heartbeatInterval
			t.Cleanup(func() { heartbeatInterval = originalHeartbeatInterval })

			// Single backend streaming two events with valid messages.
			id1, _ := jsonrpc.MakeID("1")
			id2, _ := jsonrpc.MakeID("2")
			msg1, _ := jsonrpc.EncodeMessage(&jsonrpc.Request{Method: "a1", ID: id1})
			msg2, _ := jsonrpc.EncodeMessage(&jsonrpc.Request{Method: "a2", ID: id2})
			body := "event: a1\n" + "data: " + string(msg1) + "\n\n" + "event: a2\n" + "data: " + string(msg2) + "\n\n"
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				if r.Header.Get(internalapi.MCPBackendHeader) != "backend1" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				chunkSize := len(body) / 3
				for i := 0; i < len(body); i += chunkSize {
					end := i + chunkSize
					if end > len(body) {
						end = len(body)
					}
					_, _ = w.Write([]byte(body[i:end]))
					if f, ok := w.(http.Flusher); ok {
						f.Flush()
					}
					time.Sleep(tc.eventInterval)
				}
			}))
			defer srv.Close()
			proxy := newTestMCPProxy()
			proxy.backendListenerAddr = srv.URL

			s := &session{
				proxy: proxy,
				perBackendSessions: map[filterapi.MCPBackendName]*compositeSessionEntry{
					"backend1": {
						sessionID: "s1",
					},
				},
				route: "test-route",
			}
			rr := httptest.NewRecorder()
			ctx, cancel := context.WithTimeout(t.Context(), tc.deadline)
			defer cancel()
			err2 := s.streamNotifications(ctx, rr, proxy.toolChangeSignaler)
			require.NoError(t, err2)
			out := rr.Body.String()
			require.Contains(t, out, "event: a1")
			require.Contains(t, out, "event: a2")
			heartbeatCount := strings.Count(out, `"method":"ping"`)

			if tc.wantHeartbeats {
				require.Greater(t, heartbeatCount, 1, "expected some heartbeats after the initial one")
			} else {
				require.Equal(t, 1, heartbeatCount, "expected only the initial heartbeat")
			}
		})
	}
}

func TestNotifyToolsChanged(t *testing.T) {
	var (
		reloadConfig atomic.Bool
		proxy        = newTestMCPProxy()
		cfg          = ProxyConfig{
			toolChangeSignaler: proxy.toolChangeSignaler,
			mcpProxyConfig:     proxy.mcpProxyConfig,
		}
		s = &session{
			proxy: proxy,
			route: "test-route",
			perBackendSessions: map[filterapi.MCPBackendName]*compositeSessionEntry{
				"backend1": {sessionID: "s1"},
			},
		}
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// if the test wants to reload config, trigger it once the stream is open, to better simulate
		// changes when there is an active streaming session.
		// wait a bit and trigger the config change.
		if reloadConfig.Load() {
			time.Sleep(50 * time.Millisecond)
			require.NoError(t, cfg.LoadConfig(t.Context(),
				// Clear all the routes -> should trigger a tools changed notification.
				&filterapi.Config{MCPConfig: &filterapi.MCPConfig{}}),
			)
		}
	}))
	proxy.backendListenerAddr = srv.URL

	t.Run("no tool changes by default", func(t *testing.T) {
		rr := httptest.NewRecorder()
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		t.Cleanup(cancel)
		err := s.streamNotifications(ctx, rr, proxy.toolChangeSignaler)
		require.NoError(t, err)
		out := rr.Body.String()
		require.NotContains(t, out, `"id":"`+envoyAIGatewayServerToClientToolsChangedRequestIDPrefix)
		require.NotContains(t, out, `"method":"notifications/tools/list_changed"`)
	})

	t.Run("notify tools changed", func(t *testing.T) {
		reloadConfig.Store(true)
		rr := httptest.NewRecorder()
		ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		t.Cleanup(cancel)
		err := s.streamNotifications(ctx, rr, proxy.toolChangeSignaler)
		require.NoError(t, err)
		out := rr.Body.String()
		require.Contains(t, out, `"id":"`+envoyAIGatewayServerToClientToolsChangedRequestIDPrefix)
		require.Contains(t, out, `"method":"notifications/tools/list_changed"`)
	})
}

func TestSendRequestPerBackend_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("boom"))
	}))
	defer server.Close()
	l := slog.Default()
	proxy := &MCPProxy{mcpProxyConfig: &mcpProxyConfig{backendListenerAddr: server.URL}, l: l, metrics: stubMetrics{}}
	s := &session{proxy: proxy}
	ch := make(chan *sseEvent, 1)
	cse := &compositeSessionEntry{
		sessionID: "sess1",
	}
	err2 := s.sendRequestPerBackend(t.Context(), ch, "route1", filterapi.MCPBackend{Name: "backend1"}, cse, http.MethodGet, nil)
	require.Error(t, err2)
	require.Contains(t, err2.Error(), "failed with status code")
}

func TestSendRequestPerBackend_EOF(t *testing.T) {
	// Immediate EOF (empty body) should return nil (no error) after loop breaks with EOF.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// No writes -> body closes -> EOF.
	}))
	defer server.Close()
	l := slog.Default()
	proxy := &MCPProxy{mcpProxyConfig: &mcpProxyConfig{backendListenerAddr: server.URL}, l: l, metrics: stubMetrics{}}
	s := &session{proxy: proxy}
	ch := make(chan *sseEvent, 1)
	err2 := s.sendRequestPerBackend(t.Context(), ch, "route1", filterapi.MCPBackend{Name: "backend1"}, &compositeSessionEntry{
		sessionID: "sess1",
	}, http.MethodGet, nil)
	require.True(t, err2 == nil || errors.Is(err2, io.EOF), "unexpected error: %v", err2)
}

func TestGetHeartbeatInterval(t *testing.T) {
	defaultInterval := 1 * time.Minute

	tests := []struct {
		name string
		env  string
		want time.Duration
	}{
		{"unset", "", defaultInterval},
		{"invalid", "invalid", defaultInterval},
		{"zero", "0s", 0},
		{"value", "5m", 5 * time.Minute},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.env != "" {
				t.Setenv("MCP_PROXY_HEARTBEAT_INTERVAL", tt.env)
			}
			require.Equal(t, tt.want, getHeartbeatInterval(defaultInterval))
		})
	}
}
