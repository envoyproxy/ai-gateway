// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v4"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/internal/version"
)

var (
	errSessionNotFound = errors.New("session not found")
	errBackendNotFound = errors.New("backend not found")
	errInvalidToolName = errors.New("invalid tool name")
)

func (m *MCPProxy) serveGET(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get(sessionIDHeader)
	lastEventID := r.Header.Get(lastEventIDHeader)
	if sessionID == "" {
		m.l.Error("missing session ID in GET request")
		http.Error(w, "missing session ID", http.StatusBadRequest)
		return
	}
	s, err := m.sessionFromID(secureClientToGatewaySessionID(sessionID), secureClientToGatewayEventID(lastEventID))
	if err != nil {
		m.l.Error("invalid session ID in GET request", slog.String("session_id", sessionID), slog.String("error", err.Error()))
		http.Error(w, fmt.Sprintf("invalid session ID: %v", err), http.StatusBadRequest)
		return
	}
	if m.l.Enabled(r.Context(), slog.LevelDebug) {
		m.l.Debug("Received MCP GET request",
			slog.String("mcp_session_id", sessionID),
			slog.String("last_event_id", lastEventID),
			slog.Any("backend_session", s.perBackendSessions))
	}

	w.Header().Set(sessionIDHeader, sessionID)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("transfer-encoding", "chunked")
	w.WriteHeader(http.StatusAccepted)
	if err := s.streamNotifications(r.Context(), w); err != nil && !errors.Is(err, context.Canceled) {
		m.l.Error("failed to collect notifications", slog.String("session_id", sessionID), slog.String("error", err.Error()))
		http.Error(w, "failed to collect notifications", http.StatusInternalServerError)
		return
	}
}

func (m *MCPProxy) serverDELETE(w http.ResponseWriter, r *http.Request) {
	sessionID := r.Header.Get(sessionIDHeader)
	if sessionID == "" {
		m.l.Error("missing session ID in DELETE request")
		http.Error(w, "missing session ID", http.StatusBadRequest)
		return
	}
	// we didn't care about last event id in DELETE.
	s, err := m.sessionFromID(secureClientToGatewaySessionID(sessionID), "")
	if err != nil {
		m.l.Error("invalid session ID in DELETE request", slog.String("session_id", sessionID), slog.String("error", err.Error()))
		http.Error(w, fmt.Sprintf("invalid session ID: %v", err), http.StatusBadRequest)
		return
	}
	_ = s.Close() // Ignore error as it's not recoverable here. Errors per backend are logged in Close().
	w.WriteHeader(http.StatusOK)
}

func onErrorResponse(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(msg))
}

