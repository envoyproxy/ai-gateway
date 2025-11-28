// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"bytes"
	"cmp"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/internal/tracing"
	tracingapi "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

func newTestMCPProxy() *MCPProxy {
	return newTestMCPProxyWithTracer(noopTracer)
}

func newTestMCPProxyWithTracer(t tracingapi.MCPTracer) *MCPProxy {
	// reduce the iterations for faster tests. The default is a good tradeoff between security
	// and performance, but for the tests we can use fewer iterations to make tests faster.
	sessionCrypto := NewPBKDF2AesGcmSessionCrypto("test", 100)

	return &MCPProxy{
		sessionCrypto: sessionCrypto,
		mcpProxyConfig: &mcpProxyConfig{
			backendListenerAddr: "http://test-backend",
			routes: map[filterapi.MCPRouteName]*mcpProxyConfigRoute{
				"test-route": {
					toolSelectors: map[filterapi.MCPBackendName]*toolSelector{
						"backend1": {include: map[string]struct{}{"test-tool": {}}},
					},
					backends: map[filterapi.MCPBackendName]filterapi.MCPBackend{
						"backend1": {Name: "backend1", Path: "/mcp"},
						"backend2": {Name: "backend2", Path: "/"},
					},
				},
				"test-route-another": {
					backends: map[filterapi.MCPBackendName]filterapi.MCPBackend{
						"backend3": {Name: "backend3", Path: "/mcp"},
					},
				},
			},
		},
		metrics: stubMetrics{},
		tracer:  t,
		l:       slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

func newTestMCPProxyWithOTEL(mr *sdkmetric.ManualReader, tracer tracingapi.MCPTracer) *MCPProxy {
	mcpProxy := newTestMCPProxyWithTracer(tracer)
	meter := sdkmetric.NewMeterProvider(sdkmetric.WithReader(mr)).Meter("test")
	mcpProxy.metrics = metrics.NewMCP(meter, nil)
	return mcpProxy
}

func TestServeGET_MissingSessionID(t *testing.T) {
	proxy := newTestMCPProxy()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	rr := httptest.NewRecorder()

	proxy.serveGET(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "missing session ID")
}

func TestServeGET_InvalidSessionID(t *testing.T) {
	proxy := newTestMCPProxy()
	req := httptest.NewRequest(http.MethodGet, "/mcp", nil)
	req.Header.Set(sessionIDHeader, "invalid-session-id")
	rr := httptest.NewRecorder()

	proxy.serveGET(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "invalid session ID")
}

func TestServeGET_OK(t *testing.T) {
	proxy := newTestMCPProxy()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	sessionID := secureID(t, proxy, "@@backend1:dGVzdC1zZXNzaW9u") // "test-session" base64 encoded.
	req.Header.Set(sessionIDHeader, sessionID)
	rr := httptest.NewRecorder()

	proxy.serveGET(rr, req)
	require.Equal(t, http.StatusAccepted, rr.Code)
}

func TestServerDELETE_MissingSessionID(t *testing.T) {
	proxy := newTestMCPProxy()
	req := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	rr := httptest.NewRecorder()

	proxy.serverDELETE(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "missing session ID")
}

func TestServerDELETE_InvalidSessionID(t *testing.T) {
	proxy := newTestMCPProxy()
	req := httptest.NewRequest(http.MethodDelete, "/mcp", nil)
	req.Header.Set(sessionIDHeader, "invalid-session-id")
	rr := httptest.NewRecorder()

	proxy.serverDELETE(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "invalid session ID")
}

func TestServeDELETE_OK(t *testing.T) {
	proxy := newTestMCPProxy()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	sessionID := secureID(t, proxy, "@@backend1:dGVzdC1zZXNzaW9u") // "test-session" base64 encoded.
	req.Header.Set(sessionIDHeader, sessionID)
	rr := httptest.NewRecorder()
	proxy.serverDELETE(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
}

func TestServePOST_InvalidJSONRPC(t *testing.T) {
	proxy := newTestMCPProxy()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader("{invalid json}"))
	rr := httptest.NewRecorder()

	proxy.servePOST(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "invalid JSON-RPC message")
}

func TestServePOST_InvalidSessionID(t *testing.T) {
	proxy := newTestMCPProxy()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"test-tool"},"id":"1"}`))
	req.Header.Set(sessionIDHeader, "invalid-session-id")
	rr := httptest.NewRecorder()
	proxy.servePOST(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "invalid session ID")
}

func TestServePOST_MissingSessionID(t *testing.T) {
	proxy := newTestMCPProxy()
	req := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"tools/call","params":{"name":"test-tool"},"id":"1"}`))
	rr := httptest.NewRecorder()
	proxy.servePOST(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "missing session ID")
}

func TestServePOST_InitializeRequest(t *testing.T) {
	// Create a test server to simulate the mcp backend listener.
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hasSessionID := r.Header.Get(sessionIDHeader) != ""
		backend := r.Header.Get(internalapi.MCPBackendHeader)

		// simulate an initialize failure for the second backend.
		if backend == "backend2" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("simulated backend error"))
			return
		}

		if !hasSessionID { // Call to initialize.
			w.Header().Set(sessionIDHeader, "test-session-123")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(validInitializeResponse))
		} else { // Call to notifications/initialized.
			w.WriteHeader(http.StatusAccepted)
		}
	}))
	t.Cleanup(testServer.Close)

	mr := sdkmetric.NewManualReader()
	proxy := newTestMCPProxyWithOTEL(mr, noopTracer)
	proxy.backendListenerAddr = testServer.URL

	// Create initialize request.
	id, err := jsonrpc.MakeID("test-1")
	require.NoError(t, err)
	initReq := &jsonrpc.Request{
		Method: "initialize",
		ID:     id,
		Params: json.RawMessage(`{
    "protocolVersion": "2024-11-05",
    "capabilities": {
      "roots": {
        "listChanged": true
      },
      "sampling": {},
      "elicitation": {}
    },
    "clientInfo": {
      "name": "ExampleClient",
      "title": "Example Client Display Name",
      "version": "1.0.0"
    }
  }`),
	}
	body, err := jsonrpc.EncodeMessage(initReq)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// This header is used to establish MCP session with the backends associated with the route.
	// It is set by the frontend listeners based on the selected route.
	req.Header.Set(internalapi.MCPRouteHeader, "test-route")
	rr := httptest.NewRecorder()

	proxy.servePOST(rr, req)

	if rr.Code != http.StatusOK {
		t.Logf("Response body: %s", rr.Body.String())
	}
	require.Equal(t, http.StatusOK, rr.Code)
	require.Equal(t, "application/json", rr.Header().Get("Content-Type"))
	require.NotEmpty(t, rr.Header().Get(sessionIDHeader))

	decrypted, err := proxy.sessionCrypto.Decrypt(rr.Header().Get(sessionIDHeader))
	require.NoError(t, err)
	perBackendSessions, _, err := clientToGatewaySessionID(decrypted).backendSessionIDs()
	require.NoError(t, err)
	require.ElementsMatch(t, []filterapi.MCPBackendName{"backend1"}, slices.Collect(maps.Keys(perBackendSessions)))

	count, sum := testotel.GetHistogramValues(t, mr, "mcp.initialization.duration", attribute.NewSet())
	require.Equal(t, 1, int(count)) // nolint: gosec
	require.Greater(t, sum, 0.0)

	capaCount := testotel.GetCounterValue(t, mr, "mcp.capabilities.negotiated", attribute.NewSet(
		attribute.String("capability.type", "tools"),
		attribute.String("capability.side", "server")))
	require.Equal(t, 1, int(capaCount))

	capaCount = testotel.GetCounterValue(t, mr, "mcp.capabilities.negotiated", attribute.NewSet(
		attribute.String("capability.type", "roots"),
		attribute.String("capability.side", "client")))
	require.Equal(t, 1, int(capaCount))
}

