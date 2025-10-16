// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2emcp

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/stretchr/testify/require"

	internaltesting "github.com/envoyproxy/ai-gateway/internal/testing"
)

func TestAIGWRun_LLM(t *testing.T) {
	adminPort := startAIGWCLI(t, aigwBin, []string{
		"OPENAI_BASE_URL=http://localhost:11434/v1",
		"OPENAI_API_KEY=unused",
	}, "run")

	ctx := t.Context()

	t.Run("chat completion", func(t *testing.T) {
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
		}, 10*time.Second, 2*time.Second,
			"chat completion never succeeded")
	})

	t.Run("access metrics", func(t *testing.T) {
		t.Skip()
		internaltesting.RequireEventuallyNoError(t, func() error {
			req, err := http.NewRequest(http.MethodGet,
				fmt.Sprintf("http://localhost:%d/metrics", adminPort), nil)
			require.NoError(t, err)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("status %d", resp.StatusCode)
			}
			return nil
		}, 1*time.Minute, time.Second,
			"metrics endpoint never became available")
	})
}
