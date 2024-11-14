package awsbedrock

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

type ConverseResponse struct {
	Output ConverseResponseOutput `json:"output,omitempty"`
	Usage  ConverseResponseUsage  `json:"usage,omitempty"`
}

// ConverseResponseOutput is defined in the AWS Bedrock API:
// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_ConverseOutput.html
type ConverseResponseOutput struct {
	Message Message `json:"message,omitempty"`
}

// ConverseResponseUsage is defined in the AWS Bedrock API:
// https://docs.aws.amazon.com/bedrock/latest/APIReference/API_runtime_TokenUsage.html
type ConverseResponseUsage struct {
	InputTokens  int `json:"input_tokens,omitempty"`
	OutputTokens int `json:"output_tokens,omitempty"`
	TotalTokens  int `json:"total_tokens,omitempty"`
}