// TestServePOST_JSONRPCRequest tests various jsonrpc.Request body, not jsonrpc.Response.
func TestServePOST_JSONRPCRequest(t *testing.T) {
	tests := []struct {
		name             string
		method           string
		upstreamResponse string
		// expected HTTP status code from the proxy.
		expStatusCode int
		// expected body substring on non-200 status code, i.e. when expStatusCode != 200.
		expBodyOnNonOKStatus string
		params               any
		validate             func(*testing.T, json.RawMessage)
	}{
		{
			method:               "unknown-method",
			params:               &mcp.ListToolsParams{},
			expStatusCode:        400,
			expBodyOnNonOKStatus: `unsupported method: unknown-method`,
		},
		{
			method:        "notifications/cancelled",
			params:        &mcp.ListToolsParams{},
			expStatusCode: 202,
		},
		{
			name:          "initialize invalid param",
			method:        "initialize",
			params:        "invalid-param",
			expStatusCode: 400,
		},
		{
			name:          "initialize without route header",
			method:        "initialize",
			params:        &mcp.InitializeParams{},
			expStatusCode: 500,
		},
		{
			method:           "tools/list",
			upstreamResponse: `{"jsonrpc":"2.0","id":"1","result":{"tools":[{"name":"my-tool"},{"name":"test-tool"}]}}`,
			params:           &mcp.ListToolsParams{},
			expStatusCode:    200,
			validate: func(t *testing.T, raw json.RawMessage) {
				var result mcp.ListToolsResult
				require.NoError(t, json.Unmarshal(raw, &result))
				require.Len(t, result.Tools, 1)
				require.Equal(t, "backend1__test-tool", result.Tools[0].Name)
			},
		},
		{
			method:           "notifications/roots/list_changed",
			upstreamResponse: `{"jsonrpc":"2.0","id":"1","result":{}}`,
			params:           &mcp.RootsListChangedParams{},
			expStatusCode:    202,
		},
		{
			name:          "notifications/roots/list_changed invalid param",
			method:        "notifications/roots/list_changed",
			params:        "invalid-param",
			expStatusCode: 400,
		},
		{
			method:           "prompts/list",
			upstreamResponse: `{"jsonrpc":"2.0","id":"1","result":{"prompts":[{"name":"my-prompt"}]}}`,
			params:           &mcp.ListPromptsParams{},
			expStatusCode:    200,
			validate: func(t *testing.T, raw json.RawMessage) {
				var result mcp.ListPromptsResult
				require.NoError(t, json.Unmarshal(raw, &result))
				require.Len(t, result.Prompts, 1)
				require.Equal(t, "backend1__my-prompt", result.Prompts[0].Name)
			},
		},
		{
			name:          "prompts/list invalid param type",
			method:        "prompts/list",
			params:        "invalid",
			expStatusCode: 400,
		},
		{
			method:           "resources/list",
			upstreamResponse: `{"jsonrpc":"2.0","id":"1","result":{"resources":[{"name":"my-resource"}]}}`,
			params:           &mcp.ListResourcesParams{},
			expStatusCode:    200,
			validate: func(t *testing.T, raw json.RawMessage) {
				var result mcp.ListResourcesResult
				require.NoError(t, json.Unmarshal(raw, &result))
				require.Len(t, result.Resources, 1)
				require.Equal(t, "backend1__my-resource", result.Resources[0].Name)
			},
		},
		{
			method:           "resources/templates/list",
			upstreamResponse: `{"jsonrpc":"2.0","id":"1","result":{"resourceTemplates":[{"name":"my-template"}]}}`,
			params:           &mcp.ListResourceTemplatesParams{},
			expStatusCode:    200,
			validate: func(t *testing.T, raw json.RawMessage) {
				var result mcp.ListResourceTemplatesResult
				require.NoError(t, json.Unmarshal(raw, &result))
				require.Len(t, result.ResourceTemplates, 1)
				require.Equal(t, "backend1__my-template", result.ResourceTemplates[0].Name)
			},
		},
		{
			method:           "completion/complete",
			upstreamResponse: `{"jsonrpc":"2.0","id":"1","result":{"completion": {"values":["completed text"]}}}`,
			expStatusCode:    200,
			params: &mcp.CompleteParams{
				Ref: &mcp.CompleteReference{Name: "backend1__my-completion", Type: "ref/prompt"},
			},
			validate: func(t *testing.T, raw json.RawMessage) {
				var result mcp.CompleteResult
				require.NoError(t, json.Unmarshal(raw, &result))
				fmt.Printf("Completion result: %+v\n", result)
				require.Len(t, result.Completion.Values, 1)
				require.Equal(t, "completed text", result.Completion.Values[0])
			},
		},
		{
			name:          "completion/complete invalid param type",
			method:        "completion/complete",
			expStatusCode: 400,
			// Invalid mismatched type.
			params:               "aaaaaaaaaaaa",
			expBodyOnNonOKStatus: `invalid params`,
		},
		{
			method:           "notifications/progress",
			upstreamResponse: `{"jsonrpc":"2.0","id":"1","result":{"completion": {"values":["completed text"]}}}`,
			expStatusCode:    200,
			params:           &mcp.ProgressNotificationParams{ProgressToken: "1234__i__backend1"},
		},
		{
			name:                 "notifications/progress invalid param",
			method:               "notifications/progress",
			expStatusCode:        400,
			params:               "aaaaaaaa",
			expBodyOnNonOKStatus: `invalid params`,
		},
		{
			name:                 "notifications/progress invalid token",
			method:               "notifications/progress",
			expStatusCode:        400,
			params:               &mcp.ProgressNotificationParams{ProgressToken: "invalid-token"},
			expBodyOnNonOKStatus: `invalid progressToken invalid-token`,
		},
		{
			method:        "notifications/initialized",
			expStatusCode: 202,
			params:        &mcp.InitializeParams{},
		},
		{
			method:        "logging/setLevel",
			expStatusCode: 200,
			params:        &mcp.SetLoggingLevelParams{Level: "debug"},
		},
		{
			name:                 "logging/setLevel invalid param",
			method:               "logging/setLevel",
			expStatusCode:        400,
			params:               "invalid-param",
			expBodyOnNonOKStatus: `invalid set logging level params`,
		},
		{
			method:           "resources/subscribe",
			expStatusCode:    200,
			upstreamResponse: `{"jsonrpc":"2.0","id":"1","result":{"subscriptionId":"sub-1234"}}`,
			params:           &mcp.SubscribeParams{URI: "backend1+file://my-resource"},
		},
		{
			name:                 "resources/subscribe invalid param",
			method:               "resources/subscribe",
			expStatusCode:        400,
			params:               &mcp.SubscribeParams{URI: "file://my-resource"},
			expBodyOnNonOKStatus: `invalid resource URI: file://my-resource`,
		},
		{
			method:           "resources/unsubscribe",
			expStatusCode:    200,
			upstreamResponse: `{"jsonrpc":"2.0","id":"1","result":{"subscriptionId":"sub-1234"}}`,
			params:           &mcp.UnsubscribeParams{URI: "backend1+file://my-resource"},
		},
		{
			name:                 "resources/unsubscribe invalid param",
			method:               "resources/unsubscribe",
			expStatusCode:        400,
			params:               &mcp.UnsubscribeParams{URI: "file://my-resource"},
			expBodyOnNonOKStatus: `invalid resource URI: file://my-resource`,
		},
		{
			method:           "resources/read",
			params:           &mcp.ReadResourceParams{URI: "backend1+file://my-resource"},
			upstreamResponse: `{"jsonrpc":"2.0","id":"1","result":{"contents":[{"uri":"file://my-resource"}]}}`,
			expStatusCode:    200,
			validate: func(t *testing.T, raw json.RawMessage) {
				var result mcp.ReadResourceResult
				require.NoError(t, json.Unmarshal(raw, &result))
				require.Len(t, result.Contents, 1)
				require.Equal(t, "backend1+file://my-resource", result.Contents[0].URI)
			},
		},
		{
			name:                 "resources/read invalid param",
			method:               "resources/read",
			expStatusCode:        400,
			params:               &mcp.ReadResourceParams{URI: "file://my-resource"},
			expBodyOnNonOKStatus: `invalid resource URI: file://my-resource`,
		},
	}

	for _, tt := range tests {
		t.Run(cmp.Or(tt.name, tt.method), func(t *testing.T) {
			// Mock backend server.
			backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.upstreamResponse))
			}))
			t.Cleanup(backendServer.Close)

			proxy := newTestMCPProxy()
			proxy.backendListenerAddr = backendServer.URL

			// Create a session with the test tool route.
			sessionID := secureID(t, proxy, "test-route@@backend1:dGVzdC1zZXNzaW9u") // "test-session" base64 encoded.

			// Create tools/call request.
			id, err := jsonrpc.MakeID("1")
			require.NoError(t, err)
			paramsData, err := json.Marshal(tt.params)
			require.NoError(t, err)
			toolReq := &jsonrpc.Request{
				Method: tt.method,
				ID:     id,
				Params: paramsData,
			}
			body, err := jsonrpc.EncodeMessage(toolReq)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set(sessionIDHeader, sessionID)
			rr := httptest.NewRecorder()

			proxy.servePOST(rr, req)
			require.NoError(t, err)
			require.Equal(t, tt.expStatusCode, rr.Code, rr.Body.String())
			if tt.expStatusCode != 200 {
				require.Contains(t, rr.Body.String(), tt.expBodyOnNonOKStatus)
				return
			}

			var resp *jsonrpc.Response
			if rr.Header().Get("content-type") != "text/event-stream" {
				// Regular JSON response.
				var msg jsonrpc.Message
				msg, err = jsonrpc.DecodeMessage(rr.Body.Bytes())
				require.NoError(t, err)
				var ok bool
				resp, ok = msg.(*jsonrpc.Response)
				require.True(t, ok)
			} else {
				p := newSSEEventParser(rr.Body, "backend1")
				var event *sseEvent
				event, err = p.next()
				require.NoError(t, err)
				require.Len(t, event.messages, 1)
				var ok bool
				resp, ok = event.messages[0].(*jsonrpc.Response)
				require.True(t, ok)
			}
			if tt.validate != nil {
				tt.validate(t, resp.Result)
			}
		})
	}
}

