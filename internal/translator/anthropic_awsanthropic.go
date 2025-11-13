// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"cmp"
	"fmt"
	"net/url"
	"strconv"

	"github.com/tidwall/sjson"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// NewAnthropicToAWSAnthropicTranslator creates a translator for Anthropic to AWS Bedrock Anthropic format.
// AWS Bedrock supports the native Anthropic Messages API, so this is essentially a passthrough
// translator with AWS-specific path modifications.
func NewAnthropicToAWSAnthropicTranslator(apiVersion string, modelNameOverride internalapi.ModelNameOverride) AnthropicMessagesTranslator {
	anthropicTranslator := NewAnthropicToAnthropicTranslator(apiVersion, modelNameOverride).(*anthropicToAnthropicTranslator)
	return &anthropicToAWSAnthropicTranslator{
		apiVersion:                     apiVersion,
		anthropicToAnthropicTranslator: *anthropicTranslator,
	}
}

type anthropicToAWSAnthropicTranslator struct {
	anthropicToAnthropicTranslator
	apiVersion string
}

// ResponseHeaders implements [AnthropicMessagesTranslator.ResponseHeaders].
func (a *anthropicToAWSAnthropicTranslator) ResponseHeaders(headers map[string]string) (
	newHeaders []internalapi.Header, err error,
) {
	if a.stream {
		contentType := headers[contentTypeHeaderName]
		if contentType == "application/vnd.amazon.eventstream" {
			// We need to change the content-type to text/event-stream for streaming responses.
			newHeaders = []internalapi.Header{{contentTypeHeaderName, "text/event-stream"}}
		}
	}
	return
}

// RequestBody implements [AnthropicMessagesTranslator.RequestBody] for Anthropic to AWS Bedrock Anthropic translation.
// This handles the transformation from native Anthropic format to AWS Bedrock format.
// https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-messages-request-response.html
func (a *anthropicToAWSAnthropicTranslator) RequestBody(rawBody []byte, body *anthropicschema.MessagesRequest, _ bool) (
	newHeaders []internalapi.Header, newBody []byte, err error,
) {
	a.stream = body.GetStream()
	a.requestModel = cmp.Or(a.modelNameOverride, body.GetModel())

	newBody, err = sjson.SetBytesOptions(rawBody, anthropicVersionKey, a.apiVersion, sjsonOptions)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to set anthropic_version field: %w", err)
	}
	// Remove the model field from the body as AWS Bedrock expects the model to be specified in the path.
	// Otherwise, AWS complains "extra inputs are not permitted".
	newBody, _ = sjson.DeleteBytes(newBody, "model")

	// Determine the AWS Bedrock path based on whether streaming is requested.
	var pathTemplate string
	if body.GetStream() {
		pathTemplate = "/model/%s/invoke-stream"
	} else {
		pathTemplate = "/model/%s/invoke"
	}

	// URL encode the model ID for the path to handle ARNs with special characters.
	// AWS Bedrock model IDs can be simple names (e.g., "anthropic.claude-3-5-sonnet-20241022-v2:0")
	// or full ARNs which may contain special characters.
	encodedModelID := url.PathEscape(a.requestModel)
	path := fmt.Sprintf(pathTemplate, encodedModelID)

	newHeaders = []internalapi.Header{{pathHeaderName, path}, {contentLengthHeaderName, strconv.Itoa(len(newBody))}}
	return
}
