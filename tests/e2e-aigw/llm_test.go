// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2emcp

import (
	"fmt"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func TestAIGWRun_LLM(t *testing.T) {
	startAIGWCLI(t, aigwBin, []string{
		"OPENAI_BASE_URL=http://localhost:11434/v1",
		"OPENAI_API_KEY=unused",
	}, "run")

	ctx := t.Context()

	internaltesting.RequireEventuallyNoError(t, func() error {
		t.Logf("model to use: %q", ollamaModel)
		client := openai.NewClient(option.WithBaseURL("http://localhost:1975/v1/"))
		chatReq := openai.ChatCompletionNewParams{
			Messages: []openai.ChatCompletionMessageParamUnion{
				openai.UserMessage("Say this is a test"),
			},
			Model: ollamaModel,
		}
		chatCompletion, err := client.Chat.Completions.New(ctx, chatReq)
		if err != nil {
			return fmt.Errorf("chat completion failed: %w", err)
		}
		for _, choice := range chatCompletion.Choices {
			if choice.Message.Content != "" {
				return nil
			}
		}
		return fmt.Errorf("no content in response")
	}, 10*time.Second, 2*time.Second, "chat completion never succeeded")
}
