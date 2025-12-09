// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
)

const (
	// https://github.com/modelcontextprotocol/go-sdk/blob/392f719bd1956e7601cf85f7a9b24c7010cffb4c/mcp/streamable.go#L31-L32

	sessionIDHeader         = "mcp-session-id"
	protocolVersionHeader   = "mcp-protocol-version"
	protocolVersion20250618 = "2025-06-18"

	lastEventIDHeader = "Last-Event-Id"
)

// session implements [Session].
type session struct {
	id                 secureClientToGatewaySessionID
	route              string
	proxy              *MCPProxy
	mu                 sync.RWMutex
	perBackendSessions map[filterapi.MCPBackendName]*compositeSessionEntry
}

// Close implements [io.Closer.Close].
func (s *session) Close() error {
	for backendName, sess := range s.perBackendSessions {
		sessionID := sess.sessionID
		if sessionID == "" {
			// Stateless backend, nothing to do.
			continue
		}
		httpClient := &http.Client{}
		// Make DELETE request to the MCP server to close the session.
		backend, err := s.proxy.getBackendForRoute(s.route, backendName)
		if err != nil {
			s.proxy.l.Error("failed to get backend for route",
				slog.String("backend", backendName),
				slog.String("session_id", string(sessionID)),
				slog.String("error", err.Error()),
			)
			continue
		}
		req, err := http.NewRequest(http.MethodDelete, s.proxy.mcpEndpointForBackend(backend), nil)
		if err != nil {
			s.proxy.l.Error("failed to create DELETE request to MCP server to close session",
				slog.String("backend", backendName),
				slog.String("session_id", string(sessionID)),
				slog.String("error", err.Error()),
			)
			continue
		}
		addMCPHeaders(req, nil, s.route, backendName)
		req.Header.Set(sessionIDHeader, sessionID.String())
		resp, err := httpClient.Do(req)
		if err != nil {
			s.proxy.l.Error("failed to send DELETE request to MCP server to close session",
				slog.String("backend", backendName),
				slog.String("session_id", string(sessionID)),
				slog.String("error", err.Error()),
			)
			continue
		}
		_ = resp.Body.Close()
		if status := resp.StatusCode; (status < 200 || status >= 300) &&
			// E.g., learn-microsoft returns 405 Method Not Allowed for DELETE requests even though the session ID exists.
			status != http.StatusMethodNotAllowed &&
			// Some stateless backends may return 404 Not Found if they don't track sessions.
			status != http.StatusNotFound {
			s.proxy.l.Error("failed to close MCP session",
				slog.String("backend", backendName),
				slog.String("session_id", string(sessionID)),
				slog.Int("status_code", resp.StatusCode),
			)
		}
	}
	return nil
}

// clientGatewaySessionID returns the ID of this session in MCP in the client<>Gateway direction.
func (s *session) clientGatewaySessionID() secureClientToGatewaySessionID {
	return s.id
}

func (s *session) getCompositeSessionEntry(backend filterapi.MCPBackendName) *compositeSessionEntry {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.perBackendSessions[backend]
	if !ok {
		s.proxy.l.Warn("attempt to get session for unknown backend in session",
			slog.String("backend", backend))
		return nil
	}
	return entry
}

func (s *session) setLastEventID(backend filterapi.MCPBackendName, lastEventID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	entry, ok := s.perBackendSessions[backend]
	if !ok {
		s.proxy.l.Warn("attempt to set last event ID for unknown backend in session",
			slog.String("backend", backend))
		return
	}
	entry.lastEventID = lastEventID
}

func (s *session) lastEventID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var b strings.Builder
	for _, entry := range s.perBackendSessions {
		_, _ = b.WriteString(entry.backendName)
		_, _ = b.WriteString(":")
		_, _ = b.WriteString(base64.StdEncoding.EncodeToString([]byte(entry.lastEventID)))
		_, _ = b.WriteString(",")
	}
	lastEventID := b.String()[:b.Len()-1] // string the trailing ','.
	if s.proxy != nil && s.proxy.sessionCrypto != nil {
		encrypted, err := s.proxy.sessionCrypto.Encrypt(lastEventID)
		if err != nil {
			s.proxy.l.Error("failed to encrypt last event ID", slog.String("error", err.Error()))
			return ""
		}
		return encrypted
	}

	return lastEventID
}

