// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
//
//nolint:unused // TODO: remove this once full era dispatch wiring is enabled.
package mcpproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
)

// Caching hints applied to gateway-generated cacheable results (2026-07-28
// caching spec). Servers MUST include caching hints (ttlMs/cacheScope) on
// complete results for server/discover, tools/list, prompts/list,
// resources/list, resources/templates/list, and resources/read.
//
// TODO: come up with a proper multiplexing-aware caching strategy. When the
// gateway aggregates results from N backends, each backend can advertise its
// own ttlMs/cacheScope and there is no single correct way to fold them into one
// aggregated hint. For now, we mark TTL as 0 and cacheScope as private.
const (
	// defaultTTLMs marks aggregated results as immediately stale so clients
	// re-fetch every time. Safe default until a multiplexing-aware TTL exists.
	defaultTTLMs = 0

	// defaultCacheScope keeps aggregated results private so they are never
	// shared across authorization contexts.
	defaultCacheScope = "private"

	protocolVersion20251125 = "2025-11-25"
	protocolVersion20260728 = "2026-07-28"

	mcpMethodHeader          = "Mcp-Method"
	mcpNameHeader            = "Mcp-Name"
	mcpProtocolVersionHeader = "Mcp-Protocol-Version"

	// _meta key constants for per-request metadata.
	metaProtocolVersion    = "io.modelcontextprotocol/protocolVersion"
	metaClientInfo         = "io.modelcontextprotocol/clientInfo"
	metaClientCapabilities = "io.modelcontextprotocol/clientCapabilities"
)

// supportedVersions lists all protocol versions the gateway supports, newest first.
var supportedVersions = []string{protocolVersion20260728, protocolVersion20251125, protocolVersion20250618}