func TestServePOST_ToolsCallRequest(t *testing.T) {
	tests := []struct {
		name        string
		route       string
		tool        string
		wantBackend string
		wantStatus  int
	}{
		{name: "backend1", route: "test-route", tool: "backend1__test-tool", wantBackend: "backend1", wantStatus: http.StatusOK},
		{name: "backend2", route: "test-route", tool: "backend2__test-tool", wantBackend: "backend2", wantStatus: http.StatusOK},
		{name: "backend3", route: "test-route-another", tool: "backend3__test-tool", wantBackend: "backend3", wantStatus: http.StatusOK},
		{name: "test-route-another", tool: "unknown__test-tool", wantBackend: "unknown", wantStatus: http.StatusNotFound},
		{name: "backend1-not-whitelisted", route: "test-route", tool: "backend1__custom-tool", wantBackend: "backend1", wantStatus: http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Mock backend server.
			backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Validate that all metadata headers are set.
				require.Equal(t, tt.wantBackend, r.Header.Get(internalapi.MCPBackendHeader))
				require.Equal(t, "tools/call", r.Header.Get(internalapi.MCPMetadataHeaderMethod))
				require.Equal(t, tt.tool, r.Header.Get(internalapi.MCPMetadataHeaderRequestID))
				for h := range internalapi.MCPInternalHeadersToMetadata {
					require.NotEmpty(t, r.Header.Get(h))
				}

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":"success"}`))
			}))
			t.Cleanup(backendServer.Close)

			mr := sdkmetric.NewManualReader()
			proxy := newTestMCPProxyWithOTEL(mr, noopTracer)
			proxy.backendListenerAddr = backendServer.URL

			// Create a session with the test tool route.
			sessionID := secureID(t, proxy, tt.route+"@@"+tt.wantBackend+":dGVzdC1zZXNzaW9u") // "test-session" base64 encoded.

			// Create tools/call request.
			id, err := jsonrpc.MakeID(tt.tool)
			require.NoError(t, err)
			params := &mcp.CallToolParams{Name: tt.tool}
			paramsData, err := json.Marshal(params)
			require.NoError(t, err)
			toolReq := &jsonrpc.Request{
				Method: "tools/call",
				ID:     id,
				Params: paramsData,
			}
			body, err := jsonrpc.EncodeMessage(toolReq)
			require.NoError(t, err)

			req := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			// This header is used to establish MCP session with the backends associated with the route.
			// It is set by the frontend listeners based on the selected route.
			req.Header.Set(internalapi.MCPRouteHeader, "test-route")
			req.Header.Set(sessionIDHeader, sessionID)
			rr := httptest.NewRecorder()

			proxy.servePOST(rr, req)

			require.Equal(t, tt.wantStatus, rr.Code)

			var countAttrs, durationAttrs attribute.Set
			if tt.wantStatus == http.StatusOK {
				countAttrs = attribute.NewSet(
					attribute.String("mcp.method.name", "tools/call"),
					attribute.String("status", "success"),
				)
				durationAttrs = attribute.NewSet()
			} else {
				countAttrs = attribute.NewSet(attribute.String("status", "error"))
				durationAttrs = attribute.NewSet(attribute.String("error.type", string(metrics.MCPErrorInvalidParam)))
			}

			methodCount := testotel.GetCounterValue(t, mr, "mcp.method.count", countAttrs)
			require.Equal(t, 1, int(methodCount))

			count, sum := testotel.GetHistogramValues(t, mr, "mcp.request.duration", durationAttrs)
			require.Equal(t, 1, int(count)) // nolint: gosec
			require.Greater(t, sum, 0.0)
		})
	}
}

func TestServePOST_UnsupportedMethod(t *testing.T) {
	mr := sdkmetric.NewManualReader()
	proxy := newTestMCPProxyWithOTEL(mr, noopTracer)
	t.Cleanup(func() {
		if err := mr.Shutdown(t.Context()); err != nil {
			t.Logf("failed to shutdown manual reader: %v", err)
		}
	})

	// Create request with unsupported method.
	id, err := jsonrpc.MakeID("test-1")
	require.NoError(t, err)
	req := &jsonrpc.Request{
		Method: "unsupported/method",
		ID:     id,
	}
	body, err := jsonrpc.EncodeMessage(req)
	require.NoError(t, err)

	httpReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	httpReq.Header.Set(sessionIDHeader, secureID(t, proxy, "test-route@@backend1:dGVzdC1zZXNzaW9u")) // "test-session" base64 encoded.
	rr := httptest.NewRecorder()

	proxy.servePOST(rr, httpReq)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "unsupported method")

	methodCount := testotel.GetCounterValue(t, mr, "mcp.method.count", attribute.NewSet(
		attribute.String("status", "error")))
	require.Equal(t, 1, int(methodCount))

	count, sum := testotel.GetHistogramValues(t, mr, "mcp.request.duration", attribute.NewSet(
		attribute.String("error.type", "unsupported_method")))
	require.Equal(t, 1, int(count)) // nolint: gosec
	require.Greater(t, sum, 0.0)
}

