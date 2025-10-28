// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
	"net/url"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
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

// RequestBody implements [AnthropicMessagesTranslator.RequestBody] for Anthropic to AWS Bedrock Anthropic translation.
// This handles the transformation from native Anthropic format to AWS Bedrock format.
// https://docs.aws.amazon.com/bedrock/latest/userguide/model-parameters-anthropic-claude-messages-request-response.html
func (a *anthropicToAWSAnthropicTranslator) RequestBody(rawBody []byte, body *anthropicschema.MessagesRequest, _ bool) (
	headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, err error,
) {
	// AWS Bedrock always needs a body mutation because we must add anthropic_version and remove model field
	headerMutation, bodyMutation, err = a.anthropicToAnthropicTranslator.RequestBody(rawBody, body, true)
	if err != nil {
		return
	}

	// add anthropic_version field
	preparedBody, err := sjson.SetBytes(bodyMutation.GetBody(), anthropicVersionKey, a.apiVersion)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to set anthropic_version field: %w", err)
	}
	// delete model field as AWS Bedrock expects model in the path, not in the body
	preparedBody, err = sjson.DeleteBytes(preparedBody, "model")
	if err != nil {
		return nil, nil, fmt.Errorf("failed to delete model field: %w", err)
	}

	bodyMutation = &extprocv3.BodyMutation{
		Mutation: &extprocv3.BodyMutation_Body{Body: preparedBody},
	}

	// update content length after changing the body
	setContentLength(headerMutation, preparedBody)

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
	pathSuffix := fmt.Sprintf(pathTemplate, encodedModelID)

	// Overwriting path of the Anthropic to Anthropic translator
	headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
		Header: &corev3.HeaderValue{
			Key:      ":path",
			RawValue: []byte(pathSuffix),
		},
	})
	return
}