// serveModernPOST handles modern (2026-07-28) stateless POST requests.
// This is the Phase 1 entry point for modern clients talking to modern backends.
// The JSON-RPC request has already been parsed by servePOST.
//
// startAt is the time when the overall HTTP request started, used for recording
// request duration metrics.
func (m *mcpRequestContext) serveModernPOST(w http.ResponseWriter, r *http.Request, req *jsonrpc.Request, startAt time.Time) {
	var (
		ctx     = r.Context()
		err     error
		errType metrics.MCPErrorType
		result  handlerResult
	)

	// Record metrics and close the tracing span once the request completes,
	// mirroring the deferred bookkeeping on the legacy path (serveLegacyPOST).
	// Handlers that fan out to multiple backends (e.g. server/discover, *_list)
	// set perBackendMetricsRecorded to skip the generic per-backend recording
	// here and avoid double-counting.
	defer func() {
		if m.l.Enabled(ctx, slog.LevelDebug) {
			m.l.Debug("Completed modern MCP POST request",
				slog.String("method", req.Method),
				slog.String("error_type", string(errType)),
				slog.String("duration", time.Since(startAt).String()))
		}

		if m.perBackendMetricsRecorded {
			return
		}

		metricsInstance := m.metrics
		if result.backendName != "" {
			metricsInstance = m.metrics.WithBackend(result.backendName)
		}

		if err != nil {
			applicationError := false
			var errToolCall *errToolCall
			if errors.As(err, &errToolCall) {
				applicationError = true
			}
			if applicationError {
				metricsInstance.RecordMethodErrorCount(ctx, req.Method, nil, metrics.MCPStatusFailed)
			} else {
				metricsInstance.RecordMethodErrorCount(ctx, req.Method, nil, metrics.MCPStatusError)
			}
			metricsInstance.RecordRequestErrorDuration(ctx, startAt, errType, nil)
			return
		}

		metricsInstance.RecordRequestDuration(ctx, startAt, nil)
		metricsInstance.RecordMethodCount(ctx, req.Method, nil)
	}()

	route := r.Header.Get(internalapi.MCPRouteHeader)
	if route == "" {
		m.l.Error("missing route header on modern request")
		errType = metrics.MCPErrorInternal
		err = errors.New("missing route header")
		onErrorResponse(w, http.StatusInternalServerError, "missing route header")
		return
	}

	headerMethod := r.Header.Get(mcpMethodHeader)
	if headerMethod != req.Method {
		errType = metrics.MCPErrorInvalidJSONRPC
		err = fmt.Errorf("Mcp-Method header mismatch")
		onErrorResponse(w, http.StatusBadRequest,
			fmt.Sprintf("Mcp-Method header '%s' does not match body method '%s'", headerMethod, req.Method))
		return
	}

	headerVersion := r.Header.Get(mcpProtocolVersionHeader)
	if headerVersion == "" {
		headerVersion = protocolVersion20260728
	}
	if !isSupportedVersion(headerVersion) {
		errType = metrics.MCPErrorUnsupportedProtocolVersion
		err = fmt.Errorf("unsupported protocol version: %s", headerVersion)
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("unsupported protocol version: %s", headerVersion))
		return
	}

	switch req.Method {
	case "initialize", "notifications/initialized":
		errType = metrics.MCPErrorUnsupportedMethod
		err = fmt.Errorf("method removed in 2026-07-28: %s", req.Method)
		onErrorResponse(w, http.StatusNotFound, "method removed in 2026-07-28: use server/discover")
		return
	case "ping":
		errType = metrics.MCPErrorUnsupportedMethod
		err = errors.New("ping removed in 2026-07-28")
		onErrorResponse(w, http.StatusNotFound, "ping removed in 2026-07-28")
		return
	case "logging/setLevel":
		errType = metrics.MCPErrorUnsupportedMethod
		err = errors.New("logging/setLevel removed in 2026-07-28")
		onErrorResponse(w, http.StatusNotFound, "logging/setLevel removed in 2026-07-28; use _meta logLevel")
		return
	}

	// Start a tracing span for the request, mirroring the legacy path. The span
	// is closed in the deferred block below. Fan-out handlers additionally record
	// per-backend routing on the span via RecordRouteToBackend.
	var span tracingapi.MCPSpan
	if params := modernParamsForHeaderMetadata(req); params != nil {
		span = m.tracer.StartSpanAndInjectMeta(ctx, req, params, r.Header)
	}
	defer func() {
		if span == nil {
			return
		}
		if err != nil {
			span.EndSpanOnError(string(errType), err)
		} else {
			span.EndSpan()
		}
	}()

	// Dispatch based on method. Handlers return the resolved backend (when any)
	// and an error so the deferred block can attribute metrics correctly.
	switch req.Method {
	case "server/discover":
		result, err = m.handleServerDiscover(ctx, w, req, route, span)
	case "tools/list":
		result, err = m.handleModernToolsList(ctx, w, r, req, route)
	case "resources/list":
		result, err = m.handleModernResourcesList(ctx, w, r, req, route)
	case "resources/templates/list":
		result, err = m.handleModernResourceTemplatesList(ctx, w, r, req, route)
	case "prompts/list":
		result, err = m.handleModernPromptsList(ctx, w, r, req, route)
	default:
		errType = metrics.MCPErrorUnsupportedMethod
		err = fmt.Errorf("unknown method: %s", req.Method)
		onErrorResponse(w, http.StatusNotFound, fmt.Sprintf("unknown method: %s", req.Method))
		return
	}
	if errType == "" {
		errType = errorType(err)
	}
}

// handleServerDiscover fans out server/discover to all backends, merges results.
//
// This is a fan-out handler: it records per-backend metrics itself and sets
// perBackendMetricsRecorded so the generic recording in serveModernPOST is
// skipped.
func (m *mcpRequestContext) handleServerDiscover(ctx context.Context, w http.ResponseWriter, req *jsonrpc.Request, route filterapi.MCPRouteName, span tracingapi.MCPSpan) (handlerResult, error) {
	m.perBackendMetricsRecorded = true

	routeConfig, ok := m.routes[route]
	if !ok {
		onErrorResponse(w, http.StatusNotFound, "route not found")
		return handlerResult{}, fmt.Errorf("%w: %s", errBackendNotFound, route)
	}

	var results []*mcp.DiscoverResult
	for _, backend := range routeConfig.backends {
		backendStartAt := time.Now()
		result, err := m.discoverBackend(ctx, route, backend)
		backendMetrics := m.metrics.WithBackend(backend.Name)
		if err != nil {
			m.l.Warn("server/discover failed for backend",
				slog.String("backend", backend.Name),
				slog.String("error", err.Error()))
			backendMetrics.RecordMethodErrorCount(ctx, req.Method, nil, metrics.MCPStatusError)
			backendMetrics.RecordRequestErrorDuration(ctx, backendStartAt, errorType(err), nil)
			continue
		}
		if span != nil {
			span.RecordRouteToBackend(backend.Name, "", true)
		}
		backendMetrics.RecordMethodCount(ctx, req.Method, nil)
		backendMetrics.RecordRequestDuration(ctx, backendStartAt, nil)
		results = append(results, result)
	}
	if len(results) == 0 {
		m.l.Error("server/discover failed for all backends", slog.String("route", route))
		onErrorResponse(w, http.StatusInternalServerError, "failed to discover any backend")
		return handlerResult{}, errors.New("failed to discover any backend")
	}
	merged := mergeDiscoverResults(results)
	merged.Instructions = fmt.Sprintf("Envoy AI Gateway — MCP proxy aggregating %d backends", len(routeConfig.backends))
	merged.TTLMs = defaultTTLMs
	merged.CacheScope = defaultCacheScope
	writeJSONRPCResult(w, req.ID, merged)
	return handlerResult{}, nil
}

