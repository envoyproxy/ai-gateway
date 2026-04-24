// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
)

// rewriteMultipartModel re-encodes a multipart/form-data body, replacing only the "model" field value
// with newModel. All other parts (including the file upload) are copied verbatim.
// Returns the new body bytes and the new Content-Type header value (with updated boundary).
func rewriteMultipartModel(original []byte, contentType string, newModel string) ([]byte, string, error) {
	boundary, err := parseMultipartBoundary(contentType)
	if err != nil {
		return nil, "", err
	}

	reader := multipart.NewReader(bytes.NewReader(original), boundary)

	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)

	for {
		part, err := reader.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", fmt.Errorf("failed to read multipart part: %w", err)
		}

		if part.FormName() == "model" {
			// Replace model value.
			field, err := writer.CreateFormField("model")
			if err != nil {
				return nil, "", fmt.Errorf("failed to create model field: %w", err)
			}
			if _, err := field.Write([]byte(newModel)); err != nil {
				return nil, "", fmt.Errorf("failed to write model field: %w", err)
			}
		} else {
			// Copy part verbatim with original headers.
			newPart, err := writer.CreatePart(part.Header)
			if err != nil {
				return nil, "", fmt.Errorf("failed to create part: %w", err)
			}
			if _, err := io.Copy(newPart, part); err != nil {
				return nil, "", fmt.Errorf("failed to copy part: %w", err)
			}
		}
	}

	if err := writer.Close(); err != nil {
		return nil, "", fmt.Errorf("failed to close multipart writer: %w", err)
	}

	return buf.Bytes(), writer.FormDataContentType(), nil
}

// parseMultipartBoundary extracts the boundary parameter from a Content-Type header value.
func parseMultipartBoundary(contentType string) (string, error) {
	_, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return "", fmt.Errorf("failed to parse content-type: %w", err)
	}
	boundary := params["boundary"]
	if boundary == "" {
		return "", fmt.Errorf("missing boundary in content-type")
	}
	return boundary, nil
}