var (
	envoyAIGatewayServerToClientPingRequestIDPrefix         = "aigw-server-to-client-ping"
	envoyAIGatewayServerToClientToolsChangedRequestIDPrefix = "aigw-server-to-client-tools-changed"
	pingParam, _                                            = json.Marshal(&mcpsdk.PingParams{})
	toolsChangedParam, _                                    = json.Marshal(&mcpsdk.ToolListChangedParams{})
)

func newHeartBeatPingMessage() *jsonrpc.Request {
	// Assign a unique ID to each ping request so that the client can respond to each ping.
	id, _ := jsonrpc.MakeID(envoyAIGatewayServerToClientPingRequestIDPrefix + uuid.NewString())
	return &jsonrpc.Request{
		ID:     id,
		Method: "ping",
		Params: pingParam,
	}
}

func newToolListChangedMessage() *jsonrpc.Request {
	id, _ := jsonrpc.MakeID(envoyAIGatewayServerToClientToolsChangedRequestIDPrefix + uuid.NewString())
	return &jsonrpc.Request{
		ID:     id,
		Method: "notifications/tools/list_changed",
		Params: toolsChangedParam,
	}
}

// streamNotifications streams notifications from all backends in this session to the given writer.
func (s *session) streamNotifications(ctx context.Context, w http.ResponseWriter, toolChangeSignaler changeSignaler) error {
	backendMsgs := s.sendToAllBackends(ctx, http.MethodGet, nil, nil)

	// Create a ticker for periodic heartbeat events to avoid HTTP timeouts.
	// This also helps unblock Goose at startup - it looks like Goose is waiting for the first SSE event before proceeding.
	//
	// TODO: no idea exactly why this is necessary. Goose shouldn't block on the first event.

	var (
		heartbeats      <-chan time.Time
		heartbeatTicker *time.Ticker
	)
	if heartbeatInterval > 0 {
		heartbeatTicker = time.NewTicker(heartbeatInterval)
		defer heartbeatTicker.Stop()
		heartbeats = heartbeatTicker.C
	} else {
		heartbeats = make(chan time.Time) // never ticks
	}

	// Eagerly send an initial heartbeat event to unblock Goose
	heartBeatEvent := &sseEvent{event: "message", messages: []jsonrpc.Message{newHeartBeatPingMessage()}}
	heartBeatEvent.writeAndMaybeFlush(w)

	for {
		select {
		// events received from the upstream MCP backends
		case event, ok := <-backendMsgs:
			if !ok {
				// Channel closed, all backends have finished.
				return nil
			}

			prev := event.id
			s.setLastEventID(event.backend, event.id)
			event.id = s.lastEventID()
			if s.proxy.l.Enabled(ctx, slog.LevelDebug) {
				s.proxy.l.Debug("Changed event ID", slog.String("backend", event.backend),
					slog.String("prev_event_id", prev),
					slog.String("event_id", event.id))
			}
			for _, _msg := range event.messages {
				// Maybe the server->client request made during the notification handling needs to be modified.
				if msg, ok := _msg.(*jsonrpc.Request); ok {
					if err := s.proxy.maybeServerToClientRequestModify(ctx, msg, event.backend); err != nil {
						s.proxy.l.Error("failed to modify server->client request", slog.String("error", err.Error()))
						continue
					}
				}
				s.proxy.recordResponse(ctx, _msg)
			}
			event.writeAndMaybeFlush(w)
			// Reset the heartbeat ticker so that the next heartbeat will be sent after the full interval.
			// This avoids sending heartbeats too frequently when there are events.
			if heartbeatTicker != nil {
				heartbeatTicker.Reset(heartbeatInterval)
			}
		// toolChangeSignaler will trigger when the tools configured in the MCP routes change.
		// This is not related to upstream MCP server changes, but to the tools configured in the gateway.
		case <-toolChangeSignaler.Watch():
			toolChangeEvent := &sseEvent{event: "message", messages: []jsonrpc.Message{newToolListChangedMessage()}}
			toolChangeEvent.writeAndMaybeFlush(w)
			// Reset the heartbeat ticker so that the next heartbeat will be sent after the full interval.
			// This avoids sending heartbeats too frequently when there are events.
			if heartbeatTicker != nil {
				heartbeatTicker.Reset(heartbeatInterval)
			}
		case <-heartbeats:
			heartBeatEvent := &sseEvent{event: "message", messages: []jsonrpc.Message{newHeartBeatPingMessage()}}
			heartBeatEvent.writeAndMaybeFlush(w)
		case <-ctx.Done():
			// Context cancelled, stop streaming.
			return ctx.Err()
		}
	}
}

