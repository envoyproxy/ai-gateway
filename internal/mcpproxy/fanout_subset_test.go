// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func newFanoutSubsetTestServer() (*httptest.Server, *perBackendCallCount) {
	callCount := &perBackendCallCount{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		backend := r.Header.Get(internalapi.MCPBackendHeader)
		if backend == "" {
			http.Error(w, "missing backend header", http.StatusBadRequest)
			return
		}
		if callCount.inc(backend)%2 == 1 {
			w.Header().Set(sessionIDHeader, fmt.Sprintf("test-session-%s", backend))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(validInitializeResponse))
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	return server, callCount
}

func TestBackendSubset(t *testing.T) {
	tests := []struct {
		name   string
		setHdr bool
		value  string
		want   map[filterapi.MCPBackendName]struct{}
	}{
		{
			name:   "header absent -> nil (no subsetting, fan out to all)",
			setHdr: false,
			want:   nil,
		},
		{
			name:   "empty value -> nil",
			setHdr: true,
			value:  "",
			want:   nil,
		},
		{
			name:   "whitespace only -> nil",
			setHdr: true,
			value:  "   ",
			want:   nil,
		},
		{
			name:   "single backend",
			setHdr: true,
			value:  "slack",
			want:   map[filterapi.MCPBackendName]struct{}{"slack": {}},
		},
		{
			name:   "multiple backends",
			setHdr: true,
			value:  "slack,github",
			want:   map[filterapi.MCPBackendName]struct{}{"slack": {}, "github": {}},
		},
		{
			name:   "trims surrounding whitespace",
			setHdr: true,
			value:  " slack , github ",
			want:   map[filterapi.MCPBackendName]struct{}{"slack": {}, "github": {}},
		},
		{
			name:   "skips empty entries from stray commas",
			setHdr: true,
			value:  "slack,,github,",
			want:   map[filterapi.MCPBackendName]struct{}{"slack": {}, "github": {}},
		},
		{
			name:   "deduplicates repeated names",
			setHdr: true,
			value:  "slack,slack",
			want:   map[filterapi.MCPBackendName]struct{}{"slack": {}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.setHdr {
				h.Set(internalapi.MCPBackendSubsetHeader, tc.value)
			}
			require.Equal(t, tc.want, backendSubset(h))
		})
	}
}

func TestNewSession_BackendSubset(t *testing.T) {
	tests := []struct {
		name                string
		setHdr              bool
		header              string
		wantCalls           map[string]int
		wantSessionBackends []filterapi.MCPBackendName
		wantErr             string
	}{
		{
			name:                "header absent initializes all route backends",
			setHdr:              false,
			wantCalls:           map[string]int{"backend1": 2, "backend2": 2},
			wantSessionBackends: []filterapi.MCPBackendName{"backend1", "backend2"},
		},
		{
			name:                "header subset initializes selected route backend only",
			setHdr:              true,
			header:              "backend2",
			wantCalls:           map[string]int{"backend1": 0, "backend2": 2},
			wantSessionBackends: []filterapi.MCPBackendName{"backend2"},
		},
		{
			name:                "header with known and unknown backend initializes known backend only",
			setHdr:              true,
			header:              "unknown,backend1",
			wantCalls:           map[string]int{"backend1": 2, "backend2": 0, "unknown": 0},
			wantSessionBackends: []filterapi.MCPBackendName{"backend1"},
		},
		{
			name:      "header with only unknown backend fails without initializing",
			setHdr:    true,
			header:    "unknown",
			wantCalls: map[string]int{"backend1": 0, "backend2": 0, "unknown": 0},
			wantErr:   "failed to create MCP session to any backend",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			server, callCount := newFanoutSubsetTestServer()
			defer server.Close()

			proxy := newTestMCPProxy()
			proxy.backendListenerAddr = server.URL
			proxy.requestHeaders = http.Header{}
			if tc.setHdr {
				proxy.requestHeaders.Set(internalapi.MCPBackendSubsetHeader, tc.header)
			}

			s, err := proxy.newSession(t.Context(), &mcp.InitializeParams{}, "test-route", "", nil, time.Now())
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.Nil(t, s)
			} else {
				require.NoError(t, err)
				require.NotNil(t, s)
				require.Len(t, s.perBackendSessions, len(tc.wantSessionBackends))
				for _, backend := range tc.wantSessionBackends {
					require.Contains(t, s.perBackendSessions, backend)
				}
			}
			for backend, want := range tc.wantCalls {
				require.Equal(t, want, callCount.get(backend))
			}
		})
	}
}
