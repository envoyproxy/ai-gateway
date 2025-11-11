// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/envoyproxy/ai-gateway/internal/autoconfig"
)

// proxyStdioMCPServers runs the configured stdio MCP servers and starts a Streamable HTTP proxy
// for each, updating the MCPServers in place.
func proxyStdioMCPServers(ctx context.Context, logger *slog.Logger, mcpServers *autoconfig.MCPServers) error {
	if mcpServers == nil {
		return nil
	}
	for name, mcpServer := range mcpServers.McpServers {
		if mcpServer.Command != "" {
			address, err := runStdio2HTTPProxy(ctx, logger, name, mcpServer.Command, mcpServer.Args...)
			if err != nil {
				return err
			}
			mcpServers.McpServers[name] = autoconfig.MCPServer{
				Type:         "http",
				URL:          address,
				Headers:      mcpServer.Headers,
				IncludeTools: mcpServer.IncludeTools,
			}
		}
	}
	return nil
}

// runStdio2HTTPProxy runs a Streamable HTTP MCP proxy that connects to a stdio MCP server.
// It starts the command, connects to its stdio as an MCP transport, and
// exposes a Streamable HTTP server that proxies requests to the stdio MCP session.
func runStdio2HTTPProxy(ctx context.Context, logger *slog.Logger, name, command string, args ...string) (string, error) {
	// Initialize the command to run the stdio MCP server.
	cmd := exec.Command(command, args...)
	transport := &mcp.CommandTransport{Command: cmd}
	client := mcp.NewClient(&mcp.Implementation{Name: "stdio2http-" + name}, nil)
	// This will start the configured command in the background and connect to its
	// stdio as an MCP transport.
	logger.Info("starting stdio2http MCP proxy command", "name", name, "command", command, "args", args)
	cs, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return "", fmt.Errorf("running the %s stdio2http proxy command: %w", name, err)
	}

	// Create an HTTP server that proxies requests to the MCP session.
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "stdio2http-" + name}, nil)
	if err = errors.Join(
		proxyTools(ctx, cs, mcpServer),
		proxyResources(ctx, cs, mcpServer),
		proxyPrompts(ctx, cs, mcpServer),
	); err != nil {
		return "", fmt.Errorf("proxying features: %w", err)
	}

	// Find a free port to listen on.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("getting a free port for the %s stdio2http proxy: %w", name, err)
	}
	// TODO(nacx): For security reasons, Envoy Gateway does not allow defining Backend objects for
	// "localhost (see: https://gateway.envoyproxy.io/docs/tasks/traffic/backend/#warning).
	// This restriction, however, could be relaxed in standalone mode, as it is a single persona mode
	// where everything runs in the same process and the threat model is different.
	// Until that is implemented, we need to use a different DNS name that resolves to localhost to be
	// able to proxy locally running MCP servers.
	mcpAddress := fmt.Sprintf("http://localhost:%d/mcp", listener.Addr().(*net.TCPAddr).Port)

	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer }, nil)
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 120 * time.Second,
		WriteTimeout:      120 * time.Second,
	}

	go func() {
		logger.Info("starting stdio2http MCP proxy", "name", name, "address", listener.Addr().String())
		if serverErr := server.Serve(listener); serverErr != nil && !errors.Is(serverErr, http.ErrServerClosed) {
			logger.Error("stdio2http MCP proxy error", "name", name, "error", serverErr)
		}
	}()

	go func() {
		<-ctx.Done()
		logger.Info("shutting down stdio2http MCP proxy", "name", name)
		// Terminate the command process.
		if err = cs.Close(); err != nil {
			logger.Error("stdio2http MCP proxy command shutdown error", "name", name, "error", err)
		}
		// Shutdown the HTTP server.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = server.Shutdown(shutdownCtx); err != nil {
			logger.Error("stdio2http MCP proxy server shutdown error", "name", name, "error", err)
		}
	}()

	return mcpAddress, nil
}

// proxyTools proxies tool calls to the stdio MCP client session.
func proxyTools(ctx context.Context, cs *mcp.ClientSession, server *mcp.Server) error {
	if cs.InitializeResult().Capabilities.Tools == nil {
		return nil
	}

	for tool, err := range cs.Tools(ctx, nil) {
		if err != nil {
			return err
		}
		server.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return cs.CallTool(ctx, &mcp.CallToolParams{
				Meta:      req.Params.Meta,
				Name:      req.Params.Name,
				Arguments: req.Params.Arguments,
			})
		})
	}
	return nil
}

// proxyResources proxies resource requests to the stdio MCP client session.
func proxyResources(ctx context.Context, cs *mcp.ClientSession, server *mcp.Server) error {
	if cs.InitializeResult().Capabilities.Resources == nil {
		return nil
	}

	for resource, err := range cs.Resources(ctx, nil) {
		if err != nil {
			return err
		}
		server.AddResource(resource, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return cs.ReadResource(ctx, &mcp.ReadResourceParams{
				Meta: req.Params.Meta,
				URI:  req.Params.URI,
			})
		})
	}
	for template, err := range cs.ResourceTemplates(ctx, nil) {
		if err != nil {
			return err
		}
		server.AddResourceTemplate(template, func(ctx context.Context, req *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
			return cs.ReadResource(ctx, &mcp.ReadResourceParams{
				Meta: req.Params.Meta,
				URI:  req.Params.URI,
			})
		})
	}
	return nil
}

// proxyPrompts proxies prompt requests to the stdio MCP client session.
func proxyPrompts(ctx context.Context, cs *mcp.ClientSession, server *mcp.Server) error {
	if cs.InitializeResult().Capabilities.Prompts == nil {
		return nil
	}

	for prompt, err := range cs.Prompts(ctx, nil) {
		if err != nil {
			return err
		}
		server.AddPrompt(prompt, func(ctx context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return cs.GetPrompt(ctx, &mcp.GetPromptParams{
				Meta:      req.Params.Meta,
				Name:      req.Params.Name,
				Arguments: req.Params.Arguments,
			})
		})
	}
	return nil
}