func TestHandleToolCallRequest_UnknownBackend(t *testing.T) {
	proxy := newTestMCPProxy()
	s := &session{
		proxy:              proxy,
		perBackendSessions: map[filterapi.MCPBackendName]*compositeSessionEntry{},
	}

	params := &mcp.CallToolParams{Name: "unknown-backend__unknown-tool"}
	rr := httptest.NewRecorder()

	err := proxy.handleToolCallRequest(t.Context(), s, rr, &jsonrpc.Request{}, params, nil, nil)
	require.Error(t, err)

	require.Equal(t, http.StatusNotFound, rr.Code)
	require.Contains(t, rr.Body.String(), "unknown backend unknown-backend")
}

func TestHandleToolCallRequest_BackendError(t *testing.T) {
	// Mock backend server that returns error.
	backendServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("backend error"))
	}))
	t.Cleanup(backendServer.Close)

	proxy := newTestMCPProxy()
	proxy.backendListenerAddr = backendServer.URL
	s := &session{
		proxy: proxy,
		perBackendSessions: map[filterapi.MCPBackendName]*compositeSessionEntry{
			"backend1": {
				sessionID: "test-session",
			},
		},
		route: "test-route",
	}

	params := &mcp.CallToolParams{Name: "backend1__test-tool"}
	rr := httptest.NewRecorder()

	err := proxy.handleToolCallRequest(t.Context(), s, rr, &jsonrpc.Request{}, params, nil, nil)
	require.Error(t, err)

	require.Equal(t, http.StatusInternalServerError, rr.Code)
	require.Contains(t, rr.Body.String(), "call to backend1 failed with status code 500, body=backend error")
}

func TestProxyResponseBody_JSONResponse(t *testing.T) {
	proxy := newTestMCPProxy()

	id := mustJSONRPCRequestID()
	resp := &jsonrpc.Response{ID: id, Result: json.RawMessage(`{"test": "data"}`)}
	body, err := jsonrpc.EncodeMessage(resp)
	require.NoError(t, err)

	httpResp := &http.Response{
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(body)),
		StatusCode: http.StatusOK,
	}

	rr := httptest.NewRecorder()

	proxy.proxyResponseBody(t.Context(), nil, rr, httpResp, &jsonrpc.Request{ID: id}, filterapi.MCPBackend{Name: "mybackend"})

	require.Contains(t, rr.Body.String(), "test")
	require.Contains(t, rr.Body.String(), "data")
	// Verify that the response ID matches the request ID.
	require.Contains(t, rr.Body.String(), id.Raw())
}

func TestProxyResponseBody_SSEResponse(t *testing.T) {
	proxy := newTestMCPProxy()

	// Create SSE response.
	id := mustJSONRPCRequestID()
	res := &jsonrpc.Response{ID: id}
	msg, err := jsonrpc.EncodeMessage(res)
	require.NoError(t, err)

	invalidSeverToClientReq := &jsonrpc.Request{Method: "roots/list", ID: id, Params: json.RawMessage(`{"invalid": "json"}`)}
	invalidReqBody, err := jsonrpc.EncodeMessage(invalidSeverToClientReq)
	require.NoError(t, err)

	sseBody := fmt.Sprintf(`
event: test
data: %s

event: test
data: %s

`, invalidReqBody, msg)

	httpResp := &http.Response{
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(sseBody)),
		StatusCode: http.StatusOK,
	}

	rr := httptest.NewRecorder()
	sessionID := secureID(t, proxy, "@@backend1:"+base64.StdEncoding.EncodeToString([]byte("test-session")))
	eventID := secureID(t, proxy, "@@backend1:"+base64.StdEncoding.EncodeToString([]byte("_1")))
	s, err := proxy.sessionFromID(secureClientToGatewaySessionID(sessionID), secureClientToGatewayEventID(eventID))
	require.NoError(t, err)

	proxy.proxyResponseBody(t.Context(), s, rr, httpResp, &jsonrpc.Request{Method: "test", ID: id}, filterapi.MCPBackend{Name: "mybackend"})

	require.Contains(t, rr.Body.String(), "event: test")
	require.Contains(t, rr.Body.String(), "data:")

	// Verify that the response ID matches the request ID.
	require.Contains(t, rr.Body.String(), id.Raw())
}

func TestOnError(t *testing.T) {
	rr := httptest.NewRecorder()
	onErrorResponse(rr, http.StatusBadRequest, "test error")

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Equal(t, "text/plain; charset=utf-8", rr.Header().Get("Content-Type"))
	require.Equal(t, "test error", rr.Body.String())
}

func TestRecordResponse(t *testing.T) {
	t.Run("response", func(t *testing.T) {
		msg := jsonrpc.Response{Result: json.RawMessage(`{"test": "data"}`)}
		proxy := newTestMCPProxy()
		proxy.recordResponse(t.Context(), &msg)
	})
	t.Run("request", func(t *testing.T) {
		for _, tc := range []struct {
			method string
		}{
			{method: "notifications/prompts/list_changed"},
			{method: "notifications/resources/list_changed"},
			{method: "notifications/resources/updated"},
			{method: "notifications/progress"},
			{method: "roots/list"},
			{method: "notifications/message"},
			{method: "sampling/createMessage"},
			{method: "elicitation/create"},
			{method: "notifications/tools/list_changed"},
		} {
			msg := jsonrpc.Request{Method: tc.method}
			proxy := newTestMCPProxy()
			proxy.recordResponse(t.Context(), &msg)
		}
	})

	t.Run("unsupported method", func(t *testing.T) {
		proxy := newTestMCPProxy()

		id, err := jsonrpc.MakeID("test")
		require.NoError(t, err)
		req := &jsonrpc.Request{Method: "unsupported/server/method", ID: id}
		proxy.recordResponse(t.Context(), req)
	})
	t.Run("unsupported message type", func(t *testing.T) {
		proxy := newTestMCPProxy()
		proxy.recordResponse(t.Context(), nil)
	})
}

func TestServePOST_NotificationsInitialized(t *testing.T) {
	proxy := newTestMCPProxy()

	// Create notifications/initialized request.
	req := &jsonrpc.Request{
		Method: "notifications/initialized",
		Params: json.RawMessage(`{}`),
	}
	body, err := jsonrpc.EncodeMessage(req)
	require.NoError(t, err)

	httpReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	httpReq.Header.Set(sessionIDHeader, secureID(t, proxy, "test-route@@backend1:dGVzdC1zZXNzaW9u")) // "test-session" base64 encoded.
	rr := httptest.NewRecorder()

	proxy.servePOST(rr, httpReq)

	require.Equal(t, http.StatusAccepted, rr.Code)
}

func TestServePOST_PromptsGet(t *testing.T) {
	tracer := &fakeTracer{}
	proxy := newTestMCPProxyWithTracer(tracer)

	// Create a valid session.
	sessionID := secureID(t, proxy, "@@default-backend:"+base64.StdEncoding.EncodeToString([]byte("test-session")))

	// Create prompts/get request.
	params := &mcp.GetPromptParams{Name: "somebackend__test-prompt"}
	paramsData, err := json.Marshal(params)
	require.NoError(t, err)
	req := &jsonrpc.Request{
		Method: "prompts/get",
		Params: paramsData,
	}
	body, err := jsonrpc.EncodeMessage(req)
	require.NoError(t, err)

	httpReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	httpReq.Header.Set(sessionIDHeader, sessionID)
	rr := httptest.NewRecorder()

	proxy.servePOST(rr, httpReq)

	// Should try to route but fail since no backend server.
	require.Equal(t, http.StatusNotFound, rr.Code)
	require.Contains(t, rr.Body.String(), "unknown backend somebackend")

	// EndSpanOnError called.
	require.NotNil(t, tracer.span)
}

