// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strconv"

	"github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

const (
	mimeTypeImageJPEG = "image/jpeg"
	mimeTypeImagePNG  = "image/png"
	mimeTypeImageGIF  = "image/gif"
	mimeTypeImageWEBP = "image/webp"
	schemaJSON        = "schema.json"
)

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
				Key:      HTTPHeaderKeyContentLength,
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

// processStop handles the 'stop' parameter which can be a string or a slice of strings.
// It normalizes the input into a []*string.
func processStop(data interface{}) ([]*string, error) {
	if data == nil {
		return nil, nil
	}
	switch v := data.(type) {
	case string:
		return []*string{&v}, nil
	case []string:
		result := make([]*string, len(v))
		for i, s := range v {
			temp := s
			result[i] = &temp
		}
		return result, nil
	case []*string:
		return v, nil
	default:
		return nil, fmt.Errorf("invalid type for stop parameter: expected string, []string, []*string, or nil, got %T", v)
	}
}

// coalesce returns the value of the first non-nil pointer,
// or the default value if all pointers are nil.
func coalesce[T any](defaultValue T, ptrs ...*T) T {
	for _, p := range ptrs {
		if p != nil {
			return *p
		}
	}
	return defaultValue
}
