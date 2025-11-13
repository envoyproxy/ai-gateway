// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	ext_procv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

const (
	mimeTypeImageJPEG       = "image/jpeg"
	mimeTypeImagePNG        = "image/png"
	mimeTypeImageGIF        = "image/gif"
	mimeTypeImageWEBP       = "image/webp"
	mimeTypeTextPlain       = "text/plain"
	mimeTypeApplicationJSON = "application/json"
	mimeTypeApplicationEnum = "text/x.enum"
)

var (
	sseDataPrefix  = []byte("data: ")
	sseDoneMessage = []byte("[DONE]")
)

// Define a static constant for a specific moment in time (Unix seconds), this is for test
const ReleaseDateUnix = 1731679200 // Represents 2024-11-15 09:00:00 UTC

// regDataURI follows the web uri regex definition.
// https://developer.mozilla.org/en-US/docs/Web/URI/Schemes/data#syntax
var regDataURI = regexp.MustCompile(`\Adata:(.+?)?(;base64)?,`)

// parseDataURI parse data uri example: data:image/jpeg;base64,/9j/4AAQSkZJRgABAgAAZABkAAD.
func parseDataURI(uri string) (string, []byte, error) {
	matches := regDataURI.FindStringSubmatch(uri)
	if len(matches) != 3 {
		return "", nil, fmt.Errorf("data uri does not have a valid format")
	}
	l := len(matches[0])
	contentType := matches[1]
	bin, err := base64.StdEncoding.DecodeString(uri[l:])
	if err != nil {
		return "", nil, err
	}
	return contentType, bin, nil
}

// buildRequestMutations creates header and body mutations for GCP requests
// It sets the ":path" header, the "content-length" header and the request body.
func buildRequestMutations(path string, reqBody []byte) (*ext_procv3.HeaderMutation, *ext_procv3.BodyMutation) {
	var bodyMutation *ext_procv3.BodyMutation
	var headerMutation *ext_procv3.HeaderMutation

	// Create header mutation.
	if len(path) != 0 {
		headerMutation = &ext_procv3.HeaderMutation{
			SetHeaders: []*corev3.HeaderValueOption{
				{
					Header: &corev3.HeaderValue{
						Key:      ":path",
						RawValue: []byte(path),
					},
				},
			},
		}
	}

	// If the request body is not empty, we set the content-length header and create a body mutation.
	if len(reqBody) != 0 {
		if headerMutation == nil {
			headerMutation = &ext_procv3.HeaderMutation{}
		}
		// Set the "content-length" header.
		headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{
				Key:      httpHeaderKeyContentLength,
				RawValue: []byte(strconv.Itoa(len(reqBody))),
			},
		})

		// Create body mutation.
		bodyMutation = &ext_procv3.BodyMutation{
			Mutation: &ext_procv3.BodyMutation_Body{Body: reqBody},
		}
	}

	return headerMutation, bodyMutation
}

// systemMsgToDeveloperMsg converts OpenAI system message to developer message.
// Since systemMsg is deprecated, this function is provided to maintain backward compatibility.
func systemMsgToDeveloperMsg(msg openai.ChatCompletionSystemMessageParam) openai.ChatCompletionDeveloperMessageParam {
	// Convert OpenAI system message to developer message.
	return openai.ChatCompletionDeveloperMessageParam{
		Name:    msg.Name,
		Role:    openai.ChatMessageRoleDeveloper,
		Content: msg.Content,
	}
}

// construct an openai.ChatCompletionResponseChunk from byte array
func getChatCompletionResponseChunk(body []byte) []openai.ChatCompletionResponseChunk {
	lines := bytes.Split(body, []byte("\n\n"))

	chunks := []openai.ChatCompletionResponseChunk{}
	for _, line := range lines {
		// Remove "data: " prefix from SSE format if present.
		line = bytes.TrimPrefix(line, []byte("data: "))

		// Try to parse as JSON.
		var chunk openai.ChatCompletionResponseChunk
		if err := json.Unmarshal(line, &chunk); err == nil {
			chunks = append(chunks, chunk)
		}
	}
	return chunks
}

// serialize a ChatCompletionResponseChunk, this is common for all chat completion request
func serializeOpenAIChatCompletionChunk(chunk openai.ChatCompletionResponseChunk, buf *[]byte) error {
	var chunkBytes []byte
	chunkBytes, err := json.Marshal(chunk)
	if err != nil {
		return fmt.Errorf("failed to marshal stream chunk: %w", err)
	}
	*buf = append(*buf, sseDataPrefix...)
	*buf = append(*buf, chunkBytes...)
	*buf = append(*buf, '\n', '\n')
	return nil
}