// discoverBackend sends a server/discover request to a single backend.
//
// TODO: this used to consult a per-instance capabilityCache to avoid a
// server/discover round-trip to every backend on every aggregated request. The
// cache was removed until we have a multiplexing-aware caching strategy (see the
// TODO on defaultTTLMs/defaultCacheScope in era.go), so every discover currently
// hits the backend live.
func (m *mcpRequestContext) discoverBackend(ctx context.Context, route filterapi.MCPRouteName, backend filterapi.MCPBackend) (*mcp.DiscoverResult, error) {
	id, _ := jsonrpc.MakeID(fmt.Sprintf("gw-discover-%s-%d", backend.Name, time.Now().UnixNano()))
	req := &jsonrpc.Request{
		ID:     id,
		Method: "server/discover",
		Params: discoverParams(),
	}

	resultRaw, err := m.sendModernRequest(ctx, req, route, backend)
	if err != nil {
		return nil, fmt.Errorf("server/discover request failed: %w", err)
	}

	var result mcp.DiscoverResult
	if err := json.Unmarshal(resultRaw, &result); err != nil {
		return nil, fmt.Errorf("unmarshal DiscoverResult: %w", err)
	}

	return &result, nil
}

func discoverParams() []byte {
	return []byte(`{"_meta":{` +
		`"` + metaProtocolVersion + `":"` + protocolVersion20260728 + `",` +
		`"` + metaClientInfo + `":{"name":"envoy-ai-gateway","version":"1.0.0"},` +
		`"` + metaClientCapabilities + `":{}` +
		`}}`)
}

// mergeDiscoverResults merges multiple DiscoverResult from route backends into
// a single DiscoverResult representing the gateway's aggregated capabilities.
//
// Capabilities are aggregated using the same union/OR semantics as the stateful
// initialize path
func mergeDiscoverResults(results []*mcp.DiscoverResult) *mcp.DiscoverResult {
	caps := make([]*mcp.ServerCapabilities, 0, len(results))
	for _, r := range results {
		caps = append(caps, r.Capabilities)
	}
	return &mcp.DiscoverResult{
		SupportedVersions: supportedVersions,
		Capabilities:      unionServerCapabilities(caps),
	}
}

// handleModernToolsList handles tools/list on the modern stateless path (P1.7).
//
// Fan-out handler: records per-backend metrics itself and sets
// perBackendMetricsRecorded.
func (m *mcpRequestContext) handleModernToolsList(ctx context.Context, w http.ResponseWriter, r *http.Request, req *jsonrpc.Request, route filterapi.MCPRouteName) (handlerResult, error) {
	m.perBackendMetricsRecorded = true

	routeConfig, ok := m.routes[route]
	if !ok {
		onErrorResponse(w, http.StatusNotFound, "route not found")
		return handlerResult{}, fmt.Errorf("%w: %s", errBackendNotFound, route)
	}

	// mergeToolsList reads per-caller headers from m.requestHeaders for authorization.
	// In production this is set at construction (== r.Header); ensure it is populated
	// even when this handler is invoked directly (e.g. in tests) so auth stays enforced.
	m.requestHeaders = r.Header

	responses := sendToAllModernBackendsAndAggregateResponses[mcp.ListToolsResult](ctx, m, req, route, routeConfig)
	result := m.mergeToolsList(&session{route: route}, responses)
	result.TTLMs = defaultTTLMs
	result.CacheScope = defaultCacheScope
	writeJSONRPCResult(w, req.ID, &result)
	return handlerResult{}, nil
}

