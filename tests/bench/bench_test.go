// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// go test -timeout=15m -run='^$$' -bench=. -benchmem -benchtime=1x ./tests/bench/...

package bench

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/mcpproxy"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	tracing "github.com/envoyproxy/ai-gateway/internal/tracing/api"
	"github.com/envoyproxy/ai-gateway/tests/internal/testenvironment"
	"github.com/envoyproxy/ai-gateway/tests/internal/testmcp"
)

const (
	writeTimeout  = 120 * time.Second
	mcpServerPort = 8080
	aigwPort      = 1975
)

type MCPBenchCase struct {
	Name string
	Port int
}

type NoopCrypto struct{}

func (n NoopCrypto) Encrypt(plaintext string) (string, error) { return plaintext, nil }
func (n NoopCrypto) Decrypt(encrypted string) (string, error) { return encrypted, nil }

// NoopMetrics implements metrics.MCPMetrics with no-ops.
type NoopMetrics struct{}

func (s NoopMetrics) WithRequestAttributes(_ *http.Request) metrics.MCPMetrics         { return s }
func (NoopMetrics) RecordRequestDuration(_ context.Context, _ time.Time, _ mcp.Params) {}
func (NoopMetrics) RecordRequestErrorDuration(_ context.Context, _ time.Time, _ metrics.MCPErrorType, _ mcp.Params) {
}
func (NoopMetrics) RecordMethodCount(_ context.Context, _ string, _ mcp.Params)               {}
func (NoopMetrics) RecordMethodErrorCount(_ context.Context, _ mcp.Params)                    {}
func (NoopMetrics) RecordInitializationDuration(_ context.Context, _ time.Time, _ mcp.Params) {}
func (NoopMetrics) RecordClientCapabilities(_ context.Context, _ *mcp.ClientCapabilities, _ mcp.Params) {
}

func (NoopMetrics) RecordServerCapabilities(_ context.Context, _ *mcp.ServerCapabilities, _ mcp.Params) {
}
func (NoopMetrics) RecordProgress(_ context.Context, _ mcp.Params) {}

// setupBenchmark sets up the client connection.
func setupBenchmark(b *testing.B) []MCPBenchCase {
	b.Helper() // Treat this as a helper function

	// setup MCP server
	mcpServer := testmcp.NewServer(&testmcp.Options{
		Port:              mcpServerPort,
		ForceJSONResponse: false,
		DumbEchoServer:    true,
		WriteTimeout:      writeTimeout,
		DisableLog:        true,
	})
	b.Cleanup(func() {
		_ = mcpServer.Close()
	})

	go startAIGW(b)

	errChs := []<-chan error{
		startMCPProxy(b, "0.0.0.0:3001", mcpServerPort, NoopCrypto{}),
		startMCPProxy(b, "0.0.0.0:3002", mcpServerPort, mcpproxy.NewPBKDF2AesGcmSessionCrypto("test", 100)),
		startMCPProxy(b, "0.0.0.0:3003", mcpServerPort, mcpproxy.NewPBKDF2AesGcmSessionCrypto("test", 100_100)),
	}

	for _, ch := range errChs {
		select {
		case err := <-ch:
			if err != nil {
				b.Fatalf("mcp proxy failed to start: %v", err)
			}
		case <-time.After(500 * time.Millisecond):
		}
	}
	// Reset the timer to exclude setup time from the results
	b.ResetTimer()
	return []MCPBenchCase{
		{
			Name: "ServerDirectly",
			Port: mcpServerPort,
		},
		{
			Name: "NOPCrypto",
			Port: 3001,
		},
		{
			Name: "Iterations_100",
			Port: 3002,
		},
		{
			Name: "Iterations_100_100",
			Port: 3003,
		},
		{
			Name: "AIGW",
			Port: aigwPort,
		},
	}
}

func checkAllConnections(t testing.TB, benchCases []MCPBenchCase) error {
	errGroup := &errgroup.Group{}
	for _, tc := range benchCases {
		errGroup.Go(func() error {
			return checkConnection(t, tc.Port, tc.Name)
		})
	}
	return errGroup.Wait()
}