func TestServePOST_InvalidToolCallParams(t *testing.T) {
	tracer := &fakeTracer{}
	proxy := newTestMCPProxyWithTracer(tracer)

	// Create tools/call request where the params is an array instead of object.
	// This should fail when trying to unmarshal into CallToolParams struct.
	req := &jsonrpc.Request{
		Method: "tools/call",
		Params: json.RawMessage(`["invalid", "array", "params"]`), // array instead of object.
	}
	body, err := jsonrpc.EncodeMessage(req)
	require.NoError(t, err)

	httpReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	httpReq.Header.Set("Content-Type", "application/json")
	// Need to provide a session ID for tools/call requests.
	sessionID := secureID(t, proxy, "@@backend1:"+base64.StdEncoding.EncodeToString([]byte("test-session")))
	httpReq.Header.Set(sessionIDHeader, sessionID)
	rr := httptest.NewRecorder()

	proxy.servePOST(rr, httpReq)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "invalid params")

	require.Nil(t, tracer.span)
}

func TestServePOST_InvalidPromptsGetParams(t *testing.T) {
	proxy := newTestMCPProxy()

	// Create prompts/get request where the params is an array instead of object.
	// This should fail when trying to unmarshal into GetPromptParams struct.
	req := &jsonrpc.Request{
		Method: "prompts/get",
		Params: json.RawMessage(`["invalid", "array", "params"]`), // array instead of object.
	}
	body, err := jsonrpc.EncodeMessage(req)
	require.NoError(t, err)

	httpReq := httptest.NewRequest(http.MethodPost, "/mcp", bytes.NewReader(body))
	httpReq.Header.Set(sessionIDHeader, secureID(t, proxy, "test-route@@backend1:dGVzdC1zZXNzaW9u")) // "test-session" base64 encoded.
	rr := httptest.NewRecorder()

	proxy.servePOST(rr, httpReq)

	require.Equal(t, http.StatusBadRequest, rr.Code)
	require.Contains(t, rr.Body.String(), "invalid params")
}

func Test_downstreamName(t *testing.T) {
	require.Equal(t, "learn-microsoft__resource",
		downstreamResourceName("resource", "learn-microsoft"))
}

func Test_upstreamResourceName(t *testing.T) {
	cases := []struct {
		input           string
		expectedTool    string
		expectedBackend string
		expectedErr     string
	}{
		{
			input:           "learn-microsoft__microsoft_docs_search",
			expectedBackend: "learn-microsoft",
			expectedTool:    "microsoft_docs_search",
		},
		{
			input:       "namewithoutsep",
			expectedErr: "invalid resource name: namewithoutsep",
		},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			backend, tool, err := upstreamResourceName(tc.input)
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedTool, tool)
				require.Equal(t, tc.expectedBackend, backend)
			}
		})
	}
}

func Test_downstreamResourceURI(t *testing.T) {
	t.Run("downstream resource", func(t *testing.T) {
		downstream := downstreamResourceURI("file:///tmp/file.txt", "local")
		require.Equal(t, "local+file:///tmp/file.txt", downstream)
		// Verify it is a valid URI
		parsed, err := url.Parse(downstream)
		require.NoError(t, err)
		require.Equal(t, "local+file", parsed.Scheme)
		require.Equal(t, "/tmp/file.txt", parsed.Path)
	})

	t.Run("downstream resource template", func(t *testing.T) {
		downstream := downstreamResourceURI("file:///tmp/{file}", "local")
		require.Equal(t, "local+file:///tmp/{file}", downstream)
		// Verify it is a valid URI
		parsed, err := url.Parse(downstream)
		require.NoError(t, err)
		require.Equal(t, "local+file", parsed.Scheme)
		require.Equal(t, "/tmp/{file}", parsed.Path)
	})
}

func Test_upstreamResourceURI(t *testing.T) {
	cases := []struct {
		input           string
		expectedURI     string
		expectedBackend string
		expectedErr     string
	}{
		{
			input:           "local+file:///tmp/file.txt",
			expectedBackend: "local",
			expectedURI:     "file:///tmp/file.txt",
		},
		{
			input:           "local+file+test:///tmp/file.txt",
			expectedBackend: "local",
			expectedURI:     "file+test:///tmp/file.txt",
		},
		{
			input:           "local+file:///tmp/{file}", // Verify we can decode resource templates
			expectedBackend: "local",
			expectedURI:     "file:///tmp/{file}",
		},
		{
			input:       "file:///tmp/file.txt",
			expectedErr: "invalid resource URI: file:///tmp/file.txt",
		},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			backend, uri, err := upstreamResourceURI(tc.input)
			if tc.expectedErr != "" {
				require.ErrorContains(t, err, tc.expectedErr)
			} else {
				require.NoError(t, err)
				require.Equal(t, tc.expectedURI, uri)
				require.Equal(t, tc.expectedBackend, backend)
			}
		})
	}
}

func TestExtractSubject(t *testing.T) {
	tests := []struct {
		name  string
		token string
		want  string
	}{
		{
			name:  "unsigned token with principal",
			token: "eyJhbGciOiJub25lIiwidHlwIjoiSldUIn0.eyJzdWIiOiJtY3AifQ.",
			want:  "mcp",
		},
		{
			name:  "signed token with principal",
			token: "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJtY3AifQ.3FpuVHQFtGubZnErnKK6RULYffuZmtgmS3g8D8z8ykM",
			want:  "mcp",
		},
		{
			name:  "invalid token",
			token: "invalid-token",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest("GET", "/mcp", nil)
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer "+tt.token)

			require.Equal(t, tt.want, extractSubject(req))
		})
	}

	t.Run("no auth header", func(t *testing.T) {
		req, err := http.NewRequest("GET", "/mcp", nil)
		require.NoError(t, err)
		require.Empty(t, extractSubject(req))
	})

	t.Run("invalid auth header", func(t *testing.T) {
		req, err := http.NewRequest("GET", "/mcp", nil)
		req.Header.Set("Authorization", "basic foobar")
		require.NoError(t, err)
		require.Empty(t, extractSubject(req))
	})
}

func secureID(t *testing.T, proxy *MCPProxy, sessionID string) string {
	secure, err := proxy.sessionCrypto.Encrypt(sessionID)
	require.NoError(t, err)
	return secure
}

