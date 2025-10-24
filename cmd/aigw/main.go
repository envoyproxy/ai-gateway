// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/alecthomas/kong"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/envoyproxy/ai-gateway/cmd/extproc/mainlib"
	"github.com/envoyproxy/ai-gateway/internal/autoconfig"
	"github.com/envoyproxy/ai-gateway/internal/version"
)

type (
	// cmd corresponds to the top-level `aigw` command.
	cmd struct {
		// Version is the sub-command to show the version.
		Version struct{} `cmd:"" help:"Show version."`
		// Run is the sub-command parsed by the `cmdRun` struct.
		Run cmdRun `cmd:"" help:"Run the AI Gateway locally for given configuration."`
		// Healthcheck is the sub-command to check if the aigw server is healthy.
		Healthcheck cmdHealthcheck `cmd:"" help:"Docker HEALTHCHECK command."`
	}
	// cmdRun corresponds to `aigw run` command.
	cmdRun struct {
		Debug     bool                   `help:"Enable debug logging emitted to stderr."`
		Path      string                 `arg:"" name:"path" optional:"" help:"Path to the AI Gateway configuration yaml file. Optional when at least OPENAI_API_KEY, AZURE_OPENAI_API_KEY, or ANTHROPIC_API_KEY is set." type:"path"`
		AdminPort int                    `help:"HTTP port for the admin server (serves /metrics and /health endpoints)." default:"1064"`
		McpConfig string                 `name:"mcp-config" help:"Path to MCP servers configuration file." type:"path"`
		McpJSON   string                 `name:"mcp-json" help:"JSON string of MCP servers configuration."`
		mcpConfig *autoconfig.MCPServers `kong:"-"` // Internal field: normalized MCP JSON data
	}
	// cmdHealthcheck corresponds to `aigw healthcheck` command.
	cmdHealthcheck struct{}
)

// Validate is called by Kong after parsing to validate the cmdRun arguments.
func (c *cmdRun) Validate() error {
	if c.McpConfig != "" && c.McpJSON != "" {
		return fmt.Errorf("mcp-config and mcp-json are mutually exclusive")
	}
	if c.Path == "" && os.Getenv("OPENAI_API_KEY") == "" && os.Getenv("AZURE_OPENAI_API_KEY") == "" && os.Getenv("ANTHROPIC_API_KEY") == "" && c.McpConfig == "" && c.McpJSON == "" {
		return fmt.Errorf("you must supply at least OPENAI_API_KEY, AZURE_OPENAI_API_KEY, ANTHROPIC_API_KEY, or a config file path")
	}

	var mcpJSON string
	if c.McpConfig != "" {
		raw, err := os.ReadFile(c.McpConfig)
		if err != nil {
			return fmt.Errorf("failed to read MCP config file: %w", err)
		}
		mcpJSON = string(raw)
	} else {
		mcpJSON = c.McpJSON
	}

	if mcpJSON != "" {
		var mcpConfig autoconfig.MCPServers
		if err := json.Unmarshal([]byte(mcpJSON), &mcpConfig); err != nil {
			return fmt.Errorf("failed to unmarshal MCP config: %w", err)
		}
		c.mcpConfig = &mcpConfig
	}
	return nil
}

type (
	runFn         func(context.Context, cmdRun, runOpts, io.Writer, io.Writer) error
	healthcheckFn func(context.Context, io.Writer, io.Writer) error
)

func main() {
	doMain(ctrl.SetupSignalHandler(), os.Stdout, os.Stderr, os.Args[1:], os.Exit, run, healthcheck)
}

// doMain is the main entry point for the CLI. It parses the command line arguments and executes the appropriate command.
//
//   - stdout is the writer to use for standard output. Mainly for testing.
//   - stderr is the writer to use for standard error. Mainly for testing.
//   - `args` are the command line arguments without the program name.
//   - exitFn is the function to call to exit the program during the parsing of the command line arguments. Mainly for testing.
//   - rf is the function to call to run the AI Gateway locally. Mainly for testing.
func doMain(ctx context.Context, stdout, stderr io.Writer, args []string, exitFn func(int),
	rf runFn,
	hf healthcheckFn,
) {
	var c cmd
	parser, err := kong.New(&c,
		kong.Name("aigw"),
		kong.Description("Envoy AI Gateway CLI"),
		kong.Writers(stdout, stderr),
		kong.Exit(exitFn),
	)
	if err != nil {
		log.Fatalf("Error creating parser: %v", err)
	}
	parsed, err := parser.Parse(args)
	parser.FatalIfErrorf(err)
	switch parsed.Command() {
	case "version":
		_, _ = fmt.Fprintf(stdout, "Envoy AI Gateway CLI: %s\n", version.Version)
	case "run", "run <path>":
		err = rf(ctx, c.Run, runOpts{extProcLauncher: mainlib.Main}, stdout, stderr)
		if err != nil {
			log.Fatalf("Error running: %v", err)
		}
	case "healthcheck":
		err = hf(ctx, stdout, stderr)
		if err != nil {
			log.Fatalf("Health check failed: %v", err)
		}
	default:
		panic("unreachable")
	}
}
