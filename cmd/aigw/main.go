// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"

	"github.com/alecthomas/kong"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/envoyproxy/ai-gateway/internal/version"
)

type (
	// cmd corresponds to the top-level `aigw` command.
	cmd struct {
		// Version is the sub-command to show the version.
		Version struct{} `cmd:"" help:"Show version."`
		// Translate is the sub-command parsed by the `cmdTranslate` struct.
		Translate cmdTranslate `cmd:"" help:"Translate yaml files containing AI Gateway resources to Envoy Gateway and Kubernetes resources. The translated resources are written to stdout."`
		// Run is the sub-command parsed by the `cmdRun` struct.
		Run cmdRun `cmd:"" help:"Run the AI Gateway locally for given configuration."`
	}
	// cmdTranslate corresponds to `aigw translate` command.
	cmdTranslate struct {
		Debug bool     `help:"Enable debug logging emitted to stderr."`
		Paths []string `arg:"" name:"path" help:"Paths to yaml files to translate." type:"path"`
	}
	// cmdRun corresponds to `aigw run` command.
	cmdRun struct {
		Debug       bool   `help:"Enable debug logging emitted to stderr."`
		Path        string `arg:"" name:"path" optional:"" help:"Path to the AI Gateway configuration yaml file. Optional. When this is not given, aigw runs the default configuration. Use --show-default to check the default configuration's behavior" type:"path"`
		ShowDefault bool   `help:"Show the default configuration, and exit."`
	}
)

type (
	subCmdFn[T any] func(context.Context, T, io.Writer, io.Writer) error
	translateFn     subCmdFn[cmdTranslate]
	runFn           subCmdFn[cmdRun]
)

func main() {
	doMain(ctrl.SetupSignalHandler(), os.Stdout, os.Stderr, os.Args[1:], os.Exit, translate, run)
}

// doMain is the main entry point for the CLI. It parses the command line arguments and executes the appropriate command.
//
//   - stdout is the writer to use for standard output. Mainly for testing.
//   - stderr is the writer to use for standard error. Mainly for testing.
//   - `args` are the command line arguments without the program name.
//   - exitFn is the function to call to exit the program during the parsing of the command line arguments. Mainly for testing.
//   - tf is the function to call to translate the AI Gateway resources to Envoy Gateway and Kubernetes resources. Mainly for testing.
//   - rf is the function to call to run the AI Gateway locally. Mainly for testing.
func doMain(ctx context.Context, stdout, stderr io.Writer, args []string, exitFn func(int),
	tf translateFn,
	rf runFn,
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
		_, _ = stdout.Write([]byte(fmt.Sprintf("Envoy AI Gateway CLI: %s\n", version.Version)))
	case "translate <path>":
		err = tf(ctx, c.Translate, stdout, stderr)
		if err != nil {
			log.Fatalf("Error translating: %v", err)
		}
	case "run", "run <path>":
		err = rf(ctx, c.Run, stdout, stderr)
		if err != nil {
			log.Fatalf("Error running: %v", err)
		}
	default:
		panic("unreachable")
	}
}
