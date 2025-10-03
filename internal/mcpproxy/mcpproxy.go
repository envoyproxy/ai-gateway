// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

type (
	// MCPProxy serves /mcp endpoint.
	//
	// This implements [extproc.ConfigReceiver] to gets the up-to-date configuration.
	MCPProxy struct {
		*mcpProxyConfig
		metrics       metrics.MCPMetrics
		l             *slog.Logger
		sessionCrypto SessionCrypto
		tracer        tracing.MCPTracer
	}

	mcpProxyConfig struct {
		backendListenerAddr string
		routes              map[filterapi.MCPRouteName]*mcpProxyConfigRoute // route name -> backends of that route.
	}

	mcpProxyConfigRoute struct {
		backends      map[filterapi.MCPBackendName]filterapi.MCPBackend
		toolSelectors map[filterapi.MCPBackendName]*nameSelector
	}

	nameSelector struct {
		include        map[string]struct{}
		includeRegexps []*regexp.Regexp
	}
)

func (f *nameSelector) allows(tool string) bool {
	// Only one of include/includeRegex can be set.
	switch {
	case len(f.include) > 0:
		_, ok := f.include[tool]
		return ok
	case len(f.includeRegexps) > 0:
		for _, re := range f.includeRegexps {
			if re.MatchString(tool) {
				return true
			}
		}
		return false
	default:
		return true // No filter, allow all.
	}
}

// NewMCPProxy creates a new MCPProxy instance.
func NewMCPProxy(l *slog.Logger, mcpMetrics metrics.MCPMetrics, tracer tracing.MCPTracer, sessionCrypto SessionCrypto) (*MCPProxy, *http.ServeMux, error) {
	p := &MCPProxy{l: l, metrics: mcpMetrics, tracer: tracer, sessionCrypto: sessionCrypto}
	mux := http.NewServeMux()
	mux.HandleFunc(
		// Must match all paths since the route selection happens at Envoy level and the "route" header is already
		// set when it reaches here. We use that to select the appropriate backends, so we don't need to have different paths here.
		//
		// For example, if we mistakenly set /mcp here, only the route with prefix /mcp will be matched, and other routes
		// with different prefixes will not be matched, which is not what we want.
		"/", func(w http.ResponseWriter, r *http.Request) {
			switch r.Method {
			case http.MethodGet:
				p.serveGET(w, r)
			case http.MethodPost:
				p.servePOST(w, r)
			case http.MethodDelete:
				p.serverDELETE(w, r)
			default:
				http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			}
		})
	return p, mux, nil
}

// LoadConfig implements [extproc.ConfigReceiver.LoadConfig] which will be called
// when the configuration is updated on the file system.
func (m *MCPProxy) LoadConfig(_ context.Context, config *filterapi.Config) error {
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
			toolSelectors: make(map[filterapi.MCPBackendName]*nameSelector, len(route.Backends)),
		}
		for _, backend := range route.Backends {
			r.backends[backend.Name] = backend
			if s := backend.ToolSelector; s != nil {
				ns := &nameSelector{
					include: make(map[string]struct{}),
				}
				for _, tool := range s.Include {
					ns.include[tool] = struct{}{}
				}
				for _, expr := range s.IncludeRegex {
					re, err := regexp.Compile(expr)
					if err != nil {
						return fmt.Errorf("failed to compile include regex %q for backend %q in route %q: %w", expr, backend.Name, route.Name, err)
					}
					ns.includeRegexps = append(ns.includeRegexps, re)
				}
				r.toolSelectors[backend.Name] = ns
			}
		}
		newConfig.routes[route.Name] = r
	}

	m.mcpProxyConfig = newConfig // This is racy, but we don't care.
	return nil
}

