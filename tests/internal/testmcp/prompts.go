// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testmcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var DummyPrompt = &mcp.Prompt{
	Name:        "dummy",
	Description: "a dummy prompt that does nothing",
	Arguments:   []*mcp.PromptArgument{},
}

var CodeReviewPrompt = &mcp.Prompt{
	Name:        "code_review",
	Description: "do a code review",
	Arguments:   []*mcp.PromptArgument{{Name: "Code", Required: true}},
}

func codReviewPromptHandler(_ context.Context, req *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	return &mcp.GetPromptResult{
		Description: "Code review prompt",
		Messages: []*mcp.PromptMessage{
			{Role: "user", Content: &mcp.TextContent{Text: "Please review the following code: " + req.Params.Arguments["Code"]}},
		},
	}, nil
}