// heartbeatInterval is computed at startup to avoid the locks in os.Getenv() to be called on the request path.
var heartbeatInterval = getHeartbeatInterval(1 * time.Minute)

// getHeartbeatInterval returns the heartbeat interval configured via the MCP_HEARTBEAT_INTERVAL environment variable.
// If the environment variable is not set or invalid, it returns the default value of 1 minute.
// This value is intentionally hidden under an environment variable as it is unclear if it is generally useful.
func getHeartbeatInterval(def time.Duration) time.Duration {
	hbi, err := time.ParseDuration(os.Getenv("MCP_PROXY_HEARTBEAT_INTERVAL"))
	if err != nil {
		return def
	}
	return hbi
}

// sendToAllBackends sends an HTTP request to all backends in this session and returns a channel that streams
// the response events from all backends.
func (s *session) sendToAllBackends(ctx context.Context, httpMethod string, request *jsonrpc.Request, span tracing.MCPSpan) <-chan *sseEvent {
	var (
		logger      = s.proxy.l
		backendMsgs = make(chan *sseEvent, 200)
		wg          sync.WaitGroup
	)

	wg.Add(len(s.perBackendSessions))
	for backendName, cse := range s.perBackendSessions {
		sessionID := cse.sessionID
		go func() {
			defer wg.Done()
			backend, err := s.proxy.getBackendForRoute(s.route, backendName)
			if err != nil {
				logger.Error("failed to get backend for route",
					slog.String("backend", backendName),
					slog.String("session_id", string(sessionID)),
					slog.String("error", err.Error()),
				)
				return
			}
			err = s.sendRequestPerBackend(ctx, backendMsgs, s.route, backend, cse, httpMethod, request)
			if err != nil {
				if errors.Is(err, context.Canceled) {
					return
				}
				logger.Error("failed to collect messages from MCP backend",
					slog.String("backend", backendName),
					slog.String("session_id", string(sessionID)),
					slog.String("error", err.Error()),
				)
			}
			if span != nil {
				span.RecordRouteToBackend(backendName, string(sessionID), false)
			}
		}()
	}
	go func() {
		wg.Wait()
		close(backendMsgs)
	}()

	return backendMsgs
}

