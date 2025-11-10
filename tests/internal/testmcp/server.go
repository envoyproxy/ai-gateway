// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testmcp

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var logger = log.New(os.Stdout, "[mcptestserver] ", 0)

type Options struct {
	Port                              int
	ForceJSONResponse, DumbEchoServer bool
	WriteTimeout                      time.Duration
}

// NewServer starts a demo MCP server with two tools: echo and sum.
//
// When forceJSONResponse true, the server will respond with JSON responses
// instead of using text/even-stream. The spec allows both so it is useful
// for us to test both scenarios.
//
// When dumbEchoServer is true, the server will only implement the echo tool,
// and will not implement any prompts or resources. This is useful for testing
// basic routing.
func NewServer(opts *Options) *http.Server {
	if opts.DumbEchoServer {
		return newDumbServer(opts.Port)
	}

	// --- MCP server implementation.
	handlerCounts := &notificationCounts{}
	s := mcp.NewServer(
		&mcp.Implementation{Name: "demo-http-server", Version: "0.1.0"},
		&mcp.ServerOptions{
			HasTools: true,
			RootsListChangedHandler: func(_ context.Context, request *mcp.RootsListChangedRequest) {
				logger.Printf("RootsListChanged request: %+v", request)
				handlerCounts.RootsListChanged.Add(1)
			},
			SubscribeHandler: func(_ context.Context, request *mcp.SubscribeRequest) error {
				logger.Printf("Subscribe request: %+v", request)
				handlerCounts.Subscribe.Add(1)
				return nil
			},
			UnsubscribeHandler: func(_ context.Context, request *mcp.UnsubscribeRequest) error {
				logger.Printf("Unsubscribe request: %+v", request)
				handlerCounts.Unsubscribe.Add(1)
				return nil
			},
			CompletionHandler: func(_ context.Context, request *mcp.CompleteRequest) (*mcp.CompleteResult, error) {
				logger.Printf("Complete request: %+v", request)
				return &mcp.CompleteResult{
					Completion: mcp.CompletionResultDetails{
						Values: []string{"python", "pytorch", "pyside"},
					},
				}, nil
			},
		},
	)

	if apiKey := os.Getenv("TEST_API_KEY"); apiKey != "" {
		header := strings.ToLower(cmp.Or(os.Getenv("TEST_API_KEY_HEADER"), "Authorization"))
		expectedValue := apiKey
		if header == "authorization" {
			expectedValue = "Bearer " + apiKey
		}
		s.AddReceivingMiddleware(func(handler mcp.MethodHandler) mcp.MethodHandler {
			return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
				if req.GetExtra().Header.Get(header) != expectedValue {
					return nil, fmt.Errorf("invalid API key")
				}
				return handler(ctx, method, req)
			}
		})
	}

	s.AddPrompt(CodeReviewPrompt, codReviewPromptHandler)
	s.AddResource(DummyResource, DummyResourceHandler())
	s.AddResourceTemplate(DummyResourceTemplate, DummyResourceHandler())
	mcp.AddTool(s, ToolEcho.Tool, ToolEcho.Handler)
	mcp.AddTool(s, ToolSum.Tool, ToolSum.Handler)
	mcp.AddTool(s, ToolError.Tool, ToolError.Handler)
	mcp.AddTool(s, ToolCountDown.Tool, ToolCountDown.Handler)
	mcp.AddTool(s, ToolContainsRootTool.Tool, ToolContainsRootTool.Handler)
	mcp.AddTool(s, ToolDelay.Tool, ToolDelay.Handler)
	mcp.AddTool(s, ToolElicitEmail.Tool, ToolElicitEmail.Handler)
	mcp.AddTool(s, ToolCreateMessage.Tool, ToolCreateMessage.Handler)
	promptAddTool := newToolAddPrompt(s)
	mcp.AddTool(s, promptAddTool.Tool, promptAddTool.Handler)
	resourceUpdateNotificationTool := newToolResourceUpdateNotification(s)
	mcp.AddTool(s, resourceUpdateNotificationTool.Tool, resourceUpdateNotificationTool.Handler)
	addOrDeleteResourceTool := newToolAddOrDeleteAnotherDummyResource(s)
	mcp.AddTool(s, addOrDeleteResourceTool.Tool, addOrDeleteResourceTool.Handler)
	notificationsCounts := newToolNotificationCounts(handlerCounts)
	mcp.AddTool(s, notificationsCounts.Tool, notificationsCounts.Handler)

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s
	}, &mcp.StreamableHTTPOptions{JSONResponse: opts.ForceJSONResponse})

	// --- Streamable HTTP transport (default endpoint path is "/mcp").
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", opts.Port),
		ReadHeaderTimeout: 3 * time.Second,
		// Allow long-lived connections.
		WriteTimeout: opts.WriteTimeout,
		Handler:      handler,
		ConnState: func(conn net.Conn, state http.ConnState) {
			log.Printf("MCP SERVER connection [%s] %s -> %s\n", state, conn.RemoteAddr(), conn.LocalAddr())
		},
	}
	go func() {
		log.Printf("starting MCP Streamable-HTTP server on :%d at /mcp", opts.Port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()
	return server
}

func newDumbServer(port int) *http.Server {
	s := mcp.NewServer(
		&mcp.Implementation{Name: "dumb-echo-server", Version: "0.1.0"},
		&mcp.ServerOptions{HasTools: true},
	)

	mcp.AddTool(s, ToolDumbEcho.Tool, ToolDumbEcho.Handler)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, &mcp.StreamableHTTPOptions{})
	server := &http.Server{Addr: fmt.Sprintf(":%d", port), ReadHeaderTimeout: 3 * time.Second, Handler: handler}
	go func() {
		log.Printf("starting DUMB MCP Streamable-HTTP server on :%d at /mcp", port)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server error: %v", err)
		}
	}()
	return server
}
