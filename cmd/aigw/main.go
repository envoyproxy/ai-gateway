// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"fmt"
	"io"
	"log"
	"os"

	"github.com/alecthomas/kong"

	"github.com/envoyproxy/ai-gateway/internal/version"
)

type (
	cmd struct {
		Version   struct{}     `cmd:"" help:"Show version."`
		Translate cmdTranslate `cmd:"" help:"Translate yaml files containing AI Gateway resources to Envoy Gateway and Kubernetes API Gateway resources. The translated resources are written to stdout."`
	}
	cmdTranslate struct {
		Paths []string `arg:"" name:"path" help:"Paths to yaml files to translate." type:"path"`
	}
)

func main() {
	doMain(os.Stdout, os.Stderr, os.Args[1:], translate)
}

func doMain(stdout, stderr io.Writer, args []string, tf translateFn) {
	var c cmd
	parser, err := kong.New(&c,
		kong.Name("aigw"),
		kong.Description("Envoy AI Gateway CLI"),
		kong.Writers(stdout, stderr),
	)
	if err != nil {
		log.Fatalf("Error creating parser: %v", err)
	}
	ctx, err := parser.Parse(args)
	parser.FatalIfErrorf(err)
	switch ctx.Command() {
	case "version":
		_, _ = stdout.Write([]byte(fmt.Sprintf("Envoy AI Gateway CLI: %s\n", version.Version)))
	case "translate <path>":
		err = tf(c.Translate, stdout, stderr)
		if err != nil {
			log.Fatalf("Error translating: %v", err)
		}
	default:
		panic("unreachable")
	}
}