func (m *MCPProxy) servePOST(w http.ResponseWriter, r *http.Request) {
	var (
		ctx           = r.Context()
		startAt       = time.Now()
		s             *session
		err           error
		errType       metrics.MCPErrorType
		requestMethod string
		span          tracing.MCPSpan
		params        mcp.Params
	)
	defer func() {
		if m.l.Enabled(ctx, slog.LevelDebug) {
			m.l.Debug("Completed MCP POST request",
				slog.String("method", requestMethod),
				slog.String("error_type", string(errType)),
				slog.String("duration", time.Since(startAt).String()))
		}
		if err != nil {
			if span != nil {
				span.EndSpanOnError(string(errType), err)
			}
			m.metrics.RecordMethodErrorCount(ctx, params)
			m.metrics.RecordRequestErrorDuration(ctx, &startAt, errType, params)
			return
		}

		if span != nil {
			span.EndSpan()
		}
		m.metrics.RecordRequestDuration(ctx, &startAt, params)
		// TODO: should we special case when this request is "Response" where method is empty?
		m.metrics.RecordMethodCount(ctx, requestMethod, params)
	}()
	if sessionID := r.Header.Get(sessionIDHeader); sessionID != "" {
		s, err = m.sessionFromID(secureClientToGatewaySessionID(sessionID), secureClientToGatewayEventID(r.Header.Get(lastEventIDHeader)))
		if err != nil {
			errType = metrics.MCPErrorInvalidSessionID
			m.l.Error("invalid session ID in POST request", slog.String("session_id", sessionID), slog.String("error", err.Error()))
			http.Error(w, fmt.Sprintf("invalid session ID: %v", err), http.StatusBadRequest)
			return
		}
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		errType = metrics.MCPErrorInternal
		onErrorResponse(w, http.StatusBadRequest, err.Error())
		return
	}

	rawMsg, err := jsonrpc.DecodeMessage(body)
	if err != nil {
		errType = metrics.MCPErrorInvalidJSONRPC
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON-RPC message: %v", err))
		return
	}

	switch msg := rawMsg.(type) {
	case *jsonrpc.Response:
		if str, ok := msg.ID.Raw().(string); ok && strings.HasPrefix(str, envoyAIGatewayServerToClientPingRequestIDPrefix) {
			w.Header().Set(sessionIDHeader, string(s.clientGatewaySessionID()))
			w.WriteHeader(http.StatusAccepted)
		} else {
			// We do require a Session ID. If it is not present, a 400 Bad Request response should be returned:
			// https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#session-management
			if s == nil {
				errType = metrics.MCPErrorInvalidSessionID
				onErrorResponse(w, http.StatusBadRequest, "missing session ID")
				return
			}
			m.l.Debug("Decoded MCP response", slog.Any("response", msg))
			err = m.handleClientToServerResponse(ctx, s, w, msg)
		}
	case *jsonrpc.Request:
		requestMethod = msg.Method
		if m.l.Enabled(ctx, slog.LevelDebug) {
			m.l.Debug("Decoded MCP request",
				slog.Any("id", msg.ID), slog.String("method", msg.Method), slog.String("params", string(msg.Params)))
		}

		// We do require a Session ID. If it is not present for requests other than initialize,
		// a 400 Bad Request response should be returned:
		// https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#session-management
		if s == nil && msg.Method != "initialize" {
			errType = metrics.MCPErrorInvalidSessionID
			onErrorResponse(w, http.StatusBadRequest, "missing session ID")
			return
		}

		switch msg.Method {
		case "notifications/roots/list_changed":
			params = &mcp.RootsListChangedParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				onErrorResponse(w, http.StatusBadRequest, "invalid params")
				return
			}
			err = m.handleNotificationsRootsListChanged(ctx, s, w, msg, span)
		case "completion/complete":
			params = &mcp.CompleteParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				onErrorResponse(w, http.StatusBadRequest, "invalid params")
				return
			}
			err = m.handleCompletionComplete(ctx, s, w, msg, params.(*mcp.CompleteParams), span)
		case "notifications/progress":
			params = &mcp.ProgressNotificationParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			m.metrics.RecordProgress(ctx, params)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				onErrorResponse(w, http.StatusBadRequest, "invalid params")
				return
			}
			err = m.handleClientToServerNotificationsProgress(ctx, s, w, msg, params.(*mcp.ProgressNotificationParams), span)
		case "initialize":
			// The very first request from the client to establish a session.
			params = &mcp.InitializeParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				m.l.Error("Failed to unmarshal initialize params", slog.String("error", err.Error()))
				onErrorResponse(w, http.StatusBadRequest, "invalid initialize params")
				return
			}
			// The Envoy frontend listener’s HTTPRouteFilter adds the route name header.
			// The MCP proxy uses it to locate the route’s backends and initialize sessions with them.
			route := r.Header.Get(internalapi.MCPRouteHeader)
			if route == "" {
				errType = metrics.MCPErrorInternal
				m.l.Error("cannot find route header in the downstream request")
				onErrorResponse(w, http.StatusInternalServerError, "missing route header")
				return
			}
			err = m.handleInitializeRequest(ctx, w, msg, params.(*mcp.InitializeParams), route, extractSubject(r), span)
		case "notifications/initialized":
			// According to the MCP spec, when the server receives a JSON-RPC response or notification from the client
			// and accepts it, the server MUST return HTTP 202 Accepted with an empty body.
			// https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#sending-messages-to-the-server
			w.WriteHeader(http.StatusAccepted)
		case "logging/setLevel":
			params = &mcp.SetLoggingLevelParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				m.l.Error("Failed to unmarshal set logging level params", slog.String("error", err.Error()))
				onErrorResponse(w, http.StatusBadRequest, "invalid set logging level params")
				return
			}
			err = m.handleSetLoggingLevel(ctx, s, w, msg, params.(*mcp.SetLoggingLevelParams), span)
		case "ping":
			// Ping is intentionally not traced as it's a lightweight health check.
			err = m.handlePing(ctx, w, msg)
		case "prompts/list":
			params = &mcp.ListPromptsParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				onErrorResponse(w, http.StatusBadRequest, "invalid params")
				return
			}
			err = m.handlePromptListRequest(ctx, s, w, msg, params.(*mcp.ListPromptsParams), span)
		case "prompts/get":
			params = &mcp.GetPromptParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				onErrorResponse(w, http.StatusBadRequest, "invalid params")
				return
			}
			err = m.handlePromptGetRequest(ctx, s, w, msg, params.(*mcp.GetPromptParams))
		case "tools/call":
			params = &mcp.CallToolParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				m.l.Error("Failed to unmarshal params", slog.String("method", msg.Method), slog.String("error", err.Error()))
				onErrorResponse(w, http.StatusBadRequest, "invalid params")
				return
			}
			err = m.handleToolCallRequest(ctx, s, w, msg, params.(*mcp.CallToolParams), span)
		case "tools/list":
			params = &mcp.ListToolsParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				onErrorResponse(w, http.StatusBadRequest, "invalid params")
				return
			}
			err = m.handleToolsListRequest(ctx, s, w, msg, params.(*mcp.ListToolsParams), span)
		case "resources/list":
			params = &mcp.ListResourcesParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				onErrorResponse(w, http.StatusBadRequest, "invalid params")
				return
			}
			err = m.handleResourceListRequest(ctx, s, w, msg, params.(*mcp.ListResourcesParams), span)
		case "resources/read":
			params = &mcp.ReadResourceParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				onErrorResponse(w, http.StatusBadRequest, "invalid params")
				return
			}
			err = m.handleResourceReadRequest(ctx, s, w, msg, params.(*mcp.ReadResourceParams))
		case "resources/templates/list":
			params = &mcp.ListResourceTemplatesParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				onErrorResponse(w, http.StatusBadRequest, "invalid params")
				return
			}
			err = m.handleResourcesTemplatesListRequest(ctx, s, w, msg, params.(*mcp.ListResourceTemplatesParams), span)
		case "resources/subscribe":
			params = &mcp.SubscribeParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				onErrorResponse(w, http.StatusBadRequest, "invalid params")
				return
			}
			err = m.handleResourcesSubscribeRequest(ctx, s, w, msg, params.(*mcp.SubscribeParams), span)
		case "resources/unsubscribe":
			params = &mcp.UnsubscribeParams{}
			span, err = parseParamsAndMaybeStartSpan(ctx, m, msg, params, r.Header)
			if err != nil {
				errType = metrics.MCPErrorInvalidParam
				onErrorResponse(w, http.StatusBadRequest, "invalid params")
				return
			}
			err = m.handleResourcesUnsubscribeRequest(ctx, s, w, msg, params.(*mcp.UnsubscribeParams), span)
		case "notifications/cancelled":
			// The responsibility of cancelling the operation on server side is optional, so we just ignore it for now.
			// https://modelcontextprotocol.io/specification/2025-06-18/basic/utilities/cancellation#behavior-requirements
			//
			// TODO: If we want to do it properly, we need to maintain the request ID to backend mapping in a remote cache.
			// According to the MCP spec, when the server receives a JSON-RPC response or notification from the client
			// and accepts it, the server MUST return HTTP 202 Accepted with an empty body.
			// https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#sending-messages-to-the-server
			w.WriteHeader(http.StatusAccepted)
		default:
			errType = metrics.MCPErrorUnsupportedMethod
			err = fmt.Errorf("unsupported method: %s", msg.Method)
			onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("unsupported method: %s", msg.Method))
			return
		}
		errType = errorType(err)
	default:
		errType = metrics.MCPErrorUnsupportedResponse
		err = errors.New("unsupported JSON-RPC message type")
		onErrorResponse(w, http.StatusBadRequest, "unsupported JSON-RPC message type")
	}
}

func errorType(err error) metrics.MCPErrorType {
	switch {
	case errors.Is(err, errBackendNotFound) || errors.Is(err, errSessionNotFound) || errors.Is(err, errInvalidToolName):
		return metrics.MCPErrorInvalidParam
	case err != nil:
		return metrics.MCPErrorInternal
	}
	return ""
}

// handleInitializeRequest handles the "initialize" JSON-RPC method.
func (m *MCPProxy) handleInitializeRequest(ctx context.Context, w http.ResponseWriter, req *jsonrpc.Request, p *mcp.InitializeParams, route, subject string, span tracing.MCPSpan) error {
	m.metrics.RecordClientCapabilities(ctx, p.Capabilities, p)
	s, err := m.newSession(ctx, p, route, subject, span)
	if err != nil {
		m.l.Error("failed to create new session", slog.String("error", err.Error()))
		onErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("failed to create new session: %v", err))
		return err
	}

	result := mcp.InitializeResult{ProtocolVersion: protocolVersion20250618, ServerInfo: &mcp.Implementation{}}
	result.ServerInfo.Name = "envoy-ai-gateway"
	result.ServerInfo.Version = version.Parse()
	result.Capabilities = &mcp.ServerCapabilities{
		Tools:       &mcp.ToolCapabilities{ListChanged: true},
		Prompts:     &mcp.PromptCapabilities{ListChanged: true},
		Logging:     &mcp.LoggingCapabilities{},
		Resources:   &mcp.ResourceCapabilities{ListChanged: true, Subscribe: true},
		Completions: &mcp.CompletionCapabilities{},
	}

	marshal, err := json.Marshal(result)
	if err != nil {
		m.l.Error("failed to create new session", slog.String("error", err.Error()))
		onErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("failed to create new session: %v", err))
		return err
	}

	// Convert it to raw JSON message.
	data, err := jsonrpc.EncodeMessage(&jsonrpc.Response{
		ID:     req.ID,
		Result: marshal,
	})
	if err != nil {
		m.l.Error("failed to create new session", slog.String("error", err.Error()))
		onErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("failed to create new session: %v", err))
		return err
	}
	if m.l.Enabled(ctx, slog.LevelDebug) {
		m.l.Debug("MCP session initialized", slog.String("mcp_session_id", string(s.clientGatewaySessionID())))
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set(sessionIDHeader, string(s.clientGatewaySessionID()))
	w.WriteHeader(http.StatusOK)
	_, err = w.Write(data)
	return err
}