func TestMCPProxy_handleCompletionComplete(t *testing.T) {
	reqID, _ := jsonrpc.MakeID("id")

	proxy := newTestMCPProxy()
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		reqRaw, err := jsonrpc.DecodeMessage(body)
		require.NoError(t, err)
		req, ok := reqRaw.(*jsonrpc.Request)
		require.True(t, ok)

		// Verify method and params.
		require.Equal(t, "completion/complete", req.Method)
		var params mcp.CompleteParams
		require.NoError(t, json.Unmarshal(req.Params, &params))
		require.NotNil(t, params.Ref)
		if params.Ref.Name != "" {
			require.Equal(t, "my-prompt", params.Ref.Name)
		} else {
			require.Equal(t, "file://my-uri", params.Ref.URI)
		}
		// Respond with a valid completion response.
		resp := &jsonrpc.Response{ID: reqID}
		resp.Result, _ = json.Marshal(&mcp.CompleteResult{})
		respBody, err := jsonrpc.EncodeMessage(resp)
		require.NoError(t, err)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBody)
	}))
	t.Cleanup(testServer.Close)

	proxy.backendListenerAddr = testServer.URL

	for _, tc := range []struct {
		param  *mcp.CompleteParams
		errMsg string
	}{
		{
			param: &mcp.CompleteParams{Ref: &mcp.CompleteReference{
				Type: "ref/prompt",
				Name: "backend1__my-prompt",
			}},
		},
		{
			param: &mcp.CompleteParams{
				Ref: &mcp.CompleteReference{
					Type: "ref/resource",
					URI:  "backend1+file://my-uri",
				},
			},
		},
	} {
		rr := httptest.NewRecorder()
		err := proxy.handleCompletionComplete(t.Context(), &session{
			proxy: proxy,
			perBackendSessions: map[filterapi.MCPBackendName]*compositeSessionEntry{
				"backend1": {sessionID: "test-session"},
			},
			route: "test-route",
		}, rr, &jsonrpc.Request{ID: reqID, Method: "completion/complete"}, &mcp.CompleteParams{Ref: tc.param.Ref}, nil)
		require.NoError(t, err)

		require.Equal(t, http.StatusOK, rr.Code)
		require.JSONEq(t, `{"jsonrpc":"2.0","id":"id","result":{"completion":{"values":null}}}`, rr.Body.String())
	}
}

func TestMCPProxy_handlePing(t *testing.T) {
	reqID, _ := jsonrpc.MakeID("id")

	proxy := newTestMCPProxy()
	rr := httptest.NewRecorder()
	err := proxy.handlePing(t.Context(), rr, &jsonrpc.Request{ID: reqID, Method: "ping"})
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, rr.Code)
	require.JSONEq(t, `{"jsonrpc":"2.0","id":"id","result":{}}`, rr.Body.String())
}

func TestMCPPRoxy_handleSetLoggingLevel(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"id","result":{}}`))
	}))
	t.Cleanup(testServer.Close)

	reqID, _ := jsonrpc.MakeID("id")

	proxy := newTestMCPProxy()
	proxy.backendListenerAddr = testServer.URL
	rr := httptest.NewRecorder()
	s := &session{
		proxy: proxy,
		perBackendSessions: map[filterapi.MCPBackendName]*compositeSessionEntry{
			"backend": {sessionID: "test-session"},
		},
	}
	err := proxy.handleSetLoggingLevel(t.Context(), s, rr, &jsonrpc.Request{ID: reqID, Method: "logging/setLevel"}, &mcp.SetLoggingLevelParams{}, nil)
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `data: {"jsonrpc":"2.0","id":"id","result":{}}`)
}

func TestMCPPRoxy_handleResourceReadRequest(t *testing.T) {
	t.Run("invalid resource name", func(t *testing.T) {
		proxy := newTestMCPProxy()
		rr := httptest.NewRecorder()
		err := proxy.handleResourceReadRequest(t.Context(), nil, rr,
			&jsonrpc.Request{Method: "resources/subscribe"}, &mcp.ReadResourceParams{
				URI: "invalid-form",
			},
		)
		require.ErrorContains(t, err, "invalid resource URI: invalid-form")
	})

	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "backend1", r.Header.Get(internalapi.MCPBackendHeader))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.Contains(t, string(body), `"uri":"file://foo-resource"`)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"id","result":{}}`))
	}))

	t.Cleanup(testServer.Close)

	reqID, _ := jsonrpc.MakeID("id")
	proxy := newTestMCPProxy()
	proxy.backendListenerAddr = testServer.URL
	rr := httptest.NewRecorder()
	s := &session{
		proxy:              proxy,
		perBackendSessions: map[filterapi.MCPBackendName]*compositeSessionEntry{"backend1": {sessionID: "test-session"}},
		route:              "test-route",
	}
	err := proxy.handleResourceReadRequest(t.Context(), s, rr, &jsonrpc.Request{ID: reqID, Method: "resources/read"}, &mcp.ReadResourceParams{
		URI: downstreamResourceURI("file://foo-resource", "backend1"),
	})
	require.NoError(t, err)

	require.Equal(t, http.StatusOK, rr.Code)
	require.Contains(t, rr.Body.String(), `{"jsonrpc":"2.0","id":"id","result":{"contents":[]}}`)
}

func TestMCPProxy_maybeUpdateProgressTokenMetadata(t *testing.T) {
	proxy := newTestMCPProxy()
	metadata := mcp.Meta{}
	require.False(t, proxy.maybeUpdateProgressTokenMetadata(t.Context(), metadata, "backend"))
	metadata[progressTokenMetadataKey] = struct{}{}
	require.False(t, proxy.maybeUpdateProgressTokenMetadata(t.Context(), metadata, "backend"))
	metadata[progressTokenMetadataKey] = nil
	require.False(t, proxy.maybeUpdateProgressTokenMetadata(t.Context(), metadata, "backend"))

	metadata[progressTokenMetadataKey] = "abcd"
	require.True(t, proxy.maybeUpdateProgressTokenMetadata(t.Context(), metadata, "backend"))
	// Base64 encoded "abcd" is "YWJjZA==".
	require.Equal(t, "YWJjZA==__s__backend", metadata[progressTokenMetadataKey])

	metadata[progressTokenMetadataKey] = 1.1
	require.True(t, proxy.maybeUpdateProgressTokenMetadata(t.Context(), metadata, "backend"))
	require.Equal(t, "9a9999999999f13f__f__backend", metadata[progressTokenMetadataKey])

	metadata[progressTokenMetadataKey] = int64(1)
	require.True(t, proxy.maybeUpdateProgressTokenMetadata(t.Context(), metadata, "backend"))
	require.Equal(t, "1__i__backend", metadata[progressTokenMetadataKey])
}

