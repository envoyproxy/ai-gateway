package awsbedrock

import (
	"encoding/json"
	"strings"
)

// ConverseRequest is defined in the AWS Bedrock API:
// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html#API_runtime_Converse_RequestBody
type ConverseRequest struct {
	Messages []Message `json:"messages,omitempty"`
}

// Message is defined in the AWS Bedrock API:
// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Message.html#bedrock-Type-runtime_Message-content
type Message struct {
	Role    string         `json:"role,omitempty"`
	Content []ContentBlock `json:"content,omitempty"`
}

// ContentBlock is defined in the AWS Bedrock API:
// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ContentBlock.html
type ContentBlock struct {
	Text string `json:"text,omitempty"`
}

// ConverseResponse is defined in the AWS Bedrock API:
// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_Converse.html#API_runtime_Converse_ResponseElements
type ConverseResponse struct {
	Output ConverseResponseOutput `json:"output,omitempty"`
	Usage  TokenUsage             `json:"usage,omitempty"`
}

// ConverseResponseOutput is defined in the AWS Bedrock API:
// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ConverseOutput.html
type ConverseResponseOutput struct {
	Message Message `json:"message,omitempty"`
}

// TokenUsage is defined in the AWS Bedrock API:
// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_TokenUsage.html
type TokenUsage struct {
	InputTokens  int `json:"inputTokens,omitempty"`
	OutputTokens int `json:"outputTokens,omitempty"`
	TotalTokens  int `json:"totalTokens,omitempty"`
}

// ConverseStreamEvent is the union of all possible event types in the AWS Bedrock API:
// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ConverseStream.html
type ConverseStreamEvent struct {
	ContentBlockIndex int                                   `json:"contentBlockIndex,omitempty"`
	Delta             *ConverseStreamEventContentBlockDelta `json:"delta,omitempty"`
	Role              *string                               `json:"role,omitempty"`
	StopReason        *string                               `json:"stopReason,omitempty"`
	Usage             *TokenUsage                           `json:"usage,omitempty"`
}

// String implements fmt.Stringer.
func (c ConverseStreamEvent) String() string {
	buf, _ := json.Marshal(c)
	return strings.ReplaceAll(string(buf), ",", ", ")
}

// ConverseStreamEventContentBlockDelta is defined in the AWS Bedrock API:
// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ContentBlockDelta.html
type ConverseStreamEventContentBlockDelta struct {
	Text string `json:"text,omitempty"`
}
