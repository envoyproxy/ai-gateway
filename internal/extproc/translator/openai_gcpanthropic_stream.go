// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bufio"
	"bytes"

	"github.com/anthropics/anthropic-sdk-go"
)

// streamingToolCall holds the state for a single tool call that is being streamed.
type streamingToolCall struct {
	ID         string
	Name       string
	InputJSON  bytes.Buffer
	SentHeader bool
}

// AnthropicStreamParser manages the stateful translation of an Anthropic SSE stream
// to an OpenAI-compatible SSE stream.
type AnthropicStreamParser struct {
	eventScanner    *bufio.Scanner
	buffer          bytes.Buffer
	activeMessageID string
	activeToolCalls map[int]*streamingToolCall
	tokenUsage      LLMTokenUsage
	stopReason      anthropic.StopReason
	model           string
}