// sendRequestPerBackend sends an HTTP request to the given backend and streams the response events to eventChan.
func (s *session) sendRequestPerBackend(ctx context.Context, eventChan chan<- *sseEvent, routeName filterapi.MCPRouteName, backend filterapi.MCPBackend, cse *compositeSessionEntry,
	httpMethod string, request *jsonrpc.Request,
) error {
	var body io.Reader
	if request != nil {
		encodedReq, err := jsonrpc.EncodeMessage(request)
		if err != nil {
			return fmt.Errorf("failed to encode request: %w", err)
		}
		body = bytes.NewReader(encodedReq)
	}

	req, err := http.NewRequestWithContext(ctx, httpMethod, s.proxy.mcpEndpointForBackend(backend), body)
	if err != nil {
		return fmt.Errorf("failed to create GET request: %w", err)
	}
	sessionID := cse.sessionID.String()
	addMCPHeaders(req, request, routeName, backend.Name)
	req.Header.Set(protocolVersionHeader, protocolVersion20250618)
	req.Header.Set(sessionIDHeader, cse.sessionID.String())
	if httpMethod != http.MethodGet {
		req.Header.Set("Content-type", "application/json")
	}
	req.Header.Set("Accept", "text/event-stream, application/json")
	req.Header.Set("Accept-encoding", "gzip, br, zstd, deflate")

	client := http.Client{Timeout: 1200 * time.Second} // Reduce and support for reconnect.
	if lastEventID := cse.lastEventID; lastEventID != "" {
		req.Header.Set(lastEventIDHeader, lastEventID)
	}
	if s.proxy.l.Enabled(ctx, slog.LevelDebug) {
		args := []any{
			slog.String("backend", backend.Name),
			slog.String("session_id", sessionID),
			slog.String("http_method", httpMethod),
			slog.String("url", req.URL.String()),
		}
		if request != nil {
			args = append(args,
				slog.String("mcp_request_method", request.Method),
				slog.Any("mcp_request_id", request.ID),
				slog.Any("mcp_request_params", request.Params),
			)
		}

		if lastEventID := cse.lastEventID; lastEventID != "" {
			args = append(args, slog.String("last_event_id", lastEventID))
		}
		s.proxy.l.Debug("sending MCP request", args...)
	}
	httpResp, err := client.Do(req)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return fmt.Errorf("failed to send GET request: %w", err)
	}

	switch httpResp.StatusCode {
	case http.StatusNoContent, http.StatusMethodNotAllowed, http.StatusAccepted:
		// No notifications.
		_ = httpResp.Body.Close()
		return nil
	case http.StatusOK:
	default:
		body, _ := io.ReadAll(httpResp.Body)
		_ = httpResp.Body.Close()
		return fmt.Errorf("MCP GET request failed with status code %d, body=%s", httpResp.StatusCode, string(body))
	}

	defer func() {
		_ = httpResp.Body.Close()
	}()

	if httpResp.Header.Get("Content-Type") == "application/json" {
		// This is not an SSE response, but only a single JSON response. Convert it as an event and
		// send it to the channel.
		var respBody []byte
		respBody, err = io.ReadAll(httpResp.Body)
		if err != nil {
			return fmt.Errorf("failed to read MCP response body: %w", err)
		}
		var msg jsonrpc.Message
		msg, err = jsonrpc.DecodeMessage(respBody)
		if err != nil {
			return fmt.Errorf("failed to decode jsonrpc message from MCP response body: %w", err)
		}
		eventChan <- &sseEvent{
			backend:  backend.Name,
			event:    "message",
			id:       "", // No event ID in this case.
			messages: []jsonrpc.Message{msg},
		}
		return nil
	}

	// io.Copy won't flush until the end, which doesn't happen for streaming responses.
	// So we need to read the body in chunks and flush after each chunk.
	parser := newSSEEventParser(httpResp.Body, backend.Name)
	for {
		var event *sseEvent
		event, err = parser.next()
		// TODO: handle reconnect. We need to re-arrange the event ID so that it will also contain the backend name and the original session ID.
		// 	Since event ID can be arbitrary string, we can shove each backend's last even ID into the event ID just like the session ID.
		//
		// If it happens on the upstream, we can simply just reconnect here using the last-even-id by parsing the
		// events we have seen so far. This is because the downstream is still connected.
		//
		// In any case, the reconnect support here must be in line with proxyResponseBody's reconnect logic when that happens.
		if event != nil {
			eventChan <- event
		}
		if err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) ||
				// Unexpected EOF can happen when the server closes the connection, e.g. Envoy receives signal
				// or the upstream closes the connection. Either way, the error is not recoverable or not worth
				// the logging.
				errors.Is(err, io.ErrUnexpectedEOF) {
				break
			}
			_ = httpResp.Body.Close()
			return fmt.Errorf("failed to read MCP GET response body: %w", err)
		}
	}
	return nil
}

type (
	// clientToGatewaySessionID is the ID of a session in MCP in the client<>Gateway direction.
	//
	// SessionID can be any ascii string.
	// https://modelcontextprotocol.io/specification/2025-06-18/basic/transports#session-management
	//
	// We use the following format to encapsulate multiple MCP sessions:
	//
	//	{route name}@{subject}@{mcp-backend-name1}:{base64(mcp-session-id1)},...,{mcp-backend-nameN}:{base64(mcp-session-idN)}
	//
	// For example:
	//
	//	"some-awesome-route@mcp-user@backend1:MTIzNDU2,backend2:NjU0MzIx"
	//
	// The reason on using base64 is that MCP session IDs can be arbitrary binary data, so without it, the separator characters
	// (comma and colon) may conflict with the session ID content.
	//
	// The '{subject}@' prefix is optional and is only included if there is an authenticated token with a subject.
	// Using the subject in the session ID helps with preventing session hijacking attacks:
	// https://modelcontextprotocol.io/specification/2025-06-18/basic/security_best_practices#session-hijacking
	clientToGatewaySessionID string

	// secureClientToGatewaySessionID is an encrypted clientToGatewaySessionID.
	secureClientToGatewaySessionID string

	// clientToGatewaySessionID is the last event ID of a session in MCP in the client<>Gateway direction.
	// We use the following format to encapsulate multiple MCP sessions:
	//
	//	{mcp-backend-name1}:{base64(last-event-id1)},...,{mcp-backend-nameN}:{base64(last-event-idN)}
	//
	// For example:
	//
	//	backend1:MTIzNDU2,backend2:NjU0MzIx"
	clientToGatewayEventID string

	// secureClientToGatewayEventID is an encrypted clientToGatewayEventID.
	secureClientToGatewayEventID string

	// gatewayToMCPServerSessionID is the ID of a session in MCP in the Gateway<>MCPServer direction.
	gatewayToMCPServerSessionID string

	// compositeSessionEntry is used to track the session and last event ID for each backend in a composite session.
	compositeSessionEntry struct {
		backendName string
		sessionID   gatewayToMCPServerSessionID
		lastEventID string
	}
)