// newSession creates a new session for a downstream client.
// It multiplexes the initialize request to all backends defined in the MCPRoute associated with the downstream request.
func (m *MCPProxy) newSession(ctx context.Context, p *mcp.InitializeParams, routeName filterapi.MCPRouteName, subject string, span tracing.MCPSpan) (*session, error) {
	m.l.Debug("creating new MCP session")

	var (
		wg      sync.WaitGroup
		entries []compositeSessionEntry
		counter int
	)

	backends := m.routes[routeName]
	if backends == nil {
		return nil, fmt.Errorf("no backends found for route %s", routeName)
	}
	entries = make([]compositeSessionEntry, len(backends.backends))

	if m.l.Enabled(ctx, slog.LevelDebug) {
		m.l.Debug("initializing MCP sessions to backends", slog.String("route", routeName), slog.Any("backends", backends))
	}
	for _, backend := range backends.backends {
		entryIndex := counter
		counter++
		// Initialize sessions to all backends in parallel to reduce the overall latency of session creation.
		wg.Go(func() {
			if m.l.Enabled(ctx, slog.LevelDebug) {
				m.l.Debug("creating MCP session", slog.String("backend", backend.Name))
			}
			startAt := time.Now()
			initResult, err := m.initializeSession(ctx, routeName, backend, p)
			if err != nil {
				m.l.Error("failed to create MCP session", slog.String("backend", backend.Name), slog.String("error", err.Error()))
				// If one backend fails, don't fail the overall connection. Create a session to the rest of the backends, as they
				// may provide the needed methods.
				// TODO: should we record a metric for this?
				return
			}
			m.metrics.RecordInitializationDuration(ctx, &startAt)
			if m.l.Enabled(ctx, slog.LevelDebug) {
				m.l.Debug("created MCP session", slog.String("backend", backend.Name), slog.String("session_id", string(initResult.sessionID)))
			}
			if span != nil {
				span.RecordRouteToBackend(backend.Name, string(initResult.sessionID), true)
			}
			entries[entryIndex] = compositeSessionEntry{sessionID: initResult.sessionID, backendName: backend.Name}
		})
	}
	wg.Wait()
	// Remove empty entries (failed initializations).
	finalEntries := make([]compositeSessionEntry, 0, len(entries))
	for _, e := range entries {
		// When backendName is empty, it means the initialization failed.
		// As per the comment above, we just skip the failed backends.
		if len(e.backendName) > 0 {
			finalEntries = append(finalEntries, e)
		}
	}
	if len(finalEntries) == 0 {
		// All initializations failed, which means we cannot provide any meaningful operation so we fail the session creation.
		return nil, errors.New("failed to create MCP session to any backend")
	}

	encrypted, err := m.sessionCrypto.Encrypt(string(clientToGatewaySessionIDFromEntries(subject, finalEntries, routeName)))
	if err != nil {
		return nil, fmt.Errorf("failed to encrypt session ID: %w", err)
	}
	return &session{proxy: m, id: secureClientToGatewaySessionID(encrypted)}, nil
}

// sessionFromID returns the session with the given ID, or error if not found or invalid.
func (m *MCPProxy) sessionFromID(id secureClientToGatewaySessionID, lastEvent secureClientToGatewayEventID) (*session, error) {
	decrypted, err := m.sessionCrypto.Decrypt(string(id))
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt session ID: %w", err)
	}

	perBackendSessionIDs, route, err := clientToGatewaySessionID(decrypted).backendSessionIDs()
	if err != nil {
		return nil, err
	}
	if len(lastEvent) != 0 {
		decryptedEventID, err := m.sessionCrypto.Decrypt(string(lastEvent))
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt last event ID: %w", err)
		}
		eventIDs := clientToGatewayEventID(decryptedEventID).backendEventIDs()
		for backend, eventID := range eventIDs {
			entity, ok := perBackendSessionIDs[backend]
			if ok {
				entity.lastEventID = eventID
			}
		}
	}

	return &session{id: id, route: route, proxy: m, perBackendSessions: perBackendSessionIDs}, nil
}

type initializeResult struct {
	sessionID gatewayToMCPServerSessionID
	result    *mcp.InitializeResult
}