func TestMCPProxy_handleClientToServerNotificationsProgress(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		inputProgressToken                any
		expResponseBody, expUpstreamToken string
	}{
		{
			name:               "invalid type",
			inputProgressToken: struct{}{},
			expResponseBody:    `invalid progressToken type struct {}`,
		},
		{
			name:               "invalid format",
			inputProgressToken: "@@@@@@@@@@@@@@",
			expResponseBody:    `invalid progressToken @@@@@@@@@@@@@@`,
		},
		{
			name:               "string type",
			inputProgressToken: "YWJjZA==__s__backend1", // base64 encoded "abcd".
			expUpstreamToken:   `"progressToken":"abcd"`,
		},
		{
			name:               "float64 type",
			inputProgressToken: "9a9999999999f13f__f__backend1",
			expUpstreamToken:   `"progressToken":1.1`,
		},
		{
			name:               "int64 type",
			inputProgressToken: "12345__i__backend1",
			expUpstreamToken:   `"progressToken":12345`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := newTestMCPProxy()
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				require.Contains(t, string(body), tc.expUpstreamToken)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"id","result":{}}`))
			}))
			t.Cleanup(testServer.Close)
			proxy.backendListenerAddr = testServer.URL

			rr := httptest.NewRecorder()
			s := &session{
				proxy:              proxy,
				perBackendSessions: map[filterapi.MCPBackendName]*compositeSessionEntry{"backend1": {sessionID: "test-session"}},
				route:              "test-route",
			}
			params := &mcp.ProgressNotificationParams{ProgressToken: tc.inputProgressToken}
			err := proxy.handleClientToServerNotificationsProgress(t.Context(), s, rr,
				&jsonrpc.Request{Method: "notifications/progress"}, params, nil)
			if rr.Code != http.StatusOK {
				require.Error(t, err)
				require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
				t.Logf("Response body: %s", rr.Body.String())
				require.Contains(t, rr.Body.String(), tc.expResponseBody)
				return
			}
			require.NoError(t, err)
			require.Contains(t, rr.Body.String(), tc.expResponseBody)
		})
	}
}

func TestMCPProxy_maybeServerToClientRequestModify(t *testing.T) {
	strID, err := jsonrpc.MakeID("id")
	require.NoError(t, err)
	f64ID, err := jsonrpc.MakeID(float64(1))
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		msg    *jsonrpc.Request
		expErr string
		verify func(t *testing.T, modified *jsonrpc.Request)
	}{
		{
			name:   "not server-to-client request",
			msg:    &jsonrpc.Request{Method: "ping"},
			verify: func(t *testing.T, modified *jsonrpc.Request) { require.Equal(t, "ping", modified.Method) },
		},
		{
			name:   "roots/list invalid param",
			msg:    &jsonrpc.Request{Method: "roots/list", Params: json.RawMessage(`fewfwaf`)},
			expErr: `failed to unmarshal roots/list params:`,
		},
		{
			name:   "roots/list no id",
			msg:    &jsonrpc.Request{Method: "roots/list", Params: json.RawMessage(`{"_meta": {"progressToken": 1345}}`)},
			expErr: `missing id in the server->client request`,
		},
		{
			name: "roots/list",
			msg:  &jsonrpc.Request{ID: strID, Method: "roots/list", Params: json.RawMessage(`{"_meta": {"progressToken": 1345}}`)},
			verify: func(t *testing.T, modified *jsonrpc.Request) {
				params := &mcp.ListRootsParams{}
				require.NoError(t, json.Unmarshal(modified.Params, params))
				// Check the progress token is updated.
				require.Equal(t, "0000000000049540__f__backend", params.Meta[progressTokenMetadataKey])
				// Then check the ID: aWQ= is the base64 encoded "id".
				require.Equal(t, "aWQ=__s__backend", modified.ID.Raw().(string))
			},
		},
		{
			name: "sampling/createMessage",
			msg:  &jsonrpc.Request{ID: f64ID, Method: "sampling/createMessage", Params: json.RawMessage(`{"_meta": {"progressToken": "pt"}}`)},
			verify: func(t *testing.T, modified *jsonrpc.Request) {
				params := &mcp.CreateMessageParams{}
				require.NoError(t, json.Unmarshal(modified.Params, params))
				// Check the progress token is updated: cHQ= is the base64 encoded "pt".
				require.Equal(t, "cHQ=__s__backend", params.Meta[progressTokenMetadataKey])
				// Then check the ID: 1 is encoded as 1__i__backend because of the roundtrip issue of the jsonrpc library in MCP SDK.
				// https://github.com/modelcontextprotocol/go-sdk/blob/5d64d61974982512270b554afd45d053c6dc2fb7/internal/jsonrpc2/messages.go#L32
				require.Equal(t, "1__i__backend", modified.ID.Raw().(string))
			},
		},
		{
			name: "elicitation/create",
			msg:  &jsonrpc.Request{ID: f64ID, Method: "elicitation/create", Params: json.RawMessage(`{"_meta": {"progressToken": "pt"}}`)},
			verify: func(t *testing.T, modified *jsonrpc.Request) {
				params := &mcp.CreateMessageParams{}
				require.NoError(t, json.Unmarshal(modified.Params, params))
				// Check the progress token is updated: cHQ= is the base64 encoded "pt".
				require.Equal(t, "cHQ=__s__backend", params.Meta[progressTokenMetadataKey])
				// Then check the ID: 1 is encoded as 1__i__backend because of the roundtrip issue of the jsonrpc library in MCP SDK.
				// https://github.com/modelcontextprotocol/go-sdk/blob/5d64d61974982512270b554afd45d053c6dc2fb7/internal/jsonrpc2/messages.go#L32
				require.Equal(t, "1__i__backend", modified.ID.Raw().(string))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := newTestMCPProxy()
			err := proxy.maybeServerToClientRequestModify(t.Context(), tc.msg, "backend")
			if tc.expErr != "" {
				require.ErrorContains(t, err, tc.expErr)
			} else {
				require.NoError(t, err)
				tc.verify(t, tc.msg)
			}
		})
	}
}

func TestMCPProxy_handleClientToServerResponse(t *testing.T) {
	t.Run("invalid IDs", func(t *testing.T) {
		proxy := newTestMCPProxy()
		rr := httptest.NewRecorder()
		err := proxy.handleClientToServerResponse(t.Context(), nil, rr, &jsonrpc.Response{})
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, rr.Code)
		require.Contains(t, rr.Body.String(), "invalid response ID type: <nil>")

		invalidID, err := jsonrpc.MakeID("invalidformatid")
		require.NoError(t, err)
		rr = httptest.NewRecorder()
		err = proxy.handleClientToServerResponse(t.Context(), nil, rr, &jsonrpc.Response{ID: invalidID})
		require.Error(t, err)
		require.Equal(t, http.StatusBadRequest, rr.Code)
		require.Contains(t, rr.Body.String(), "invalid response ID format: invalidformatid")

		invalidID2, err := jsonrpc.MakeID("__foo__")
		require.NoError(t, err)
		rr = httptest.NewRecorder()
		err = proxy.handleClientToServerResponse(t.Context(), nil, rr, &jsonrpc.Response{ID: invalidID2})
		require.ErrorContains(t, err, `invalid response ID type identifier: foo`)
		require.Equal(t, http.StatusBadRequest, rr.Code)
		require.Contains(t, rr.Body.String(), `invalid response ID type identifier`)
	})

	unknownBackendID, err := jsonrpc.MakeID("aWQ=__s__unknownbackend") // aWQK is the base64 encoded "id".
	require.NoError(t, err)
	intID, err := jsonrpc.MakeID("1__i__backend1")
	require.NoError(t, err)
	strID, err := jsonrpc.MakeID("aWQ=__s__backend1") // aWQK is the base64 encoded "id".
	require.NoError(t, err)
	f64ID, err := jsonrpc.MakeID("9a9999999999f13f__f__backend1")
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		msg    *jsonrpc.Response
		expErr string
		verify func(t *testing.T, modified *jsonrpc.Response)
	}{
		{
			name:   "no backend",
			msg:    &jsonrpc.Response{ID: unknownBackendID},
			expErr: `no MCP session found for backend unknownbackend`,
		},
		{
			name: "str id",
			msg:  &jsonrpc.Response{ID: strID},
			verify: func(t *testing.T, modified *jsonrpc.Response) {
				// Check the ID is decoded properly.
				require.Equal(t, "id", modified.ID.Raw().(string))
			},
		},
		{
			name: "int id",
			msg:  &jsonrpc.Response{ID: intID},
			verify: func(t *testing.T, modified *jsonrpc.Response) {
				// Check the ID is decoded properly.
				require.Equal(t, int64(1), modified.ID.Raw().(int64))
			},
		},
		{
			name: "float id",
			msg:  &jsonrpc.Response{ID: f64ID},
			verify: func(t *testing.T, modified *jsonrpc.Response) {
				// MCP SDK ignores the fraction part and converts to int64 during the roundtrip.
				// https://github.com/modelcontextprotocol/go-sdk/blob/5d64d61974982512270b554afd45d053c6dc2fb7/internal/jsonrpc2/messages.go#L32
				require.Equal(t, int64(1), modified.ID.Raw().(int64))
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				// Parse the body as the jsonrpc message.
				reqRaw, err := jsonrpc.DecodeMessage(body)
				require.NoError(t, err)
				req, ok := reqRaw.(*jsonrpc.Response)
				require.True(t, ok)
				tc.verify(t, req)

				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"id","result":{}}`))
			}))
			t.Cleanup(testServer.Close)
			proxy := newTestMCPProxy()
			proxy.backendListenerAddr = testServer.URL

			rr := httptest.NewRecorder()
			err := proxy.handleClientToServerResponse(t.Context(), &session{
				proxy:              proxy,
				perBackendSessions: map[filterapi.MCPBackendName]*compositeSessionEntry{"backend1": {sessionID: "test-session"}},
				route:              "test-route",
			}, rr, tc.msg)
			if tc.expErr != "" {
				require.ErrorContains(t, err, tc.expErr)
				require.Equal(t, http.StatusBadRequest, rr.Code)
				require.Contains(t, rr.Body.String(), tc.expErr)
				return
			}
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, rr.Code)
			require.Contains(t, rr.Body.String(), `{"jsonrpc":"2.0","id":"id","result":{}}`)
		})
	}
}