// handleClientToServerResponse handles the response from client to server.
//
// The idea is that the request ID is constructed in maybeServerToClientRequestModify to include the original request ID, type, backend name and path prefix.
// So here we need to parse the ID and restore the original ID before sending it to the backend.
func (m *MCPProxy) handleClientToServerResponse(ctx context.Context, s *session, w http.ResponseWriter, res *jsonrpc.Response) error {
	clientToServer, ok := res.ID.Raw().(string)
	// We should've modified the server->client request ID to include the backend name.
	if !ok {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid response ID type: %v", res.ID.Raw()))
		return errors.New("invalid response ID type")
	}
	// TODO: we might want to encrypt/sign the ID to prevent tampering just like session in maybeServerToClientRequestModify.
	//		If we do that, we need to decrypt/verify it here.
	parts := strings.Split(clientToServer, nameSeparator)
	if len(parts) != 3 {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid response ID format: %s", clientToServer))
		return errors.New("invalid response ID format")
	}
	originalIDRaw := parts[0]
	typeIdentifier := parts[1]
	backendName := parts[2]
	var id jsonrpc.ID
	switch typeIdentifier {
	case "i": // ID is an int64 encoded as bytes.
		i64, err := strconv.ParseInt(originalIDRaw, 10, 64)
		if err != nil {
			onErrorResponse(w, http.StatusBadRequest, "invalid response ID format")
			return fmt.Errorf("invalid response ID format: %w", err)
		}
		id, err = jsonrpc.MakeID(float64(i64))
		if err != nil {
			onErrorResponse(w, http.StatusBadRequest, "invalid response ID format")
			return fmt.Errorf("invalid response ID format: %w", err)
		}
		if m.l.Enabled(ctx, slog.LevelDebug) {
			m.l.Debug("Parsed int64 ID", slog.Int64("id", i64), slog.Any("jsonrpc_id", id))
		}
	case "f": // ID is a float64 encoded as bytes.
		b, err := hex.DecodeString(originalIDRaw)
		if err != nil {
			onErrorResponse(w, http.StatusBadRequest, "invalid response ID format")
			return fmt.Errorf("invalid response ID format: %w: %s", err, originalIDRaw)
		}
		id, err = jsonrpc.MakeID(math.Float64frombits(binary.LittleEndian.Uint64(b)))
		if err != nil {
			onErrorResponse(w, http.StatusBadRequest, "invalid response ID format")
			return fmt.Errorf("invalid response ID format: %w", err)
		}
		if m.l.Enabled(ctx, slog.LevelDebug) {
			m.l.Debug("Parsed float64 ID", slog.Float64("id", math.Float64frombits(binary.LittleEndian.Uint64(b))), slog.Any("jsonrpc_id", id))
		}
	case "s": // ID is a string encoded as base64.
		decoded, err := base64.StdEncoding.DecodeString(originalIDRaw)
		if err != nil {
			onErrorResponse(w, http.StatusBadRequest, "invalid response ID format")
			return fmt.Errorf("invalid response ID format: %w: %s", err, originalIDRaw)
		}
		id, err = jsonrpc.MakeID(string(decoded))
		if err != nil {
			onErrorResponse(w, http.StatusBadRequest, "invalid response ID format")
			return fmt.Errorf("invalid response ID format: %w", err)
		}
		if m.l.Enabled(ctx, slog.LevelDebug) {
			m.l.Debug("Parsed string ID", slog.String("id", originalIDRaw), slog.Any("jsonrpc_id", id))
		}
	default:
		onErrorResponse(w, http.StatusBadRequest, "invalid response ID type identifier")
		return fmt.Errorf("invalid response ID type identifier: %s", typeIdentifier)
	}
	res.ID = id

	cse := s.getCompositeSessionEntry(backendName)
	if cse == nil {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("no MCP session found for backend %s", backendName))
		return fmt.Errorf("no MCP session found for backend %s", backendName)
	}

	backend, err := m.getBackendForRoute(s.route, backendName)
	if err != nil {
		onErrorResponse(w, http.StatusNotFound, fmt.Sprintf("unknown backend %s", backendName))
		return fmt.Errorf("%w: unknown backend %s", errBackendNotFound, backendName)
	}
	resp, err := m.invokeJSONRPCRequest(ctx, s.route, backend, cse, res)
	if err != nil {
		onErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("failed to send: %v", err))
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	copyProxyHeaders(resp, w)
	w.Header().Set(sessionIDHeader, string(s.clientGatewaySessionID()))
	m.proxyResponseBody(ctx, s, w, resp, nil, backend)
	return nil
}

func (m *MCPProxy) handleToolCallRequest(ctx context.Context, s *session, w http.ResponseWriter, req *jsonrpc.Request, p *mcp.CallToolParams, span tracing.MCPSpan) error {
	backendName, toolName, err := upstreamResourceName(p.Name)
	if err != nil {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid tool name %s: %v", p.Name, err))
		return err
	}

	backend, err := m.getBackendForRoute(s.route, backendName)
	if err != nil {
		onErrorResponse(w, http.StatusNotFound, fmt.Sprintf("unknown backend %s", backendName))
		return fmt.Errorf("%w: unknown backend %s", errBackendNotFound, backendName)
	}

	// Validate that the tool is whitelisted for this route
	route := m.routes[s.route]
	if route == nil {
		// This should never happen as the route must have been validated when the session is created.
		onErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("route not found: %s", s.route))
		return fmt.Errorf("route not found: %s", s.route)
	}
	selector := route.toolSelectors[backendName]
	if selector != nil && !selector.allows(toolName) {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid tool name: %s", toolName))
		return fmt.Errorf("%w: %s", errInvalidToolName, toolName)
	}

	cse := s.getCompositeSessionEntry(backendName)
	if cse == nil {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("no MCP session found for backend %s", backendName))
		return fmt.Errorf("%w: no MCP session found for backend %s", errSessionNotFound, backendName)
	}

	// Send the request to the MCP backend listener.
	p.Name = toolName
	param, _ := json.Marshal(p)
	if m.l.Enabled(ctx, slog.LevelDebug) {
		logger := m.l.With(slog.String("tool", p.Name), slog.Any("session", cse))
		logger.Debug("Routing to backend")
	}
	if span != nil {
		span.RecordRouteToBackend(backend.Name, string(cse.sessionID), false)
	}
	req.Params = param
	return m.invokeAndProxyResponse(ctx, s, w, backend, cse, req)
}

func copyProxyHeaders(resp *http.Response, w http.ResponseWriter) {
	isJSONResponse := resp.Header.Get("Content-Type") == "application/json"
	for k, v := range resp.Header {
		// Skip content-length header for non JSON response since we might modify the response.
		if !isJSONResponse && strings.ToLower(k) == "content-length" {
			continue
		}

		for _, vv := range v {
			w.Header().Add(k, vv)
		}
	}
	if !isJSONResponse {
		w.Header().Set("Transfer-Encoding", "chunked")
	}
}

