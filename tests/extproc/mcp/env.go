// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/testing/testotel"
	"github.com/envoyproxy/ai-gateway/tests/internal/testenvironment"
	"github.com/envoyproxy/ai-gateway/tests/internal/testmcp"
)

// envoyConfig is the embedded Envoy configuration template.
//
//go:embed envoy.yaml
var envoyConfig string

// MCPSuite is a test suite for testing MCP client and server interactions using the server-implementation in testcmp package.
//
// TODO: move this to testmcp package so that we could reuse the same tests in the end-to-end tests.
type mcpEnv struct {
	client            *mcp.Client
	mux               sync.Mutex
	extProcMetricsURL string
	baseURL           string
	sessions          map[string]*mcpSession // ID -> session.
	writeTimeout      time.Duration
	collector         *testotel.OTLPCollector
}

// mcpSession wraps an MCP client session and its associated state for testing.
//
// See the comment on ProgressNotificationHandler in [MCPSuite.SetupSuite] for why we need this wrapper.
// Otherwise, we could pass the handler option directly to the [MCPSuite.newSession] call.
//
// **NOTE*** Do not add any direct access to the in-memory server session. Otherwise, the tests will result in
// not being able to run in end-to-end tests. The test code must solely operate through the client side sessions.
type mcpSession struct {
	session *mcp.ClientSession
	// TODO: merge them into one chan for simplicity?
	progressNotifications            chan *mcp.ProgressNotificationClientRequest
	promptListChangedNotifications   chan *mcp.PromptListChangedRequest
	resourceUpdatedNotifications     chan *mcp.ResourceUpdatedNotificationRequest
	resourceListChangedNotifications chan *mcp.ResourceListChangedRequest
	loggingNotification              chan *mcp.LoggingMessageRequest
	createMessageRequests            chan *mcp.CreateMessageRequest
	elicitRequests                   chan *mcp.ElicitRequest
}

const (
	mcpDefaultRootName = "test-root"
	mcpDefaultRootURI  = "foo://bar"
)

func requireNewMCPEnv(t *testing.T, forceJSONResponse bool, writeTimeout time.Duration, path string) *mcpEnv {
	collector := testotel.StartOTLPCollector()
	t.Cleanup(collector.Close)
	mcpConfig := &filterapi.MCPConfig{
		BackendListenerAddr: "http://127.0.0.1:9999",
		Routes: []filterapi.MCPRoute{
			{
				Name: "test-route",
				Backends: []filterapi.MCPBackend{
					{Name: "dumb-mcp-backend", Path: "/mcp"},
					{Name: "default-mcp-backend", Path: "/mcp"},
				},
			},
			{
				Name: "yet-another-route",
				Backends: []filterapi.MCPBackend{
					{
						Name: "default-mcp-backend", Path: "/mcp",
						// This shouldn't affect any other routes.
						ToolSelector: &filterapi.MCPToolSelector{Include: []string{"non-existent"}},
					},
					{Name: "dumb-mcp-backend", Path: "/mcp"},
				},
			},
			{
				Name: "awesome-route",
				Backends: []filterapi.MCPBackend{
					{Name: "dumb-mcp-backend", Path: "/mcp"},
				},
			},
		},
	}
	config, err := json.Marshal(filterapi.Config{MCPConfig: mcpConfig})
	require.NoError(t, err)

	env := testenvironment.StartTestEnvironment(t,
		func(_ testing.TB, _ io.Writer, ports map[string]int) {
			srv1 := testmcp.NewServer(&testmcp.Options{
				Port:              ports["ts1"],
				ForceJSONResponse: forceJSONResponse,
				DumbEchoServer:    false,
				WriteTimeout:      writeTimeout,
			})
			srv2 := testmcp.NewServer(&testmcp.Options{
				Port:              ports["ts2"],
				ForceJSONResponse: forceJSONResponse,
				DumbEchoServer:    true,
				WriteTimeout:      writeTimeout,
			})
			t.Cleanup(func() {
				_ = srv1.Close()
				_ = srv2.Close()
			})
		}, map[string]int{"ts1": 8080, "ts2": 8081, "special_listener": 9999},
		"", string(config), collector.Env(), envoyConfig, true, true,
		writeTimeout,
	)

	m := new(mcpEnv)
	m.collector = collector
	m.writeTimeout = writeTimeout
	m.extProcMetricsURL = fmt.Sprintf("http://localhost:%d/metrics", env.ExtProcAdminPort())
	m.baseURL = fmt.Sprintf("http://localhost:%d%s", env.EnvoyListenerPort(), path)

	m.client = mcp.NewClient(&mcp.Implementation{Name: "demo-http-client", Version: "0.1.0"}, &mcp.ClientOptions{
		// TODO: this is due to how the official go-sdk is designed. Notification is a per-session concept but
		// they force the handler to be per-client, which resulted in forcing us to do this multiplexing here.
		ProgressNotificationHandler: func(_ context.Context, request *mcp.ProgressNotificationClientRequest) {
			m.mux.Lock()
			defer m.mux.Unlock()
			if sess, ok := m.sessions[request.GetSession().ID()]; ok {
				t.Log("received progress notification for session ", request.GetSession().ID(), ": ", request.Params)
				sess.progressNotifications <- request
			} else {
				t.Fatalf("received progress notification for unknown session ID %q", request.GetSession().ID())
			}
		},
		PromptListChangedHandler: func(_ context.Context, request *mcp.PromptListChangedRequest) {
			m.mux.Lock()
			defer m.mux.Unlock()
			if sess, ok := m.sessions[request.GetSession().ID()]; ok {
				t.Log("received prompt notification for session ", request.GetSession().ID(), ": ", request.Params)
				sess.promptListChangedNotifications <- request
			} else {
				t.Fatalf("received prompt notification for unknown session ID %q", request.GetSession().ID())
			}
		},
		ResourceUpdatedHandler: func(_ context.Context, request *mcp.ResourceUpdatedNotificationRequest) {
			m.mux.Lock()
			defer m.mux.Unlock()
			if sess, ok := m.sessions[request.GetSession().ID()]; ok {
				t.Log("received resource updated notification for session ", request.GetSession().ID(), ": ", request.Params)
				sess.resourceUpdatedNotifications <- request
			} else {
				t.Fatalf("received resource updated notification for unknown session ID %q", request.GetSession().ID())
			}
		},
		ResourceListChangedHandler: func(_ context.Context, request *mcp.ResourceListChangedRequest) {
			m.mux.Lock()
			defer m.mux.Unlock()
			if sess, ok := m.sessions[request.GetSession().ID()]; ok {
				t.Log("received resource list changed notification for session ", request.GetSession().ID(), ": ", request.Params)
				sess.resourceListChangedNotifications <- request
			} else {
				t.Fatalf("received resource list changed notification for unknown session ID %q", request.GetSession().ID())
			}
		},

		LoggingMessageHandler: func(_ context.Context, request *mcp.LoggingMessageRequest) {
			m.mux.Lock()
			defer m.mux.Unlock()
			if sess, ok := m.sessions[request.GetSession().ID()]; ok {
				t.Log("received logging message for session ", request.GetSession().ID(), ": ", request.Params)
				sess.loggingNotification <- request
			} else {
				t.Fatalf("received logging message for unknown session ID %q", request.GetSession().ID())
			}
		},
		CreateMessageHandler: func(_ context.Context, request *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
			m.mux.Lock()
			defer m.mux.Unlock()
			if sess, ok := m.sessions[request.GetSession().ID()]; ok {
				t.Log("received create message request for session ", request.GetSession().ID(), ": ", request.Params)
				sess.createMessageRequests <- request
			} else {
				t.Fatalf("received create message request for unknown session ID %q", request.GetSession().ID())
			}
			return &mcp.CreateMessageResult{
				Content: &mcp.TextContent{
					Text: "Just plug Envoy into MCP, add some AI magic, and boom — you’ve got yourself an MCP Gateway.",
				},
				Model: "mcp-gatewayinator-3000",
			}, nil
		},
		ElicitationHandler: func(_ context.Context, request *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			m.mux.Lock()
			defer m.mux.Unlock()
			if sess, ok := m.sessions[request.GetSession().ID()]; ok {
				t.Log("received elicit request for session ", request.GetSession().ID(), ": ", request.Params)
				sess.elicitRequests <- request
			} else {
				t.Fatalf("received elicit request for unknown session ID %q", request.GetSession().ID())
			}
			return &mcp.ElicitResult{
				Action: "accept",
				Content: map[string]any{
					"name":  "Rob",
					"email": "rob@foo.com",
				},
			}, nil
		},
	})
	m.client.AddRoots(&mcp.Root{
		Name: mcpDefaultRootName,
		URI:  mcpDefaultRootURI,
	})
	return m
}