func TestMCPServer_handleNotificationsRootsListChanged(t *testing.T) {
	reqID, err := jsonrpc.MakeID("id")
	require.NoError(t, err)

	proxy := newTestMCPProxy()
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(testServer.Close)

	proxy.backendListenerAddr = testServer.URL
	proxy.routes = map[filterapi.MCPRouteName]*mcpProxyConfigRoute{
		"some-route": {
			backends: map[filterapi.MCPBackendName]filterapi.MCPBackend{
				"test-backend": {Name: "test-backend"},
			},
		},
	}

	req := &jsonrpc.Request{ID: reqID, Method: "notifications/roots/list_changed", Params: emptyJSONRPCMessage}
	rr := httptest.NewRecorder()
	err = proxy.handleNotificationsRootsListChanged(t.Context(), &session{
		proxy:              proxy,
		perBackendSessions: map[filterapi.MCPBackendName]*compositeSessionEntry{"test-backend": {sessionID: ""}},
	}, rr, req, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, rr.Code)
}

func TestMCPServer_handleResourcesSubscriptionRequest(t *testing.T) {
	reqID, err := jsonrpc.MakeID("id")
	require.NoError(t, err)

	for _, tc := range []struct {
		p    any
		name string
	}{
		{p: &mcp.SubscribeParams{URI: "backend1+file://foo"}, name: "resources/subscribe"},
		{p: &mcp.UnsubscribeParams{URI: "backend1+file://bar"}, name: "resources/unsubscribe"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			proxy := newTestMCPProxy()
			testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body []byte
				body, err = io.ReadAll(r.Body)
				require.NoError(t, err)
				var decoded jsonrpc.Message
				decoded, err = jsonrpc.DecodeMessage(body)
				require.NoError(t, err)
				req, ok := decoded.(*jsonrpc.Request)
				require.True(t, ok)
				require.Equal(t, tc.name, req.Method)
				switch tc.p.(type) {
				case *mcp.SubscribeParams:
					var params mcp.SubscribeParams
					require.NoError(t, json.Unmarshal(req.Params, &params))
					require.Equal(t, "file://foo", params.URI)
				case *mcp.UnsubscribeParams:
					var params mcp.UnsubscribeParams
					require.NoError(t, json.Unmarshal(req.Params, &params))
					require.Equal(t, "file://bar", params.URI)
				default:
					t.Fatalf("unexpected params type: %T", tc.p)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":"id","result":{}}`))
			}))
			t.Cleanup(testServer.Close)

			proxy.backendListenerAddr = testServer.URL

			req := &jsonrpc.Request{ID: reqID, Method: tc.name, Params: emptyJSONRPCMessage}
			rr := httptest.NewRecorder()
			s := &session{
				proxy:              proxy,
				perBackendSessions: map[filterapi.MCPBackendName]*compositeSessionEntry{"backend1": {sessionID: "a"}},
				route:              "test-route",
			}
			switch pp := tc.p.(type) {
			case *mcp.SubscribeParams:
				err = proxy.handleResourcesSubscribeRequest(t.Context(), s, rr, req, pp, nil)
			case *mcp.UnsubscribeParams:
				err = proxy.handleResourcesUnsubscribeRequest(t.Context(), s, rr, req, pp, nil)
			}
			require.NoError(t, err)
			require.Equal(t, http.StatusOK, rr.Code)
		})
	}
}

func Test_sendToAllBackendsAndAggregateResponsesImpl(t *testing.T) {
	reqID, err := jsonrpc.MakeID("id")
	require.NoError(t, err)
	proxy := newTestMCPProxy()
	s := &session{perBackendSessions: map[filterapi.MCPBackendName]*compositeSessionEntry{"a": {sessionID: "session-a"}}}

	type testData struct {
		Value string `json:"value"`
	}
	events := make(chan *sseEvent)
	go func() {
		for _, msg := range []jsonrpc.Message{
			&jsonrpc.Response{ID: reqID, Result: json.RawMessage(`{"value": "foo"}`)},
			&jsonrpc.Request{Method: "notifications/roots/list_changed"},
			// Empty result should be ignored.
			&jsonrpc.Response{ID: reqID},
			&jsonrpc.Response{ID: reqID, Result: json.RawMessage(`{"value": "bar"}`)},
			// Invalid result should be logged and ignored, not blocking the response.
			&jsonrpc.Response{ID: reqID, Result: json.RawMessage(`invalidddddddddddddddddd`)},
			// Error should be logged and ignored, not blocking the response.
			&jsonrpc.Response{ID: reqID, Error: errors.New("some error")},
		} {
			events <- &sseEvent{backend: "a", messages: []jsonrpc.Message{msg}}
		}
		close(events)
	}()

	rr := httptest.NewRecorder()
	err = sendToAllBackendsAndAggregateResponsesImpl(t.Context(), events, proxy, rr, s, &jsonrpc.Request{ID: reqID, Method: "test"},
		func(_ *session, res []broadCastResponse[testData]) testData {
			var combined testData
			for _, r := range res {
				combined.Value += r.res.Value
			}
			return combined
		},
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rr.Code)
	// The response is the SSE stream containing the aggregated result.
	require.Equal(t, "text/event-stream", rr.Header().Get("Content-Type"))
	require.Contains(t, rr.Body.String(), `{"jsonrpc":"2.0","id":"id","result":{"value":"foobar"}}`)
}

func Test_parseParamsAndMaybeStartSpan(t *testing.T) {
	params := &mcp.GetPromptParams{Name: "somebackend__test-prompt"}
	paramsData, err := json.Marshal(params)
	require.NoError(t, err)
	req := &jsonrpc.Request{
		Method: "prompts/get",
		Params: paramsData,
	}
	p := &mcp.GetPromptParams{}
	m := newTestMCPProxy()
	t.Setenv("OTEL_TRACES_EXPORTER", "console")
	trace, err := tracing.NewTracingFromEnv(t.Context(), t.Output(), nil)
	require.NoError(t, err)
	m.tracer = trace.MCPTracer()
	s, err := parseParamsAndMaybeStartSpan(t.Context(), m, req, p, nil)
	require.NoError(t, err)
	require.NotNil(t, s)
	// Make sure that traceparent is not empty, that's span started.
	require.NotEmpty(t, p.GetMeta()["traceparent"])
}

func Test_parseParamsAndMaybeStartSpan_NilParam(t *testing.T) {
	req := &jsonrpc.Request{
		Method: "prompts/get",
	}
	p := &mcp.GetPromptParams{}
	m := newTestMCPProxy()
	s, err := parseParamsAndMaybeStartSpan(t.Context(), m, req, p, nil)
	require.NoError(t, err)
	require.Nil(t, s)
}
