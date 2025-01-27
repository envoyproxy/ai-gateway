//go:build test_doctest

package doctest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// codeBlock represents a single code block in a markdown file.
type codeBlock struct {
	// lines is each line of the code block.
	lines []string
}

// String implements the fmt.Stringer interface for debugging.
func (c codeBlock) String() string {
	var str string
	for i, line := range c.lines {
		str += fmt.Sprintf("%d: %s", i, line)
	}
	return str
}

// requireRunAllLines runs all lines in a code block, skipping empty/commented lines.
func (c codeBlock) requireRunAllLines(t *testing.T) {
	for _, line := range c.lines {
		requireRunBashCommand(t, line)
	}
}

// requireExtractCodeBlocks extracts all code blocks from a markdown file.
//
// This skips all lines starting with "#", which are comments.
func requireExtractCodeBlocks(t *testing.T, path string) []codeBlock {
	source, err := os.ReadFile(path)
	require.NoError(t, err)

	md := goldmark.New(goldmark.WithParserOptions(parser.WithAutoHeadingID()))
	doc := md.Parser().Parse(text.NewReader(source))

	var codeBlocks []codeBlock
	err = ast.Walk(doc, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if rawBlock, ok := n.(*ast.FencedCodeBlock); ok {
				var blk codeBlock
				for i := 0; i < rawBlock.Lines().Len(); i++ {
					line := rawBlock.Lines().At(i)
					if len(line.Value(source)) == 0 || line.Value(source)[0] == '#' {
						continue
					}
					blk.lines = append(blk.lines, string(line.Value(source)))
				}
				codeBlocks = append(codeBlocks, blk)
			}
		}
		return ast.WalkContinue, nil
	})
	require.NoError(t, err)
	return codeBlocks
}

// requireExecutableInPath checks if the executables are in the PATH.
func requireExecutableInPath(t *testing.T, executables ...string) {
	// Always require "bash" to run the code blocks.
	_, err := exec.LookPath("bash")
	require.NoError(t, err, "bash not found in PATH")
	for _, executable := range executables {
		_, err := exec.LookPath(executable)
		require.NoError(t, err, "executable %s not found in PATH", executable)
	}
}

// requireRunBashCommand runs a bash command. This is used to run the code blocks in the markdown file.
func requireRunBashCommand(t *testing.T, command string) {
	fmt.Printf("\u001b[32m=== Running command: %s\u001B[0m\n", command)
	cmd := exec.Command("bash", "-c", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())
}

// requireNewKindCluster creates a new kind cluster if it does not already exist.
func requireNewKindCluster(t *testing.T, clusterName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Minute)
	defer cancel()
	const kindPath = "../../.bin/kind" // This is automatically installed as a dependency in the Makefile.

	cmd := exec.CommandContext(ctx, kindPath, "create", "cluster", "--name", clusterName)
	out, err := cmd.CombinedOutput()
	if err != nil && !bytes.Contains(out, []byte("already exist")) {
		require.NoError(t, err, "error creating kind cluster")
	}

	cmd = exec.CommandContext(ctx, kindPath, "export", "kubeconfig", "--name", clusterName)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Run())
}