// newSession creates a new MCP client session and registers it for progress notifications.
func (m *mcpEnv) newSession(t *testing.T) *mcpSession {
	ret := &mcpSession{
		progressNotifications:            make(chan *mcp.ProgressNotificationClientRequest, 100),
		promptListChangedNotifications:   make(chan *mcp.PromptListChangedRequest, 100),
		resourceUpdatedNotifications:     make(chan *mcp.ResourceUpdatedNotificationRequest, 100),
		resourceListChangedNotifications: make(chan *mcp.ResourceListChangedRequest, 100),
		loggingNotification:              make(chan *mcp.LoggingMessageRequest, 100),
		createMessageRequests:            make(chan *mcp.CreateMessageRequest, 100),
		elicitRequests:                   make(chan *mcp.ElicitRequest, 100),
	}
	var err error
	ret.session, err = m.client.Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: m.baseURL}, nil)
	require.NoError(t, err)
	span := m.collector.TakeSpan()
	t.Log("created new MCP session with ID ", ret.session.ID(), ", first span: ", span.String())
	requireMCPSpan(t, span, "Initialize", map[string]string{
		"mcp.method.name":    "initialize",
		"mcp.client.name":    "demo-http-client",
		"mcp.client.title":   "",
		"mcp.client.version": "0.1.0",
	})

	// **NOTE*** Do not add any direct access to the in-memory server session. Otherwise, the tests will result in
	// not being able to run in end-to-end tests. The test code must solely operate through the client side sessions.
	t.Cleanup(func() {
		_ = ret.session.Close()
		m.mux.Lock()
		defer m.mux.Unlock()
		delete(m.sessions, ret.session.ID())
		close(ret.progressNotifications)
		close(ret.promptListChangedNotifications)
		close(ret.resourceUpdatedNotifications)
		close(ret.resourceListChangedNotifications)
		close(ret.loggingNotification)
		close(ret.createMessageRequests)
	})
	m.mux.Lock()
	defer m.mux.Unlock()
	if m.sessions == nil {
		m.sessions = make(map[string]*mcpSession)
	}
	m.sessions[ret.session.ID()] = ret
	return ret
}
