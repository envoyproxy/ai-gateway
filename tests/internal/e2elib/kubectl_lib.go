// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2elib

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"k8s.io/kubectl/pkg/cmd"
)

// kubectlCmd wraps kubectl library calls to provide an exec.Cmd-like interface
type kubectlCmd struct {
	ctx     context.Context
	args    []string
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Process *kubectlProcess
}

// kubectlProcess mimics os/exec.Cmd Process field for compatibility
type kubectlProcess struct {
	cancel context.CancelFunc
}

// Kill terminates the kubectl process
func (p *kubectlProcess) Kill() error {
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

// Run executes the kubectl command
func (k *kubectlCmd) Run() error {
	stdin := k.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := k.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := k.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	kubectlCmd := cmd.NewDefaultKubectlCommand()
	kubectlCmd.SetArgs(k.args)
	kubectlCmd.SetContext(k.ctx)
	kubectlCmd.SetIn(stdin)
	kubectlCmd.SetOut(stdout)
	kubectlCmd.SetErr(stderr)

	return kubectlCmd.Execute()
}

// Start begins the kubectl command asynchronously
func (k *kubectlCmd) Start() error {
	// Create a cancellable context for the process
	ctx, cancel := context.WithCancel(k.ctx)
	k.Process = &kubectlProcess{cancel: cancel}

	stdin := k.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := k.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := k.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	kubectlCmd := cmd.NewDefaultKubectlCommand()
	kubectlCmd.SetArgs(k.args)
	kubectlCmd.SetContext(ctx)
	kubectlCmd.SetIn(stdin)
	kubectlCmd.SetOut(stdout)
	kubectlCmd.SetErr(stderr)

	// Start the command in a goroutine
	go func() {
		_ = kubectlCmd.Execute()
	}()

	return nil
}

// Output executes the kubectl command and returns stdout
func (k *kubectlCmd) Output() ([]byte, error) {
	var outBuf bytes.Buffer
	k.Stdout = &outBuf
	err := k.Run()
	return outBuf.Bytes(), err
}

// Kubectl runs the kubectl command with the given context and arguments.
// Returns a command that can be executed like exec.Cmd but uses kubectl library internally.
func Kubectl(ctx context.Context, args ...string) *kubectlCmd {
	return &kubectlCmd{
		ctx:    ctx,
		args:   args,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
	}
}

func KubectlApplyManifest(ctx context.Context, manifest string) error {
	cmd := Kubectl(ctx, "apply", "--server-side", "-f", manifest, "--force-conflicts")
	return cmd.Run()
}

func KubectlApplyManifestStdin(ctx context.Context, manifest string) error {
	cmd := Kubectl(ctx, "apply", "--server-side", "-f", "-")
	cmd.Stdin = bytes.NewBufferString(manifest)
	return cmd.Run()
}

func KubectlDeleteManifest(ctx context.Context, manifest string) error {
	cmd := Kubectl(ctx, "delete", "-f", manifest)
	return cmd.Run()
}

func kubectlRestartDeployment(ctx context.Context, namespace, deployment string) error {
	cmd := Kubectl(ctx, "rollout", "restart", "deployment/"+deployment, "-n", namespace)
	return cmd.Run()
}

func kubectlWaitForDeploymentReady(ctx context.Context, namespace, deployment string) error {
	cmd := Kubectl(ctx, "wait", "--timeout=2m", "-n", namespace,
		"deployment/"+deployment, "--for=create")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error waiting for deployment %s in namespace %s: %w", deployment, namespace, err)
	}

	cmd = Kubectl(ctx, "wait", "--timeout=2m", "-n", namespace,
		"deployment/"+deployment, "--for=condition=Available")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error waiting for deployment %s in namespace %s: %w", deployment, namespace, err)
	}
	return nil
}

func kubectlWaitForDaemonSetReady(ctx context.Context, namespace, daemonset string) error {
	// Wait for daemonset to be created.
	cmd := Kubectl(ctx, "wait", "--timeout=2m", "-n", namespace,
		"daemonset/"+daemonset, "--for=create")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error waiting for daemonset %s in namespace %s: %w", daemonset, namespace, err)
	}

	// Wait for daemonset pods to be ready using jsonpath.
	cmd = Kubectl(ctx, "wait", "--timeout=2m", "-n", namespace,
		"daemonset/"+daemonset, "--for=jsonpath={.status.numberReady}=1")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("error waiting for daemonset %s pods to be ready in namespace %s: %w", daemonset, namespace, err)
	}
	return nil
}