// handleModernResourcesList handles resources/list (P1.7 fan-out).
//
// Fan-out handler: records per-backend metrics itself and sets
// perBackendMetricsRecorded.
func (m *mcpRequestContext) handleModernResourcesList(ctx context.Context, w http.ResponseWriter, _ *http.Request, req *jsonrpc.Request, route filterapi.MCPRouteName) (handlerResult, error) {
	m.perBackendMetricsRecorded = true

	routeConfig, ok := m.routes[route]
	if !ok {
		onErrorResponse(w, http.StatusNotFound, "route not found")
		return handlerResult{}, fmt.Errorf("%w: %s", errBackendNotFound, route)
	}

	responses := sendToAllModernBackendsAndAggregateResponses[mcp.ListResourcesResult](ctx, m, req, route, routeConfig)
	result := m.mergeResourceList(&session{route: route}, responses)
	result.TTLMs = defaultTTLMs
	result.CacheScope = defaultCacheScope
	writeJSONRPCResult(w, req.ID, &result)
	return handlerResult{}, nil
}

// handleModernResourceTemplatesList handles resources/templates/list (P1.7 fan-out).
// Fans out to all backends and namespaces each template's uriTemplate with the
// backend prefix, mirroring resources/list.
func (m *mcpRequestContext) handleModernResourceTemplatesList(ctx context.Context, w http.ResponseWriter, _ *http.Request, req *jsonrpc.Request, route filterapi.MCPRouteName) (handlerResult, error) {
	m.perBackendMetricsRecorded = true

	routeConfig, ok := m.routes[route]
	if !ok {
		onErrorResponse(w, http.StatusNotFound, "route not found")
		return handlerResult{}, fmt.Errorf("%w: %s", errBackendNotFound, route)
	}
	responses := sendToAllModernBackendsAndAggregateResponses[mcp.ListResourceTemplatesResult](ctx, m, req, route, routeConfig)
	result := m.mergeResourcesTemplateList(&session{route: route}, responses)
	result.TTLMs = defaultTTLMs
	result.CacheScope = defaultCacheScope
	writeJSONRPCResult(w, req.ID, &result)
	return handlerResult{}, nil
}

// handleModernPromptsList handles prompts/list (P1.7 fan-out).
//
// Fan-out handler: records per-backend metrics itself and sets
// perBackendMetricsRecorded.
func (m *mcpRequestContext) handleModernPromptsList(ctx context.Context, w http.ResponseWriter, _ *http.Request, req *jsonrpc.Request, route filterapi.MCPRouteName) (handlerResult, error) {
	m.perBackendMetricsRecorded = true

	routeConfig, ok := m.routes[route]
	if !ok {
		onErrorResponse(w, http.StatusNotFound, "route not found")
		return handlerResult{}, fmt.Errorf("%w: %s", errBackendNotFound, route)
	}

	responses := sendToAllModernBackendsAndAggregateResponses[mcp.ListPromptsResult](ctx, m, req, route, routeConfig)
	result := m.mergePromptsList(&session{route: route}, responses)
	result.TTLMs = defaultTTLMs
	result.CacheScope = defaultCacheScope
	writeJSONRPCResult(w, req.ID, &result)
	return handlerResult{}, nil
}

