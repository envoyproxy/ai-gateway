// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// 1. Build AIGW
//  	make clean build.aigw
// 2. Run the bench test
//   	go test -timeout=15m -run='^$$' -bench=. -benchmem -benchtime=1x ./tests/bench/...

package bench

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/envoyproxy/ai-gateway/tests/internal/testenvironment"
	"github.com/envoyproxy/ai-gateway/tests/internal/testmcp"
)

const (
	writeTimeout  = 120 * time.Second
	mcpServerPort = 8080
	aigwPort      = 1975
)

type MCPBenchCase struct {
	Name         string
	CheckPorts   []int
	Port         int
	Binary       string
	Args         []string
	ReadyMessage string
}

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

	// Reset the timer to exclude setup time from the results
	b.ResetTimer()
	return []MCPBenchCase{
		{
			Name: "BaseLine",
			Port: aigwPort,
			Args: []string{"run", "./aigw.yaml"},
		},
		{
			Name: "Iterations_100",
			Port: aigwPort,
			Args: []string{"run", "./aigw.yaml", "--mcp-session-encryption-iterations=100"},
		},
	}
}

func BenchmarkMCP(b *testing.B) {
	cases := setupBenchmark(b)
	for _, tc := range cases {
		if tc.Binary == "" {
			tc.Binary = fmt.Sprintf("../../out/aigw-%s-%s", runtime.GOOS, runtime.GOARCH)
		}
		if len(tc.Args) == 0 {
			tc.Args = []string{"run", "../aigw.yaml"}
		}
		if len(tc.CheckPorts) == 0 {
			tc.CheckPorts = []int{9901, 1061}
		}
		if tc.ReadyMessage == "" {
			tc.ReadyMessage = "Envoy AI Gateway"
		}

		b.Run(tc.Name, func(b *testing.B) {
			c := startProxy(b, &tc)
			defer func() {
				_ = c.Cancel()
			}()
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

func killProcessListeningOn(port int) error {
	cmd := exec.Command("lsof", "-nP", "-sTCP:LISTEN", "-i", fmt.Sprintf("TCP:%d", port), "-t") // nolint: gosec
	out, err := cmd.Output()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) && len(ee.Stderr) == 0 {
			return fmt.Errorf("no process listening on %d", port)
		}
		return fmt.Errorf("lsof failed: %w", err)
	}
	pids := strings.Fields(string(out))
	if len(pids) == 0 {
		return fmt.Errorf("no process listening on %d", port)
	}
	for _, ps := range pids {
		pid, err := strconv.Atoi(ps)
		if err != nil {
			continue
		}
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = syscall.Kill(pid, syscall.SIGKILL)
	}
	return nil
}

func startProxy(b testing.TB, tc *MCPBenchCase) *exec.Cmd {
	for _, p := range tc.CheckPorts {
		_ = killProcessListeningOn(p)
	}

	cmd := exec.CommandContext(b.Context(), tc.Binary, tc.Args...) // nolint: gosec
	devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		b.Fatalf("open %s: %v", os.DevNull, err)
	}
	b.Cleanup(func() {
		_ = devnull.Close()
	})
	testenvironment.StartAndAwaitReady(b, cmd, devnull, devnull, tc.ReadyMessage)
	return cmd
}
