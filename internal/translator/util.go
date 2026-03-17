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

// File and Batch ID prefixes used for encoding routing information.
const (
	FileIDPrefix  = "file-"
	BatchIDPrefix = "batch_"
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

// EncodeIDWithModel encodes a file/batch ID with model routing information.
//
// Format: <prefix><base64(id:<original_id>;model:<model_name>)>
// The result preserves the original prefix (file-, batch_, etc.) for OpenAI compliance.
//
// Args:
//
//	id: Original file/batch ID from the provider (e.g., "file-abc123", "batch_xyz")
//	modelName: Model name (e.g., "gpt-4o-mini")
//	idType: Type of ID being encoded. Used to determine the correct prefix.
//	       Defaults to "file". Supported values are "file" and "batch".
//
// Returns:
//
//	Encoded ID starting with appropriate prefix and containing routing information
//
// Examples:
//
//	EncodeIDWithModel("file-abc123", "gpt-4o-mini", "file")
//	-> "file-aWQ6ZmlsZS1hYmMxMjM7bW9kZWw6Z3B0LTRvLW1pbmk"
//
//	EncodeIDWithModel("3814889423749775360", "gemini-2.5-pro", "batch")
//	-> "batch_aWQ6MzgxNDg4OTQyMzc0OTc3NTM2MDttb2RlbDpnZW1pbmktMi41LXBybw"
func EncodeIDWithModel(id, modelName, idType string) string {
	prefix := FileIDPrefix
	if idType == "batch" {
		prefix = BatchIDPrefix
	}
	return prefix + base64.RawURLEncoding.EncodeToString(fmt.Appendf(nil, "id:%s;model:%s", id, modelName))
}

// DecodeFileID extracts the model name and original batch / file id from an encoded file/batch ID.
//
// It expects the encoded ID to be in the format produced by EncodeIDWithModel, which includes a prefix (file- or batch_)
// followed by a base64-encoded string containing the original ID and model name.
//
// Args:
//
//	encodedID: The encoded file/batch ID containing routing information.
//
// Returns:
//
//	The extracted model name, original file / batch id if decoding is successful, or an error if the format is invalid or decoding fails.
//
// Examples:
//
//	DecodeFileID("file-aWQ6ZmlsZS1hYmMxMjM7bW9kZWw6Z3B0LTRvLW1pbmk")
//	-> "gpt-4o-mini", "file-abc123", nil
//
//	DecodeFileID("batch_aWQ6MzgxNDg4OTQyMzc0OTc3NTM2MDttb2RlbDpnZW1pbmktMi41LXBybw")
//	-> "gemini-2.5-pro", "3814889423749775360", nil
func DecodeFileID(encodedID string) (modelName string, id string, err error) {
	var base64Part string
	switch {
	case strings.HasPrefix(encodedID, FileIDPrefix):
		base64Part = strings.TrimPrefix(encodedID, FileIDPrefix)
	case strings.HasPrefix(encodedID, BatchIDPrefix):
		base64Part = strings.TrimPrefix(encodedID, BatchIDPrefix)
	default:
		return "", "", fmt.Errorf("invalid encoded ID format: missing expected prefix")
	}

	decodedBytes, err := base64.RawURLEncoding.DecodeString(base64Part)
	if err != nil {
		return "", "", fmt.Errorf("failed to decode base64 part of the ID: %w", err)
	}
	decodedStr := string(decodedBytes)
	parts := strings.Split(decodedStr, ";")
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid decoded ID format: expected format 'id:<original_id>;model:<model_name>'")
	}

	modelName, found := strings.CutPrefix(parts[1], "model:")
	if !found || modelName == "" {
		return "", "", fmt.Errorf("model name not found in decoded Id")
	}
	id, found = strings.CutPrefix(parts[0], "id:")
	if !found || id == "" {
		return "", "", fmt.Errorf("file/batch id not found in decoded Id ")
	}
	return
}