func (m *MCPProxy) initializeSession(ctx context.Context, routeName filterapi.MCPRouteName, backend filterapi.MCPBackend, p *mcp.InitializeParams) (*initializeResult, error) {
	// Send the initialize request to the MCP backend listener.
	reqID := mustJSONRPCRequestID()
	var (
		sessionID  string
		initResult *mcp.InitializeResult
	)
	{
		// Scoping per each request to avoid leaking Close.
		initializeReq, err := json.Marshal(p)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal MCP initialize params: %w", err)
		}
		mcpReq := &jsonrpc.Request{Method: "initialize", Params: initializeReq, ID: reqID}
		resp, err := m.invokeJSONRPCRequest(ctx, routeName, backend, nil, mcpReq)
		if err != nil {
			return nil, fmt.Errorf("failed to send MCP initialize request: %w", err)
		}
		defer func() {
			_ = resp.Body.Close()
		}()
		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("MCP initialize request failed with status code %d and body=%s", resp.StatusCode, string(body))
		}
		if m.l.Enabled(ctx, slog.LevelDebug) {
			m.l.Debug("MCP initialize request succeeded",
				slog.String("backend", backend.Name),
				slog.String("content type", resp.Header.Get("Content-Type")),
			)
		}

		// Note: some servers are stateless hence no (==empty) session ID.
		sessionID = resp.Header.Get(sessionIDHeader)
		if m.l.Enabled(ctx, slog.LevelDebug) {
			m.l.Debug("initialized MCP session", slog.String("backend", backend.Name), slog.String("session_id", sessionID))
		}

		var rawMsg jsonrpc.Message
		switch resp.Header.Get("Content-Type") {
		case "text/event-stream":
			parser := newSSEEventParser(resp.Body, backend.Name)
			for {
				event, parseErr := parser.next()
				// TODO: handle reconnect. We need to re-arrange the event ID so that it will also contain the backend name and the original session ID.
				// 	Since event ID can be arbitrary string, we can shove each backend's last even ID into the event ID just like the session ID.
				if event != nil {
					// TODO: there's no session here what should we do?
					if len(event.messages) < 1 {
						return nil, errors.New("failed to get message from MCP sse event")
					}
					// Last event is the actual response.
					rawMsg = event.messages[len(event.messages)-1]
				}
				if parseErr != nil {
					if errors.Is(parseErr, io.EOF) || strings.Contains(parseErr.Error(), "context deadline exceeded") {
						break
					}
					m.l.Error("failed to read MCP GET response body", slog.String("error", parseErr.Error()))
					break
				}
			}
		default:
			// Handle JSON response.
			body, _ := io.ReadAll(resp.Body)
			// Decode the JSON-RPC message.
			rawMsg, err = jsonrpc.DecodeMessage(body)
			if err != nil {
				m.l.Warn("Failed to decode MCP message", slog.String("error", err.Error()))
				return nil, fmt.Errorf("failed to decode MCP message: %w", err)
			}
		}

		msg, ok := rawMsg.(*jsonrpc.Response)
		if !ok {
			m.l.Warn("MCP message is not a response", slog.String("type", fmt.Sprintf("%T", rawMsg)))
			return nil, fmt.Errorf("MCP message is not a response: %T", rawMsg)
		}
		// TODO: do we need to merge and return the result back?

		err = json.Unmarshal(msg.Result, &initResult)
		if err != nil {
			m.l.Warn("Failed to decode MCP initialize result", slog.String("error", err.Error()))
			return nil, fmt.Errorf("failed to decode MCP initialize result: %w", err)
		}
		if m.l.Enabled(ctx, slog.LevelDebug) {
			m.l.Debug("MCP session initialized", slog.Any("capabilities", initResult.Capabilities))
		}
		m.metrics.RecordServerCapabilities(ctx, initResult.Capabilities)
	}

	// Need to invoke "notifications/initialized" to complete the initialization.
	{
		// Send the notifications/initialized request to the MCP backend listener.
		mcpReq := &jsonrpc.Request{Method: "notifications/initialized", Params: json.RawMessage(`{}`)}
		resp, err := m.invokeJSONRPCRequest(ctx, routeName, backend, &compositeSessionEntry{
			sessionID: gatewayToMCPServerSessionID(sessionID),
		}, mcpReq)
		if err != nil {
			return nil, fmt.Errorf("failed to send MCP notifications/initialized request: %w", err)
		}
		defer func() {
			_ = resp.Body.Close()
		}()
		if resp.StatusCode != http.StatusAccepted {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("MCP notifications/initialized request failed with status code %d, body=%s", resp.StatusCode, string(body))
		}
	}
	if m.l.Enabled(ctx, slog.LevelDebug) {
		m.l.Debug("sent MCP notifications/initialized", slog.String("backend", backend.Name), slog.String("session_id", sessionID))
	}
	return &initializeResult{
		sessionID: gatewayToMCPServerSessionID(sessionID),
		result:    initResult,
	}, nil
}

func (m *MCPProxy) invokeJSONRPCRequest(ctx context.Context, routeName filterapi.MCPRouteName, backend filterapi.MCPBackend, cse *compositeSessionEntry, msg jsonrpc.Message) (*http.Response, error) {
	encoded, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return nil, fmt.Errorf("failed to encode MCP message: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.mcpEndpointForBackend(backend), bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("failed to create MCP notifications/initialized request: %w", err)
	}
	addMCPHeaders(req, msg, routeName, backend.Name)
	if cse != nil {
		if len(cse.sessionID) > 0 {
			req.Header.Set(sessionIDHeader, string(cse.sessionID))
		}
		if len(cse.lastEventID) > 0 {
			req.Header.Set(lastEventIDHeader, cse.lastEventID)
		}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	client := http.Client{Timeout: 10 * time.Second}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send MCP notifications/initialized request: %w", err)
	}
	return resp, nil
}

func (m *MCPProxy) getBackendForRoute(route, backend filterapi.MCPBackendName) (filterapi.MCPBackend, error) {
	r := m.routes[route]
	if r == nil {
		return filterapi.MCPBackend{}, fmt.Errorf("no route found for %q", route)
	}
	b, ok := r.backends[backend]
	if !ok {
		return filterapi.MCPBackend{}, fmt.Errorf("no backend found for %q in route %q", backend, route)
	}
	return b, nil
}

func mustJSONRPCRequestID() jsonrpc.ID {
	id, err := jsonrpc.MakeID(uuid.NewString())
	if err != nil {
		panic(err)
	}
	return id
}