// sendToAllModernBackendsAndAggregateResponses fans out a modern (stateless) list request to all backends in
// the route and collects each backend's result unmarshaled into T. Backends that
// fail the request or whose result cannot be unmarshaled are logged and skipped,
// mirroring the "partial failure is non-fatal" behavior of the legacy aggregation
// path (sendToAllBackendsAndAggregateResponses).
//
// The returned []broadCastResponse[T] is intentionally shaped like the legacy
// aggregation input so the modern handlers can reuse the same merge* functions
// (mergeToolsList, mergeResourceList, ...) and avoid drifting from the legacy
// prefixing/filtering/authorization logic.
func sendToAllModernBackendsAndAggregateResponses[T any](ctx context.Context, m *mcpRequestContext, req *jsonrpc.Request, route filterapi.MCPRouteName, routeConfig *mcpProxyConfigRoute) []broadCastResponse[T] {
	responses := make([]broadCastResponse[T], 0, len(routeConfig.backends))
	for backendName, backend := range routeConfig.backends {
		backendStartAt := time.Now()
		backendMetrics := m.metrics.WithBackend(backendName)
		resp, err := m.sendModernRequest(ctx, req, route, backend)
		if err != nil {
			m.l.Warn("modern list request failed for backend",
				slog.String("method", req.Method),
				slog.String("backend", backendName),
				slog.String("error", err.Error()))
			backendMetrics.RecordMethodErrorCount(ctx, req.Method, nil, metrics.MCPStatusError)
			backendMetrics.RecordRequestErrorDuration(ctx, backendStartAt, errorType(err), nil)
			continue
		}
		var result T
		if err := json.Unmarshal(resp, &result); err != nil {
			m.l.Warn("failed to unmarshal modern list response from backend",
				slog.String("method", req.Method),
				slog.String("backend", backendName),
				slog.String("error", err.Error()))
			backendMetrics.RecordMethodErrorCount(ctx, req.Method, nil, metrics.MCPStatusError)
			backendMetrics.RecordRequestErrorDuration(ctx, backendStartAt, metrics.MCPErrorInternal, nil)
			continue
		}
		backendMetrics.RecordMethodCount(ctx, req.Method, nil)
		backendMetrics.RecordRequestDuration(ctx, backendStartAt, nil)
		responses = append(responses, broadCastResponse[T]{backendName: backendName, res: result})
	}
	return responses
}

// sendModernRequest sends a JSON-RPC request to a modern backend with proper headers (P1.6).
// Returns the raw result JSON. Handles both plain JSON and SSE response formats.
func (m *mcpRequestContext) sendModernRequest(ctx context.Context, req *jsonrpc.Request, route filterapi.MCPRouteName, backend filterapi.MCPBackend) (json.RawMessage, error) {
	body, err := jsonrpc.EncodeMessage(req)
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, m.backendListenerAddr, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	// Keep modern forwarding behavior aligned with the legacy proxy path:
	// backend/route metadata headers, optional log/header mappings, and original path.
	addMCPHeaders(httpReq, req, modernParamsForHeaderMetadata(req), route, backend.Name)
	m.applyLogHeaderMappings(httpReq, req)
	m.applyOriginalPathHeaders(httpReq)

	// P1.6: Set modern headers. No Mcp-Session-Id, no Last-Event-Id.
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream, application/json")
	httpReq.Header.Set(mcpProtocolVersionHeader, protocolVersion20260728)
	httpReq.Header.Set(mcpMethodHeader, req.Method)
	httpReq.Header.Set(internalapi.MCPBackendHeader, backend.Name)
	httpReq.Header.Set(internalapi.MCPRouteHeader, route)

	// Forward configured headers to backend.
	if routeConfig := m.routes[route]; routeConfig != nil {
		// Route-level headers (e.g. auth claimToHeaders).
		for _, header := range routeConfig.forwardHeaders {
			if value := m.requestHeaders.Get(header); value != "" {
				httpReq.Header.Set(header, value)
			}
		}
		// Per-backend headers (from MCPRouteBackendRef.forwardHeaders) with optional renaming.
		if b, ok := routeConfig.backends[backend.Name]; ok {
			for _, fh := range b.ForwardHeaders {
				if value := m.requestHeaders.Get(fh.Name); value != "" {
					httpReq.Header.Set(fh.ForwardName(), value)
				}
			}
		}
	}

	// Set Mcp-Name if applicable.
	if name := extractNameForMethod(req.Method, json.RawMessage(req.Params)); name != "" {
		httpReq.Header.Set(mcpNameHeader, name)
	} else if req.Method == "tools/call" {
		return nil, fmt.Errorf("invalid tools/call params: missing required name")
	}

	m.l.Warn("modern outbound request to backend", slog.String("backend", backend.Name), slog.String("method", req.Method), slog.String("params", string(req.Params)), slog.String("body", string(body)))
	resp, err := m.client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()
	m.l.Warn("modern outbound response received", "backend=", backend.Name, "method=", req.Method, "status=", resp.StatusCode, "contentType=", resp.Header.Get("Content-Type"))
	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("backend returned status %d: %s", resp.StatusCode, string(respBody))
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// Determine response format: SSE or plain JSON.
	contentType := resp.Header.Get("Content-Type")
	var jsonPayload []byte

	if strings.Contains(contentType, "text/event-stream") || strings.HasPrefix(string(respBody), "event:") || strings.HasPrefix(string(respBody), "data:") {
		// Parse SSE: find the last "data:" line which contains the JSON-RPC response.
		jsonPayload = extractJSONFromSSE(respBody)
		if jsonPayload == nil {
			return nil, fmt.Errorf("no JSON-RPC response found in SSE stream")
		}
	} else {
		jsonPayload = respBody
	}

	// Parse JSON-RPC response. Use map to avoid jsonrpc.ID unmarshal issues with sonic.
	var rpcResp map[string]json.RawMessage
	if err := json.Unmarshal(jsonPayload, &rpcResp); err != nil {
		return nil, fmt.Errorf("parse response: %w (body: %.200s)", err, string(jsonPayload))
	}
	if errField, ok := rpcResp["error"]; ok && string(errField) != "null" {
		return nil, fmt.Errorf("backend error: %s", string(errField))
	}
	result, ok := rpcResp["result"]
	if !ok || string(result) == "null" {
		return nil, fmt.Errorf("backend returned no result")
	}
	return result, nil
}

