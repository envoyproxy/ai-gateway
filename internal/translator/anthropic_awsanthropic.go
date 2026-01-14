// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"cmp"
	"fmt"
	"io"
	"net/url"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws/protocol/eventstream"
	"github.com/tidwall/sjson"

	anthropicschema "github.com/envoyproxy/ai-gateway/internal/apischema/anthropic"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
	"github.com/envoyproxy/ai-gateway/internal/json"
	"github.com/envoyproxy/ai-gateway/internal/metrics"
	"github.com/envoyproxy/ai-gateway/internal/tracing/tracingapi"
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
	a.stream = body.Stream
	a.requestModel = cmp.Or(a.modelNameOverride, body.Model)

	newBody, err = sjson.SetBytesOptions(rawBody, anthropicVersionKey, a.apiVersion, sjsonOptions)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to set anthropic_version field: %w", err)
	}
	// Remove the model field from the body as AWS Bedrock expects the model to be specified in the path.
	// Otherwise, AWS complains "extra inputs are not permitted".
	newBody, _ = sjson.DeleteBytes(newBody, "model")
	newBody, _ = sjson.DeleteBytes(newBody, "stream")

	// Determine the AWS Bedrock path based on whether streaming is requested.
	var pathTemplate string
	if body.Stream {
		pathTemplate = "/model/%s/invoke-with-response-stream"
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

// ResponseBody implements [AnthropicMessagesTranslator.ResponseBody].
func (a *anthropicToAWSAnthropicTranslator) ResponseBody(_ map[string]string, body io.Reader, endOfStream bool, span tracingapi.MessageSpan) (
	newHeaders []internalapi.Header, newBody []byte, tokenUsage metrics.TokenUsage, responseModel string, err error,
) {
	if !a.stream {
		return a.anthropicToAnthropicTranslator.ResponseBody(nil, body, endOfStream, span)
	}
	// For streaming responses, AWS somehow wraps each Anthropicschema.MessagesStreamChunk
	// in an Amazon EventStream message. We need to unwrap these messages and convert them
	// to SSE format.
	newBody = make([]byte, 0)
	var buf []byte
	buf, err = io.ReadAll(body)
	if err != nil {
		err = fmt.Errorf("failed to read body: %w", err)
		return
	}
	a.buffered = append(a.buffered, buf...)
	a.convertMessagesEventWrappedInAmazonEventStreamEvent(&newBody, span)
	if endOfStream {
		// Recalculate total tokens before returning
		a.updateTotalTokens()
	}
	return nil, newBody, a.streamingTokenUsage, cmp.Or(a.streamingResponseModel, a.requestModel), nil
}

func (a *anthropicToAWSAnthropicTranslator) convertMessagesEventWrappedInAmazonEventStreamEvent(out *[]byte, span tracingapi.MessageSpan) {
	// TODO: Maybe reuse the reader and decoder.
	r := bytes.NewReader(a.buffered)
	dec := eventstream.NewDecoder()
	var lastRead int64
	for {
		msg, err := dec.Decode(r, nil)
		if err != nil {
			a.buffered = a.buffered[lastRead:]
			return
		}
		// This is undocumented struct used to wrap the actual Anthropicschema.MessagesStreamChunk in AWS eventstream.
		var rawEvent struct {
			Bytes []byte `json:"bytes"`
		}
		if err := json.Unmarshal(msg.Payload, &rawEvent); err != nil {
			lastRead = r.Size() - int64(r.Len())
			continue
		}
		var event anthropicschema.MessagesStreamChunk
		if err := json.Unmarshal(rawEvent.Bytes, &event); err != nil {
			lastRead = r.Size() - int64(r.Len())
			continue
		}
		if span != nil {
			span.RecordResponseChunk(&event)
		}

		a.reflectStreamingEvent(&event)
		*out = append(*out, sseEventPrefix...)
		*out = append(*out, event.Type...)
		*out = append(*out, '\n')
		*out = append(*out, sseDataPrefix...)
		*out = append(*out, rawEvent.Bytes...)
		*out = append(*out, '\n', '\n')
		lastRead = r.Size() - int64(r.Len())
	}
}
