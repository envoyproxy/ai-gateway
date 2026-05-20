// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/json"
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

// File ID prefix used for encoding routing information.
const (
	FileIDPrefix = "file-"
)

var (
	sseDataPrefix   = []byte("data: ")
	sseDoneMessage  = []byte("[DONE]")
	sseDoneFullLine = append(append(sseDataPrefix, sseDoneMessage...), '\n')
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

// serialize a ChatCompletionResponseChunk, this is common for all chat completion request
func serializeOpenAIChatCompletionChunk(chunk *openai.ChatCompletionResponseChunk, buf *[]byte) error {
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

// EncodeFileIDWithRouting encodes a file/batch ID with model and backend routing information.
//
// Format: <prefix><base64url(aigw:<original_id>;model:<model_name>;backend:<backend_name>)>
// The result preserves the original prefix (file-, batch_, etc.) for OpenAI compliance.
//
// Args:
//
//	id: Original file ID from the provider (e.g., "file-abc123")
//	modelName: Model name (e.g., "gpt-4o-mini")
//	backendName: Backend name (e.g., "azure-openai", "openai-primary")
//	idType: Type of ID being encoded. Used to determine the correct prefix.
//	       Defaults to "file". Supported values are "file".
//
// Returns:
//
//	Encoded ID starting with appropriate prefix and containing routing information
//
// Examples:
//
//	EncodeFileIDWithRouting("file-abc123", "gpt-4o-mini", "azure-openai", "file")
//	-> "file-YWlndzpmaWxlLWFiYzEyMzttb2RlbDpncHQtNG8tbWluaTtiYWNrZW5kOmF6dXJlLW9wZW5haQ"
func EncodeFileIDWithRouting(id, modelName, backendName, _ string) string {
	prefix := FileIDPrefix
	// Use "aigw:" prefix to identify AI Gateway encoded IDs, consistent with the proposal
	return prefix + base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "aigw:%s;model:%s;backend:%s", id, modelName, backendName))
}

// DecodeFileIDWithRouting extracts the model name, backend name, and original file id from an encoded file ID.
//
// It expects the encoded ID to be in the format produced by EncodeIDWithRouting, which includes a prefix (file-)
// followed by a base64-encoded string containing the original ID, model name, and backend name.
//
// Args:
//
//	encodedID: The encoded file ID containing routing information.
//
// Returns:
//
//	The extracted model name, backend name, and original file/batch id if decoding is successful,
//	or an error if the format is invalid or decoding fails.
//
// Examples:
//
//	DecodeFileIDWithRouting("file-YWlndzpmaWxlLWFiYzEyMzttb2RlbDpncHQtNG8tbWluaTtiYWNrZW5kOmF6dXJlLW9wZW5haQ")
//	-> "gpt-4o-mini", "azure-openai", "file-abc123", nil
func DecodeFileIDWithRouting(encodedID string) (modelName string, backendName string, id string, err error) {
	var base64Part string
	switch {
	case strings.HasPrefix(encodedID, FileIDPrefix):
		base64Part = strings.TrimPrefix(encodedID, FileIDPrefix)
	default:
		return "", "", "", fmt.Errorf("invalid encoded ID format: missing expected prefix")
	}

	decodedBytes, err := base64.RawURLEncoding.DecodeString(base64Part)
	if err != nil {
		return "", "", "", fmt.Errorf("failed to decode base64 part of the ID: %w", err)
	}
	decodedStr := string(decodedBytes)

	if !strings.HasPrefix(decodedStr, "aigw:") {
		return "", "", "", fmt.Errorf("invalid decoded ID format: expected format 'aigw:<original_id>;model:<model_name>;backend:<backend_name>'")
	}

	// Format: aigw:<id>;model:<model>;backend:<backend>
	parts := strings.Split(strings.TrimPrefix(decodedStr, "aigw:"), ";")
	if len(parts) < 2 {
		return "", "", "", fmt.Errorf("invalid decoded ID format: expected format 'aigw:<original_id>;model:<model_name>;backend:<backend_name>'")
	}

	// Extract ID from first part
	id = parts[0]
	if id == "" {
		return "", "", "", fmt.Errorf("file id not found in decoded Id")
	}

	// Parse remaining parts as key:value pairs
	for _, part := range parts[1:] {
		if key, value, found := strings.Cut(part, ":"); found {
			switch key {
			case "model":
				modelName = value
			case "backend":
				backendName = value
			}
		}
	}

	if modelName == "" {
		return "", "", "", fmt.Errorf("model name not found in decoded Id")
	}

	return modelName, backendName, id, nil
}