func (m *MCPProxy) proxyResponseBody(ctx context.Context, s *session, w http.ResponseWriter, resp *http.Response,
	req *jsonrpc.Request, backend filterapi.MCPBackend,
) {
	if resp.Header.Get("Content-Type") == "application/json" {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			m.l.Error("failed to read response body", slog.String("error", err.Error()))
			return
		}
		_msg, err := jsonrpc.DecodeMessage(body)
		if err != nil {
			m.l.Error("failed to decode JSON-RPC message from response body", slog.String("error", err.Error()))
			return
		}

		switch msg := _msg.(type) {
		case *jsonrpc.Request:
			if err := m.maybeServerToClientRequestModify(ctx, msg, backend.Name); err != nil {
				m.l.Error("failed to modify server->client request", slog.String("error", err.Error()))
				return
			}
			body, _ = jsonrpc.EncodeMessage(msg)
		case *jsonrpc.Response:
			if req != nil {
				msg.ID = req.ID
				body, _ = jsonrpc.EncodeMessage(msg)
			}
			m.recordResponse(ctx, msg)
		}

		// We need to update the content length since we might have modified the ID.
		w.Header().Set("Content-Length", fmt.Sprintf("%d", len(body)))
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(body)
		return
	}

	// io.Copy won't flush until the end, which doesn't happen for streaming responses.
	// So we need to read the body in chunks and flush after each chunk.
	if m.l.Enabled(ctx, slog.LevelDebug) {
		m.l.Debug("Starting to stream MCP response body", slog.String("content_type", resp.Header.Get("Content-Type")), slog.String("mcp_session_id", resp.Header.Get(sessionIDHeader)))
	}
	w.WriteHeader(resp.StatusCode)
	parser := newSSEEventParser(resp.Body, backend.Name)
	for {
		event, err := parser.next()
		// TODO: handle reconnect. We need to re-arrange the event ID so that it will also contain the backend name and the original session ID.
		// 	Since event ID can be arbitrary string, we can shove each backend's last even ID into the event ID just like the session ID.
		if event != nil {
			// update per backend last event id then regenerate event id.
			prev := event.id
			s.setLastEventID(event.backend, event.id)
			event.id = s.lastEventID()
			if m.l.Enabled(ctx, slog.LevelDebug) {
				m.l.Debug("Changed event ID", slog.String("backend", event.backend),
					slog.String("prev_event_id", prev),
					slog.String("event_id", event.id))
			}

			for _, _msg := range event.messages {
				switch msg := _msg.(type) {
				case *jsonrpc.Request:
					if err = m.maybeServerToClientRequestModify(ctx, msg, backend.Name); err != nil {
						m.l.Error("failed to modify server->client request", slog.String("error", err.Error()))
						continue
					}
				case *jsonrpc.Response:
					// Correct the ID to match the original request if possible.
					if req != nil {
						msg.ID = req.ID
					}
					m.recordResponse(ctx, msg)
				}
			}
			event.writeAndMaybeFlush(w)
		}
		if err != nil {
			if errors.Is(err, io.EOF) || strings.Contains(err.Error(), "context deadline exceeded") {
				break
			}
			m.l.Error("failed to read MCP GET response body", slog.String("error", err.Error()))
			break
		}
	}
}

// https://modelcontextprotocol.io/specification/2025-06-18/basic/utilities/progress#progress
const progressTokenMetadataKey = "progressToken"

func (m *MCPProxy) maybeUpdateProgressTokenMetadata(ctx context.Context, meta mcp.Meta, backendName filterapi.MCPBackendName) bool {
	// TODO: maybe enctrypt/sign the progress token to prevent tampering just like session/event ID.
	originalPt, ok := meta[progressTokenMetadataKey]
	if !ok {
		return false
	}
	var newPt string
	switch v := originalPt.(type) {
	case string:
		base64Encoded := base64.StdEncoding.EncodeToString([]byte(v))
		newPt = fmt.Sprintf("%s%ss%s%s", base64Encoded, nameSeparator, nameSeparator, backendName)
	case int64:
		newPt = fmt.Sprintf("%d%si%s%s", v, nameSeparator, nameSeparator, backendName)
	case float64:
		// Bytes encoded as number will be decoded as float64.
		buf := [8]byte{}
		b := buf[:]
		binary.LittleEndian.PutUint64(b, math.Float64bits(v))
		newPt = fmt.Sprintf("%x%sf%s%s", b, nameSeparator, nameSeparator, backendName)
	case nil:
		return false // Valid per spec.
	default:
		m.l.Warn("TODO/BUG: unsupported progressToken type in metadata", slog.String("type", fmt.Sprintf("%T", v)))
		return false
	}

	meta["progressToken"] = newPt
	if m.l.Enabled(ctx, slog.LevelDebug) {
		m.l.Debug("Modified progressToken in metadata", slog.Any("old_progress_token", originalPt), slog.String("new_progress_token", newPt), slog.String("backend", backendName))
	}
	return true
}

// maybeServerToClientRequestModify modifies the server->client request ID to include the backend name and path prefix
// so that we can route the client->server response back to the correct backend.
//
// This essentially prepares the request for the future invocation of handleClientToServerResponse.
func (m *MCPProxy) maybeServerToClientRequestModify(ctx context.Context, msg *jsonrpc.Request, backend filterapi.MCPBackendName) error {
	switch msg.Method {
	case "roots/list":
		if msg.Params != nil {
			params := &mcp.ListRootsParams{}
			if err := json.Unmarshal(msg.Params, params); err != nil {
				return fmt.Errorf("failed to unmarshal roots/list params: %w", err)
			}
			if m.maybeUpdateProgressTokenMetadata(ctx, params.Meta, backend) {
				msg.Params, _ = json.Marshal(params) // Already decoded params, so ignore error.
			}
		}
	case "sampling/createMessage":
		if msg.Params != nil {
			params := &mcp.CreateMessageParams{}
			if err := json.Unmarshal(msg.Params, params); err != nil {
				return fmt.Errorf("failed to unmarshal sampling/createMessage params: %w", err)
			}
			if m.maybeUpdateProgressTokenMetadata(ctx, params.Meta, backend) {
				msg.Params, _ = json.Marshal(params) // Already decoded params, so ignore error.
			}
		}
	case "elicitation/create":
		if msg.Params != nil {
			params := &mcp.ElicitParams{}
			if err := json.Unmarshal(msg.Params, params); err != nil {
				return fmt.Errorf("failed to unmarshal elicitation/create params: %w", err)
			}
			if m.maybeUpdateProgressTokenMetadata(ctx, params.Meta, backend) {
				msg.Params, _ = json.Marshal(params) // Already decoded params, so ignore error.
			}
		}
	default:
		// Others are not server->client requests that we care about.
		return nil
	}

	var prefixedID string
	switch v := msg.ID.Raw().(type) {
	case nil:
		return errors.New("missing id in the server->client request")
	case int64:
		prefixedID = fmt.Sprintf("%d%si%s%s", v, nameSeparator, nameSeparator, backend)
	case float64:
		// Bytes encoded as number will be decoded as float64.
		buf := [8]byte{}
		b := buf[:]
		binary.LittleEndian.PutUint64(b, math.Float64bits(v))
		prefixedID = fmt.Sprintf("%x%sf%s%s", b, nameSeparator, nameSeparator, backend)
	case string:
		encoded := base64.StdEncoding.EncodeToString([]byte(v))
		prefixedID = fmt.Sprintf("%s%ss%s%s", encoded, nameSeparator, nameSeparator, backend)
	default:
		return fmt.Errorf("BUG/TODO: unsupported id type %T in the server->client request", v)
	}
	// TODO: we might want to encrypt/sign the ID to prevent tampering just like session/event ID.
	newID, err := jsonrpc.MakeID(prefixedID)
	if err != nil {
		return fmt.Errorf("failed to make new ID %q: %w", prefixedID, err)
	}
	if m.l.Enabled(ctx, slog.LevelDebug) {
		m.l.Debug("Modified server->client request ID", slog.Any("old_id", msg.ID), slog.Any("new_id", newID), slog.String("backend", backend))
	}
	msg.ID = newID
	return nil
}