// String implements fmt.Stringer.
func (g gatewayToMCPServerSessionID) String() string { return string(g) }

// String implements fmt.Stringer.
func (c clientToGatewaySessionID) String() string { return string(c) }

// backendSessionIDs parses the SessionID and returns a map of MCP backend name to MCP session ID.
func (c clientToGatewaySessionID) backendSessionIDs() (map[filterapi.MCPBackendName]*compositeSessionEntry, string, error) {
	perBackendSessionIDs := make(map[filterapi.MCPBackendName]*compositeSessionEntry)
	parts := strings.Split(string(c), "@")
	if len(parts) != 3 {
		return nil, "", fmt.Errorf("invalid session ID: missing '@' separator")
	}
	route := parts[0]
	// Ignore strip the subject part for now.
	_ = parts[1]
	backendSessions := parts[2]
	for _, part := range strings.Split(backendSessions, ",") {
		colon := strings.Index(part, ":")
		if colon < 0 {
			return nil, "", fmt.Errorf("invalid session ID: missing ':' separator in backend session ID part %q", part)
		}
		backendName := part[:colon]
		if backendName == "" {
			return nil, "", fmt.Errorf("invalid session ID: empty backend name in part %q", part)
		}
		var sessionID gatewayToMCPServerSessionID
		sessionIDBas64 := part[colon+1:]
		if sessionIDBas64 != "" { // Some servers are stateless hence no (==empty) session ID.
			decoded, err := base64.StdEncoding.DecodeString(sessionIDBas64)
			if err != nil {
				err = fmt.Errorf("invalid session ID: failed to base64 decode session ID in part %q: %w", part, err)
				return nil, "", err
			}
			sessionID = gatewayToMCPServerSessionID(decoded)
		}
		perBackendSessionIDs[backendName] = &compositeSessionEntry{
			backendName: backendName,
			sessionID:   sessionID,
		}
	}
	return perBackendSessionIDs, route, nil
}

// String implements fmt.Stringer.
func (s secureClientToGatewaySessionID) String() string { return string(s) }

// clientToGatewaySessionIDFromEntries returns the ID of this session in MCP in the client<>Gateway direction.
func clientToGatewaySessionIDFromEntries(subject string, entries []compositeSessionEntry, routeName string) clientToGatewaySessionID {
	var b strings.Builder
	_, _ = b.WriteString(subject)
	_, _ = b.WriteString("@")
	for _, entry := range entries {
		_, _ = b.WriteString(entry.backendName)
		_, _ = b.WriteString(":")
		_, _ = b.WriteString(base64.StdEncoding.EncodeToString([]byte(entry.sessionID)))
		_, _ = b.WriteString(",")
	}
	sessionID := b.String()[:b.Len()-1] // string the trailing ','.
	sessionID = routeName + "@" + sessionID
	return clientToGatewaySessionID(sessionID)
}

func (e clientToGatewayEventID) backendEventIDs() map[filterapi.MCPBackendName]string {
	result := map[filterapi.MCPBackendName]string{}
	parts := strings.Split(string(e), ",")
	for _, part := range parts {
		colon := strings.Index(part, ":")
		if colon < 0 {
			continue
		}
		backendName := part[:colon]
		if backendName == "" {
			continue
		}
		eventID := part[colon+1:]
		if eventID != "" {
			decoded, err := base64.StdEncoding.DecodeString(eventID)
			if err != nil {
				continue
			}
			eventID = string(decoded)
		}
		result[backendName] = eventID
	}
	return result
}
