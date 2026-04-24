// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"errors"
	"io"
	"mime/multipart"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRewriteMultipartModel(t *testing.T) {
	t.Run("rewrites model field", func(t *testing.T) {
		body, contentType := buildMultipartBody(t, map[string]string{"model": "whisper-1"}, "file", "test.mp3", []byte("audio-data"))

		newBody, newCT, err := rewriteMultipartModel(body, contentType, "whisper-large-v3")
		require.NoError(t, err)
		require.NotEmpty(t, newBody)
		require.Contains(t, newCT, "multipart/form-data")

		fields := parseMultipartFields(t, newBody, newCT)
		require.Equal(t, "whisper-large-v3", fields["model"])
		require.Equal(t, "audio-data", fields["file"])
	})

	t.Run("preserves other fields", func(t *testing.T) {
		body, contentType := buildMultipartBody(t,
			map[string]string{"model": "whisper-1", "language": "en", "prompt": "test prompt"},
			"file", "test.mp3", []byte("audio-data"),
		)

		newBody, newCT, err := rewriteMultipartModel(body, contentType, "new-model")
		require.NoError(t, err)

		fields := parseMultipartFields(t, newBody, newCT)
		require.Equal(t, "new-model", fields["model"])
		require.Equal(t, "en", fields["language"])
		require.Equal(t, "test prompt", fields["prompt"])
		require.Equal(t, "audio-data", fields["file"])
	})

	t.Run("invalid content type", func(t *testing.T) {
		_, _, err := rewriteMultipartModel([]byte("data"), "text/plain", "new-model")
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing boundary")
	})

	t.Run("empty boundary", func(t *testing.T) {
		_, _, err := rewriteMultipartModel([]byte("data"), "multipart/form-data", "new-model")
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing boundary")
	})
}

func TestRewriteMultipartModel_CorruptBody(t *testing.T) {
	t.Run("corrupt multipart body", func(t *testing.T) {
		ct := "multipart/form-data; boundary=myboundary"
		_, _, err := rewriteMultipartModel([]byte("this is not a valid multipart body"), ct, "new-model")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to read multipart part")
	})

	t.Run("unparseable content type", func(t *testing.T) {
		_, _, err := rewriteMultipartModel([]byte("data"), ";;;invalid", "new-model")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse content-type")
	})
}

func TestParseMultipartBoundary(t *testing.T) {
	t.Run("valid content type", func(t *testing.T) {
		b, err := parseMultipartBoundary("multipart/form-data; boundary=----WebKitFormBoundary")
		require.NoError(t, err)
		require.Equal(t, "----WebKitFormBoundary", b)
	})

	t.Run("missing boundary", func(t *testing.T) {
		_, err := parseMultipartBoundary("multipart/form-data")
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing boundary")
	})

	t.Run("not multipart", func(t *testing.T) {
		_, err := parseMultipartBoundary("application/json")
		require.Error(t, err)
		require.Contains(t, err.Error(), "missing boundary")
	})

	t.Run("unparseable content type", func(t *testing.T) {
		_, err := parseMultipartBoundary(";;;invalid")
		require.Error(t, err)
		require.Contains(t, err.Error(), "failed to parse content-type")
	})
}

// buildMultipartBody creates a multipart/form-data body with the given text fields and a file.
func buildMultipartBody(t *testing.T, fields map[string]string, fileField, fileName string, fileData []byte) ([]byte, string) {
	t.Helper()
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	for k, v := range fields {
		require.NoError(t, writer.WriteField(k, v))
	}
	if fileField != "" {
		part, err := writer.CreateFormFile(fileField, fileName)
		require.NoError(t, err)
		_, err = part.Write(fileData)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	return buf.Bytes(), writer.FormDataContentType()
}

// parseMultipartFields parses a multipart body and returns all form fields as key-value pairs.
// File parts are returned with their content as the value.
func parseMultipartFields(t *testing.T, body []byte, contentType string) map[string]string {
	t.Helper()
	boundary, err := parseMultipartBoundary(contentType)
	require.NoError(t, err)

	reader := multipart.NewReader(bytes.NewReader(body), boundary)
	fields := make(map[string]string)
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		data, err := io.ReadAll(part)
		require.NoError(t, err)
		fields[part.FormName()] = string(data)
	}
	return fields
}