func (m *MCPProxy) recordResponse(ctx context.Context, rawMsg jsonrpc.Message) {
	switch msg := rawMsg.(type) {
	case *jsonrpc.Response:
		if m.l.Enabled(ctx, slog.LevelDebug) {
			m.l.Debug("Decoded MCP response from server",
				slog.Any("id", msg.ID),
				slog.String("result", string(msg.Result)),
				slog.Any("error", msg.Error))
		}
	case *jsonrpc.Request:
		if m.l.Enabled(ctx, slog.LevelDebug) {
			m.l.Debug("Decoded MCP request from server", slog.Any("method", msg.Method))
		}
		knownMethod := true
		switch msg.Method {
		case "notifications/prompts/list_changed":
		case "notifications/resources/list_changed":
		case "notifications/resources/updated":
		case "notifications/progress":
			params := &mcp.ProgressNotificationParams{}
			if err := json.Unmarshal(msg.Params, &params); err != nil {
				m.l.Error("Failed to unmarshal params", slog.String("method", msg.Method), slog.String("error", err.Error()))
			}
			m.metrics.RecordProgress(ctx, params)
		case "notifications/message":
		case "notifications/tools/list_changed":
		case "roots/list":
		case "sampling/createMessage":
		case "elicitation/create":
		default:
			knownMethod = false
			m.metrics.RecordMethodErrorCount(ctx, nil)
			m.l.Warn("Unsupported MCP request method from server", slog.String("method", msg.Method))
		}
		if knownMethod {
			m.metrics.RecordMethodCount(ctx, msg.Method, nil)
		}
	default:
		m.l.Warn("unexpected message type in MCP response", slog.Any("message", msg))
	}
}

func (m *MCPProxy) mcpEndpointForBackend(backend filterapi.MCPBackend) string {
	return m.backendListenerAddr + backend.Path
}

func (m *MCPProxy) handleResourceReadRequest(ctx context.Context, s *session, w http.ResponseWriter, req *jsonrpc.Request, p *mcp.ReadResourceParams) error {
	backendName, resourceName, err := upstreamResourceName(p.URI)
	if err != nil {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid resource name %s: %v", p.URI, err))
		return err
	}
	backend, err := m.getBackendForRoute(s.route, backendName)
	if err != nil {
		onErrorResponse(w, http.StatusNotFound, fmt.Sprintf("unknown backend %s", backendName))
		return fmt.Errorf("%w: unknown backend %s in resource name %s", errBackendNotFound, backendName, p.URI)
	}
	sess := s.getCompositeSessionEntry(backendName)
	if sess == nil {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("no MCP session found for backend %s", backendName))
		return fmt.Errorf("%w: no MCP session found for backend %s", errSessionNotFound, backendName)
	}
	// Send the request to the MCP backend listener.
	p.URI = resourceName
	param, _ := json.Marshal(p)
	if m.l.Enabled(ctx, slog.LevelDebug) {
		logger := m.l.With(slog.String("method", req.Method), slog.Any("session_", sess),
			slog.String("resource", p.URI))
		logger.Debug("Routing to backend")
	}
	req.Params = param
	return m.invokeAndProxyResponse(ctx, s, w, backend, sess, req)
}

// handleResourcesSubscribeRequest handles the "resources/subscribe" JSON-RPC method.
func (m *MCPProxy) handleResourcesSubscribeRequest(ctx context.Context, s *session, w http.ResponseWriter, req *jsonrpc.Request, p *mcp.SubscribeParams, span tracing.MCPSpan) error {
	return m.handleResourcesSubscriptionRequest(ctx, s, w, req, p, span)
}

// handleResourcesUnsubscribeRequest handles the "resources/unsubscribe" JSON-RPC method.
func (m *MCPProxy) handleResourcesUnsubscribeRequest(ctx context.Context, s *session, w http.ResponseWriter, req *jsonrpc.Request, p *mcp.UnsubscribeParams, span tracing.MCPSpan) error {
	return m.handleResourcesSubscriptionRequest(ctx, s, w, req, p, span)
}

func (m *MCPProxy) handleResourcesSubscriptionRequest(ctx context.Context, s *session, w http.ResponseWriter,
	req *jsonrpc.Request, p interface{}, // *mcp.SubscribeParams or *mcp.UnsubscribeParams.
	span tracing.MCPSpan,
) error {
	var uri string
	switch v := p.(type) {
	case *mcp.SubscribeParams:
		uri = v.URI
	case *mcp.UnsubscribeParams:
		uri = v.URI
	default:
		return fmt.Errorf("invalid params type")
	}
	backendName, resourceName, err := upstreamResourceName(uri)
	if err != nil {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid resource name %s: %v", uri, err))
		return err
	}
	backend, err := m.getBackendForRoute(s.route, backendName)
	if err != nil {
		onErrorResponse(w, http.StatusNotFound, fmt.Sprintf("unknown backend %s", backendName))
		return fmt.Errorf("%w: unknown backend %s in resource name %s", errBackendNotFound, backendName, uri)
	}
	cse := s.getCompositeSessionEntry(backendName)
	if cse == nil {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("no MCP session found for backend %s", backendName))
		return fmt.Errorf("%w: no MCP session found for backend %s", errSessionNotFound, backendName)
	}

	// update the resource name in params to the downstream name.
	switch v := p.(type) {
	case *mcp.SubscribeParams:
		v.URI = resourceName
	case *mcp.UnsubscribeParams:
		v.URI = resourceName
	}

	param, _ := json.Marshal(p)
	if m.l.Enabled(ctx, slog.LevelDebug) {
		logger := m.l.With(
			slog.String("method", req.Method),
			slog.String("resource", resourceName),
			slog.String("backend", backend.Name),
			slog.Any("session", cse),
		)
		logger.Debug("Routing to backend")
	}
	if span != nil {
		span.RecordRouteToBackend(backend.Name, string(cse.sessionID), false)
	}
	req.Params = param
	return m.invokeAndProxyResponse(ctx, s, w, backend, cse, req)
}

var emptyJSONRPCMessage = json.RawMessage(`{}`)

func (m *MCPProxy) handlePing(_ context.Context, w http.ResponseWriter, req *jsonrpc.Request) (err error) {
	encodedResp, _ := jsonrpc.EncodeMessage(&jsonrpc.Response{ID: req.ID, Result: emptyJSONRPCMessage})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if _, err = w.Write(encodedResp); err != nil {
		m.l.Error("failed to write response", slog.String("error", err.Error()))
	}
	return
}

