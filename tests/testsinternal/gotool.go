// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testsinternal

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"golang.org/x/tools/go/packages"
)

// moduleRoot holds the root directory of the current Go module.
var moduleRoot string

func init() {
	cfg := &packages.Config{Mode: packages.NeedModule}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		panic(err)
	}
	if len(pkgs) == 0 || pkgs[0].Module == nil {
		panic("could not determine module root directory")
	}
	moduleRoot = pkgs[0].Module.Dir
}

// RunGoTool runs a Go tool command with the specified arguments and returns its output or an error.
func RunGoTool(name string, args ...string) (string, error) {
	return RunGoToolContext(context.Background(), name, args...)
}

// RunGoToolContext runs a Go tool command with the specified arguments and returns its output or an error.
// It uses a context to allow for cancellation and timeouts.
func RunGoToolContext(ctx context.Context, name string, args ...string) (string, error) {
	var stderr bytes.Buffer
	cmd := GoToolCmdContext(ctx, name, args...)
	cmd.Stderr = &stderr

	out, err := cmd.Output()
	if err != nil {
		return string(out), fmt.Errorf("%w: %s", err, stderr.String())
	}
	return string(out), nil
}

// GoToolCmd creates an *exec.Cmd to run a Go tool command with the specified arguments.
func GoToolCmd(name string, args ...string) *exec.Cmd {
	return GoToolCmdContext(context.Background(), name, args...)
}

// GoToolCmdContext creates an *exec.Cmd to run a Go tool command with the specified arguments.
func GoToolCmdContext(ctx context.Context, name string, args ...string) *exec.Cmd {
	execArgs := append([]string{"tool", fmt.Sprintf("-modfile=%s/tools/go.mod", moduleRoot), name}, args...)
	return exec.CommandContext(ctx, "go", execArgs...)
}