func checkConnection(t testing.TB, port int, name string) error {
	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Logf("Failed to connect to %s on port %d: %v", name, port, err)
		return fmt.Errorf("failed to connect to %s on port %d: %w", name, port, err)
	}
	err = conn.Close()
	if err != nil {
		t.Logf("Failed to close connection to %s on port %d: %v", name, port, err)
		return fmt.Errorf("failed to close connection to %s on port %d: %w", name, port, err)
	}
	t.Logf("Successfully connected to %s on port %d", name, port)
	return nil
}

func startAIGW(b testing.TB) {
	// go run ./cmd/aigw run ./tests/bench/aigw.yaml
	cmd := exec.CommandContext(b.Context(), "go", "run", "../../cmd/aigw", "run", "./aigw.yaml")
	b.Logf("Running aigw with command: %v\n", cmd.Args)
	testenvironment.StartAndAwaitReady(b, cmd, b.Output(), b.Output(), "Envoy AI Gateway")
}

func startMCPProxy(b testing.TB, address string, mcpServerPort int, crypto mcpproxy.SessionCrypto) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		mcpLis, err := net.Listen("tcp", address)
		if err != nil {
			errCh <- fmt.Errorf("failed to listen on %s: %w", address, err)
			return
		}

		l := slog.New(slog.NewTextHandler(b.Output(), &slog.HandlerOptions{Level: slog.LevelDebug}))
		p, mux, _ := mcpproxy.NewMCPProxy(l, NoopMetrics{}, tracing.NoopMCPTracer{}, crypto)
		_ = p.LoadConfig(b.Context(), &filterapi.Config{
			MCPConfig: &filterapi.MCPConfig{
				BackendListenerAddr: fmt.Sprintf("http://127.0.0.1:%d", mcpServerPort),
				Routes: []filterapi.MCPRoute{
					{
						Name: "test-route",
						Backends: []filterapi.MCPBackend{
							{Name: "dumb-mcp-backend", Path: "/mcp"},
						},
					},
				},
			},
		})
		mcpServer := &http.Server{
			Handler:           mux,
			ReadHeaderTimeout: 120 * time.Second,
			WriteTimeout:      writeTimeout,
		}

		l.Info("Starting mcp proxy", "addr", mcpLis.Addr())
		if err2 := mcpServer.Serve(mcpLis); err2 != nil && !errors.Is(err2, http.ErrServerClosed) {
			l.Error("mcp proxy failed", "error", err2)
			select {
			case errCh <- err2:
			default:
			}
		}
		// If server exits cleanly, close channel without error (optional)
		close(errCh)
	}()

	return errCh
}

func BenchmarkMCP(b *testing.B) {
	cases := setupBenchmark(b)
	require.Eventually(b, func() bool {
		return checkAllConnections(b, cases) == nil
	}, time.Minute, time.Second)

	for _, tc := range cases {
		b.Run(tc.Name, func(b *testing.B) {
			mcpClient := mcp.NewClient(&mcp.Implementation{Name: "bench-http-client", Version: "0.1.0"}, nil)
			cs, err := mcpClient.Connect(b.Context(), &mcp.StreamableClientTransport{
				Endpoint: fmt.Sprintf("http://localhost:%d/mcp", tc.Port),
			}, nil)
			if err != nil {
				b.Fatalf("Failed to connect server: %v", err)
			}

			tools, err := cs.ListTools(b.Context(), &mcp.ListToolsParams{})
			if err != nil {
				b.Fatalf("Failed to list tools: %v", err)
			}
			var toolName string
			for _, t := range tools.Tools {
				if strings.Contains(t.Name, "echo") {
					toolName = t.Name
					break
				}
			}
			if toolName == "" {
				b.Fatalf("no echo tool found")
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ctx, cancel := context.WithTimeout(b.Context(), 5*time.Second)
				res, err := cs.CallTool(ctx, &mcp.CallToolParams{
					Name:      toolName,
					Arguments: testmcp.ToolEchoArgs{Text: "hello MCP"},
				})
				cancel()
				if err != nil {
					b.Fatalf("MCP Tool call name %s failed at iteration %d: %v", toolName, i, err)
				}

				txt, ok := res.Content[0].(*mcp.TextContent)
				if !ok {
					b.Fatalf("unexpected content type")
				}
				if txt.Text != "dumb echo: hello MCP" {
					b.Fatalf("unexpected text: %q", txt.Text)
				}
			}
		})
	}
}