// Using "__" as the separator to avoid collision with any character in k8s resource names as well as base64 encoding.
// We can't use special characters as tool names must match the regex `[a-zA-Z0-9._-]+`.
const nameSeparator = "__"

// downstreamResourceName converts the upstream resource/prompt name to the downstream resource/prompt name by
// prefixing the backend name.
func downstreamResourceName(name string, backendName string) string {
	return fmt.Sprintf("%s%s%s", backendName, nameSeparator, name)
}

// upstreamResourceName converts the downstream tool/resource name to the upstream resource/prompt name by
// stripping the backend name prefix.
//
// We assume that all tool/resource names are prefixed with the backend name followed by an underscore, so
// it's an unrecoverable error if the tool/resource name doesn't contain an underscore and that's a client error.
func upstreamResourceName(fullName string) (backendName, name string, err error) {
	index := strings.Index(fullName, nameSeparator)
	if index < 0 {
		return "", "", fmt.Errorf("invalid resource name: %s", fullName)
	}
	return fullName[:index], fullName[index+len(nameSeparator):], nil
}

// extractSubject extracts the "sub" claim from the JWT in the Authorization header.
// This method will not validate the token as it assumes if the token is present it has already been
// validated and authenticated.
func extractSubject(r *http.Request) string {
	authzHeader := r.Header.Get("Authorization")
	if authzHeader == "" {
		return ""
	}
	parts := strings.SplitN(authzHeader, " ", 2)
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return ""
	}

	var claims jwt.RegisteredClaims
	_, _, _ = jwt.NewParser().ParseUnverified(parts[1], &claims)
	return claims.Subject
}

// handlePromptGetRequest handles the "prompts/get" JSON-RPC method.
func (m *MCPProxy) handlePromptGetRequest(ctx context.Context, s *session, w http.ResponseWriter, req *jsonrpc.Request, p *mcp.GetPromptParams) error {
	backendName, promptName, err := upstreamResourceName(p.Name)
	if err != nil {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid prompt name %s: %v", p.Name, err))
		return err
	}
	backend, err := m.getBackendForRoute(s.route, backendName)
	if err != nil {
		onErrorResponse(w, http.StatusNotFound, fmt.Sprintf("unknown backend %s", backendName))
		return fmt.Errorf("%w: unknown backend %s in prompt name %s", errBackendNotFound, backendName, p.Name)
	}
	cse := s.getCompositeSessionEntry(backendName)
	if cse == nil {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("no MCP session found for backend %s", backendName))
		return fmt.Errorf("%w: no MCP session found for backend %s", errSessionNotFound, backendName)
	}
	// Send the request to the MCP backend listener.
	p.Name = promptName
	param, _ := json.Marshal(p)
	if m.l.Enabled(ctx, slog.LevelDebug) {
		logger := m.l.With(slog.String("method", req.Method), slog.String("backend", backend.Name), slog.Any("session", cse),
			slog.String("prompt", p.Name))
		logger.Debug("Routing to backend")
	}
	req.Params = param
	return m.invokeAndProxyResponse(ctx, s, w, backend, cse, req)
}

func (m *MCPProxy) handleCompletionComplete(ctx context.Context, s *session, w http.ResponseWriter, req *jsonrpc.Request, param *mcp.CompleteParams, span tracing.MCPSpan) error {
	backendName, resourceName, err := upstreamResourceName(cmp.Or(param.Ref.Name, param.Ref.URI))
	if err != nil {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid resource name %s: %v", param.Ref.Name, err))
		return err
	}
	// Either one of Name or URI is non-empty, depending on the Ref.Type.
	// https://modelcontextprotocol.io/specification/2025-06-18/server/utilities/completion#reference-types
	switch param.Ref.Type {
	case "ref/prompt":
		param.Ref.Name = resourceName
	case "ref/resource":
		param.Ref.URI = resourceName
	}
	encodedParam, _ := json.Marshal(param)
	req.Params = encodedParam

	backend, err := m.getBackendForRoute(s.route, backendName)
	if err != nil {
		onErrorResponse(w, http.StatusNotFound, fmt.Sprintf("unknown backend %s", backendName))
		return fmt.Errorf("%w: unknown backend %s in resource name %s", errBackendNotFound, backendName, resourceName)
	}

	// Send the request to the MCP backend listener.
	cse := s.getCompositeSessionEntry(backend.Name)
	if span != nil {
		span.RecordRouteToBackend(backend.Name, string(cse.sessionID), false)
	}
	return m.invokeAndProxyResponse(ctx, s, w, backend, cse, req)
}

// handleClientToServerNotificationsProgress handles client-to-server progress notifications that require routing to a specific backend.
//
// The progressToken contains the backend name and path prefix, so we can use that to route the notification to the correct backend.
func (m *MCPProxy) handleClientToServerNotificationsProgress(ctx context.Context, s *session, w http.ResponseWriter, req *jsonrpc.Request, p *mcp.ProgressNotificationParams, span tracing.MCPSpan) error {
	pt, ok := p.ProgressToken.(string)
	if !ok {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid progressToken type %T", p.ProgressToken))
		return fmt.Errorf("invalid progressToken type %T", p.ProgressToken)
	}

	parts := strings.Split(pt, nameSeparator)
	if len(parts) != 3 {
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid progressToken %s", pt))
		return fmt.Errorf("invalid progressToken %s", pt)
	}

	// The following does inverse of maybeUpdateProgressTokenMetadata.
	originalPt := parts[0]
	originalPtType := parts[1]
	switch originalPtType {
	case "s":
		decoded, err := base64.StdEncoding.DecodeString(originalPt)
		if err != nil {
			onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid progressToken %s: %v", pt, err))
			return fmt.Errorf("invalid progressToken %s: %w", pt, err)
		}
		p.ProgressToken = string(decoded)
	case "i":
		v, err := strconv.ParseInt(originalPt, 10, 64)
		if err != nil {
			onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid progressToken %s: %v", pt, err))
			return fmt.Errorf("invalid progressToken %s: %w", pt, err)
		}
		p.ProgressToken = v
	case "f":
		// Bytes encoded as hex string.
		b, err := hex.DecodeString(originalPt)
		if err != nil {
			onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid progressToken %s: %v", pt, err))
			return fmt.Errorf("invalid progressToken %s: %w", pt, err)
		}
		if len(b) != 8 {
			onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid progressToken %s: invalid length", pt))
			return fmt.Errorf("invalid progressToken %s: invalid length", pt)
		}
		v := math.Float64frombits(binary.LittleEndian.Uint64(b))
		p.ProgressToken = v
	default:
		onErrorResponse(w, http.StatusBadRequest, fmt.Sprintf("invalid progressToken %s: unknown type %s", pt, originalPtType))
		return fmt.Errorf("invalid progressToken %s: unknown type %s", pt, originalPtType)
	}

	backendName := parts[2]
	backend, err := m.getBackendForRoute(s.route, backendName)
	if err != nil {
		onErrorResponse(w, http.StatusNotFound, fmt.Sprintf("unknown backend %s", backendName))
		return fmt.Errorf("%w: unknown backend %s in progressToken %s", errBackendNotFound, backendName, pt)
	}
	cse := s.getCompositeSessionEntry(backend.Name)
	// Send the request to the MCP backend listener.
	param, _ := json.Marshal(p)
	req.Params = param
	if m.l.Enabled(ctx, slog.LevelDebug) {
		logger := m.l.With(slog.String("method", req.Method), slog.Any("session", cse),
			slog.String("original_progress_token", originalPt), slog.String("progress_token_type", originalPtType))
		logger.Debug("Routing to backend")
	}
	if span != nil {
		span.RecordRouteToBackend(backendName, string(cse.sessionID), false)
	}
	return m.invokeAndProxyResponse(ctx, s, w, backend, cse, req)
}