// extractJSONFromSSE parses an SSE response body and extracts the last JSON-RPC
// message from "data:" lines. MCP backends return the final result as the last event.
func extractJSONFromSSE(body []byte) []byte {
	var lastData []byte
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data != "" && data != "[DONE]" {
				lastData = []byte(data)
			}
		}
	}
	return lastData
}

// extractNameForMethod returns the name/uri field for methods that require Mcp-Name header.
func extractNameForMethod(method string, params json.RawMessage) string {
	if params == nil {
		return ""
	}
	switch method {
	case "tools/call":
		var p struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(params, &p) == nil {
			return p.Name
		}
	case "prompts/get":
		var p struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(params, &p) == nil {
			return p.Name
		}
	case "resources/read":
		var p struct {
			URI string `json:"uri"`
		}
		if json.Unmarshal(params, &p) == nil {
			return p.URI
		}
	}
	return ""
}

// modernParamsForHeaderMetadata best-effort parses params for methods where
// addMCPHeaders can enrich upstream metadata (tool name/resource URI).
func modernParamsForHeaderMetadata(req *jsonrpc.Request) mcp.Params {
	if req == nil || req.Params == nil {
		return nil
	}
	switch req.Method {
	case "tools/call":
		var p mcp.CallToolParams
		if json.Unmarshal(req.Params, &p) == nil {
			return &p
		}
	case "resources/read":
		var p mcp.ReadResourceParams
		if json.Unmarshal(req.Params, &p) == nil {
			return &p
		}
	case "resources/subscribe":
		var p mcp.SubscribeParams
		if json.Unmarshal(req.Params, &p) == nil {
			return &p
		}
	case "resources/unsubscribe":
		var p mcp.UnsubscribeParams
		if json.Unmarshal(req.Params, &p) == nil {
			return &p
		}
	}
	return nil
}

func writeJSONRPCResult(w http.ResponseWriter, id jsonrpc.ID, result any) {
	encoded, _ := json.Marshal(result)
	writeRawJSONRPCResult(w, id, encoded)
}

func writeRawJSONRPCResult(w http.ResponseWriter, id jsonrpc.ID, result json.RawMessage) {
	// P1.12: Ensure resultType: "complete" is present.
	result = ensureResultType(result)

	resp := map[string]any{
		"jsonrpc": "2.0",
		"id":      id.Raw(),
		"result":  result,
	}
	encoded, _ := json.Marshal(resp)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(encoded)
}

// ensureResultType injects "resultType":"complete" if the field is absent (P1.12).
func ensureResultType(result json.RawMessage) json.RawMessage {
	if result == nil {
		return []byte(`{"resultType":"complete"}`)
	}
	var check map[string]json.RawMessage
	if json.Unmarshal(result, &check) != nil {
		return result
	}
	if _, ok := check["resultType"]; ok {
		return result // Already has resultType (could be "input_required" from MRTR).
	}
	check["resultType"] = json.RawMessage(`"complete"`)
	out, _ := json.Marshal(check)
	return out
}

// isSupportedVersion checks if a protocol version is in our supported set.
func isSupportedVersion(v string) bool {
	for _, sv := range supportedVersions {
		if sv == v {
			return true
		}
	}
	return false
}