// invokeAndProxyResponse invokes the given JSON-RPC request to the given backend and proxies the response back to the client
// via w ResponseWriter.
func (m *MCPProxy) invokeAndProxyResponse(ctx context.Context, s *session, w http.ResponseWriter, backend filterapi.MCPBackend, sess *compositeSessionEntry, req *jsonrpc.Request) error {
	resp, err := m.invokeJSONRPCRequest(ctx, s.route, backend, sess, req)
	if err != nil {
		onErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("call to %s failed: %v", backend.Name, err))
		return err
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			onErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("call to %s failed and failed to read body: %v", backend.Name, err))
			return err
		}
		onErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("call to %s failed with status code %d, body=%s", backend.Name, resp.StatusCode, string(body)))
		return errors.New("tool call failed with non-200 status code")
	}
	copyProxyHeaders(resp, w)
	w.Header().Set(sessionIDHeader, string(s.clientGatewaySessionID()))
	m.proxyResponseBody(ctx, s, w, resp, req, backend)
	return nil
}

// addMCPHeaders adds the MCP metadata headers to the HTTP request.
func addMCPHeaders(httpReq *http.Request, msg jsonrpc.Message, routeName filterapi.MCPRouteName, backendName filterapi.MCPBackendName) {
	// MCP backend header is used for upstream MCP routing.
	httpReq.Header.Set(internalapi.MCPBackendHeader, backendName)
	httpReq.Header.Set(internalapi.MCPRouteHeader, routeName)
	if mcpReq, ok := msg.(*jsonrpc.Request); ok && mcpReq != nil {
		// MCP request headers are used to populate information in the envoy filter metadata.
		httpReq.Header.Set(internalapi.MCPMetadataHeaderRequestID, fmt.Sprintf("%v", mcpReq.ID.Raw()))
		httpReq.Header.Set(internalapi.MCPMetadataHeaderMethod, mcpReq.Method)
	}
}

type (
	// broadCastResponse represents the response from a backend along with the backend name.
	//
	// Used in sendToAllBackendsAndAggregateResponses.
	broadCastResponse[T any] struct {
		backendName string
		res         T
	}
	// broadCastResponseMergeFn is a function that merges multiple broadCastResponse into a single response type.
	//
	// Used in sendToAllBackendsAndAggregateResponses.
	broadCastResponseMergeFn[T any] func(*session, []broadCastResponse[T]) T
)

// sendToAllBackendsAndAggregateResponses is a generic function that can be used for handling all "list" variant
// JSON-RPC methods that require sending the request to all backends and aggregating the responses.
//
// The mergeFn is used to merge the responses from all backends into a single response that will be sent back to the client.
func sendToAllBackendsAndAggregateResponses[responseType any, paramsType mcp.Params](ctx context.Context, m *MCPProxy, w http.ResponseWriter, s *session, request *jsonrpc.Request, p paramsType, mergeFn broadCastResponseMergeFn[responseType], span tracing.MCPSpan) error {
	encoded, _ := json.Marshal(p)
	request.Params = encoded
	backendMsgs := s.sendToAllBackends(ctx, http.MethodPost, request, span)
	return sendToAllBackendsAndAggregateResponsesImpl(ctx, backendMsgs, m, w, s, request, mergeFn)
}

// sendToAllBackendsAndAggregateResponsesImpl is the implementation of sendToAllBackendsAndAggregateResponses for better testability.
func sendToAllBackendsAndAggregateResponsesImpl[responseType any](ctx context.Context, events <-chan *sseEvent, m *MCPProxy, w http.ResponseWriter, s *session, request *jsonrpc.Request, mergeFn broadCastResponseMergeFn[responseType]) error {
	logger := m.l.With(slog.String("method", request.Method), slog.String("client_gateway_session_id", string(s.clientGatewaySessionID())))

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set(sessionIDHeader, string(s.clientGatewaySessionID()))
	w.WriteHeader(http.StatusOK)
	var responses []broadCastResponse[responseType]
	for event := range events {
		// Update backend last event id and regenerate event ID.
		s.setLastEventID(event.backend, event.id)
		event.id = s.lastEventID()
		if l := len(event.messages); l != 0 {
			// Since the "response" is always the last message in the SSE stream per backend,
			// we can just check the last message to see if it's a response to the original request.
			if respMsg, ok := event.messages[l-1].(*jsonrpc.Response); ok && respMsg.ID == request.ID {
				switch {
				case respMsg.Error != nil:
					logger.Error("error response from backend", slog.String("backend", event.backend), slog.Any("error", respMsg.Error))
				case respMsg.Result != nil: // Empty result is valid, for example set/loggingLevel returns empty result from some backends.
					var result responseType
					if err := json.Unmarshal(respMsg.Result, &result); err != nil {
						// Partial failure, log and ignore this backend's response so that it won't affect the overall response.
						logger.Error("failed to unmarshal response from backend. Ignoring this backend's response",
							slog.String("backend", event.backend), slog.String("error", err.Error()), slog.String("result", string(respMsg.Result)))
					} else {
						responses = append(responses, broadCastResponse[responseType]{backendName: event.backend, res: result})
					}
				}
				// Regardless of whether it's error or success response, we need to remove it from the event messages so that
				// we can send back to the client only one merged response below.
				event.messages = event.messages[:l-1]
			}
			// We need to write any remaining events to the client.
			for _, msg := range event.messages {
				if reqMsg, ok := msg.(*jsonrpc.Request); ok {
					if err := m.maybeServerToClientRequestModify(ctx, reqMsg, event.backend); err != nil {
						logger.Error("failed to modify server->client request", slog.String("error", err.Error()))
						onErrorResponse(w, http.StatusInternalServerError, fmt.Sprintf("failed to modify server->client request: %v", err))
						return fmt.Errorf("failed to modify server->client request: %w", err)
					}
				}
			}
			if len(event.messages) > 0 {
				event.writeAndMaybeFlush(w)
			}
		}
	}

	mergedResp := mergeFn(s, responses)
	encodedResp, err := json.Marshal(mergedResp)
	if err != nil {
		return fmt.Errorf("failed to marshal response: %w", err)
	}
	event := sseEvent{
		event: "message",
		// TODO: *Maybe* this should be a last event ID that includes all backends' last event IDs.
		// 	However, directly using s.lastEventID() is not correct at the moment since the last event ID
		// 	should have been already used in the previous events sent to the client. So we need to update
		//	the last-event-id construction logic to be able to, for example, send the last even id with additional uuid suffix.
		// 	On the other hand, this is the "end" of the SSE stream for this request, so the client probably won't
		// 	really care about the last event ID here.
		id:       uuid.NewString(),
		messages: []jsonrpc.Message{&jsonrpc.Response{ID: request.ID, Result: encodedResp}},
	}
	event.writeAndMaybeFlush(w)
	return nil
}

// parseParamsAndMaybeStartSpan parses the params from the JSON-RPC request and starts a tracing span if params is non-nil.
func parseParamsAndMaybeStartSpan[paramType mcp.Params](ctx context.Context, m *MCPProxy, req *jsonrpc.Request, p paramType, headers http.Header) (tracing.MCPSpan, error) {
	if req.Params == nil {
		return nil, nil
	}
	err := json.Unmarshal(req.Params, &p)
	if err != nil {
		m.l.Error("Failed to unmarshal params", slog.String("method", req.Method), slog.String("error", err.Error()))
		return nil, err
	}

	span := m.tracer.StartSpanAndInjectMeta(ctx, req, p, headers)
	return span, nil
}

// handleToolsListRequest handles the "tools/list" JSON-RPC method.
//
// This aggregates and returns the list of tools from all backends.
func (m *MCPProxy) handleToolsListRequest(ctx context.Context, s *session, w http.ResponseWriter, req *jsonrpc.Request, p *mcp.ListToolsParams, span tracing.MCPSpan) error {
	// TODO: use cursor for pagination, but in spec it's "SHOULD" not "MUST".
	return sendToAllBackendsAndAggregateResponses(ctx, m, w, s, req, p, m.mergeToolsList, span)
}

// handleResourceListRequest handles the "resources/list" JSON-RPC method.
// This aggregates and returns the list of resources from all backends.
func (m *MCPProxy) handleResourceListRequest(ctx context.Context, s *session, w http.ResponseWriter, req *jsonrpc.Request, p *mcp.ListResourcesParams, span tracing.MCPSpan) error {
	// TODO: use cursor for pagination, but in spec it's "SHOULD" not "MUST".
	return sendToAllBackendsAndAggregateResponses(ctx, m, w, s, req, p, m.mergeResourceList, span)
}

// handleResourcesTemplatesListRequest handles the "resources/templates/list" JSON-RPC method.
func (m *MCPProxy) handleResourcesTemplatesListRequest(ctx context.Context, s *session, w http.ResponseWriter, req *jsonrpc.Request, p *mcp.ListResourceTemplatesParams, span tracing.MCPSpan) error {
	// TODO: use cursor for pagination, but in spec it's "SHOULD" not "MUST".
	return sendToAllBackendsAndAggregateResponses(ctx, m, w, s, req, p, m.mergeResourcesTemplateList, span)
}

// handlePromptListRequest handles the "prompts/list" JSON-RPC method.
// This aggregates and returns the list of prompts from all backends.
func (m *MCPProxy) handlePromptListRequest(ctx context.Context, s *session, w http.ResponseWriter, req *jsonrpc.Request, p *mcp.ListPromptsParams, span tracing.MCPSpan) error {
	// TODO: use cursor for pagination, but in spec it's "SHOULD" not "MUST".
	return sendToAllBackendsAndAggregateResponses(ctx, m, w, s, req, p, m.mergePromptsList, span)
}

// handleSetLoggingLevel handles the "logging/setLevel" JSON-RPC method.
func (m *MCPProxy) handleSetLoggingLevel(ctx context.Context, s *session, w http.ResponseWriter, originalRequest *jsonrpc.Request, p *mcp.SetLoggingLevelParams, span tracing.MCPSpan) error {
	// TODO: maybe some backend doesn't support set logging level, so filter out such backends.
	return sendToAllBackendsAndAggregateResponses(ctx, m, w, s, originalRequest, p, func(*session, []broadCastResponse[any]) any {
		return emptyJSONRPCMessage
	}, span)
}

// mergeToolsList merges the list of tools from all backends and prepare the response message to be sent back to the client.
func (m *MCPProxy) mergeToolsList(s *session, responses []broadCastResponse[mcp.ListToolsResult]) mcp.ListToolsResult {
	resp := mcp.ListToolsResult{}
	route := m.routes[s.route]
	if route == nil {
		// This should never happen as the route must have been validated when the session is created.
		return resp
	}

	// Aggregate the tools from all responses.
	// A backend specific prefix is added to the tool name to avoid name collision.
	// The tools are filtered based on the toolFilters configured for each backend.
	for _, r := range responses {
		selector := route.toolSelectors[r.backendName]
		for _, tool := range r.res.Tools {
			if selector != nil && !selector.allows(tool.Name) {
				continue
			}
			tool.Name = downstreamResourceName(tool.Name, r.backendName)
			resp.Tools = append(resp.Tools, tool)
		}
	}

	return resp
}

// mergeResourceList merges the list of resources from all backends and prepare the response message to be sent back to the client.
func (m *MCPProxy) mergeResourceList(_ *session, responses []broadCastResponse[mcp.ListResourcesResult]) mcp.ListResourcesResult {
	// Aggregate the resources from all responses with some logic to match the actual proxy behavior.
	// TODO: do we need a more sophisticated merging logic here?
	// TODO: how to handle NextCursor?
	resp := mcp.ListResourcesResult{Resources: make([]*mcp.Resource, 0)}
	for _, r := range responses {
		for _, res := range r.res.Resources {
			res.Name = downstreamResourceName(res.Name, r.backendName)
			resp.Resources = append(resp.Resources, res)
		}
	}
	return resp
}

// mergeResourcesTemplateList merges the list of resource templates from all backends and prepare the response message to be sent back to the client.
func (m *MCPProxy) mergeResourcesTemplateList(_ *session, responses []broadCastResponse[mcp.ListResourceTemplatesResult]) mcp.ListResourceTemplatesResult {
	resp := mcp.ListResourceTemplatesResult{ResourceTemplates: make([]*mcp.ResourceTemplate, 0)}
	for _, r := range responses {
		for _, res := range r.res.ResourceTemplates {
			res.Name = downstreamResourceName(res.Name, r.backendName)
			resp.ResourceTemplates = append(resp.ResourceTemplates, res)
		}
	}
	return resp
}

// mergePromptsList merges the list of prompts from all backends and prepare the response message to be sent back to the client.
func (m *MCPProxy) mergePromptsList(_ *session, responses []broadCastResponse[mcp.ListPromptsResult]) mcp.ListPromptsResult {
	// Aggregate the resources from all responses with some logic to match the actual proxy behavior.
	aggregatedResponse := mcp.ListPromptsResult{Prompts: make([]*mcp.Prompt, 0)}
	for _, r := range responses {
		for _, res := range r.res.Prompts {
			res.Name = downstreamResourceName(res.Name, r.backendName)
			aggregatedResponse.Prompts = append(aggregatedResponse.Prompts, res)
		}
	}
	return aggregatedResponse
}

// handleNotificationsRootsListChanged handles the "notifications/roots/list_changed" JSON-RPC method.
func (m *MCPProxy) handleNotificationsRootsListChanged(ctx context.Context, s *session, w http.ResponseWriter, req *jsonrpc.Request, span tracing.MCPSpan) error {
	// Since notifications request doesn't expect a response, we can just send the request to all backends and return 202 Accepted per the spec.
	eventChan := s.sendToAllBackends(ctx, http.MethodPost, req, span)
	w.Header().Set(sessionIDHeader, string(s.clientGatewaySessionID()))
	w.WriteHeader(http.StatusAccepted)
	// Just wait for all requests to complete and return 202 Accepted. There should be events sent from the backends per the spec.
	<-eventChan
	return nil
}
