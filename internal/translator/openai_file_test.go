// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

func TestCreateFileOpenAIToOpenAITranslatorRequestBody(t *testing.T) {
	for _, tc := range []struct {
		name              string
		modelNameOverride internalapi.ModelNameOverride
		requestBody       string
		expPath           string
		expError          bool
		expErrorMsg       string
		forceBodyMutation bool
	}{
		{
			name:        "valid_file_upload",
			expPath:     "/v1/files",
			requestBody: `{}`,
		},
		{
			name:              "with_model_name_override",
			modelNameOverride: "custom-model",
			expPath:           "/v1/files",
			requestBody:       `{}`,
		},
		{
			name:        "missing_model_name",
			expPath:     "/v1/files",
			requestBody: `{}`,
			expError:    true,
			expErrorMsg: "'model' parameter should be passed as extra field for file upload",
		},
		{
			name:              "with_force_body_mutation",
			modelNameOverride: "",
			expPath:           "/v1/files",
			requestBody:       `{}`,
			forceBodyMutation: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewCreateFileOpenAIToOpenAITranslator("v1", tc.modelNameOverride)
			impl := translator.(*openAIToOpenAITranslatorV1CreateFile)

			// Create params with model in ExtraBody (except for missing_model_name test)
			params := &openai.FileNewParams{
				ExtraBody: map[string]any{},
			}
			if tc.name != "missing_model_name" {
				params.ExtraBody["model"] = []byte("test-model")
			}

			headerMutation, _, err := translator.RequestBody(nil, []byte(tc.requestBody), params, tc.forceBodyMutation)

			if tc.expError {
				require.Error(t, err)
				if tc.expErrorMsg != "" {
					require.Contains(t, err.Error(), tc.expErrorMsg)
				}
				return
			}

			require.NoError(t, err)
			require.NotNil(t, headerMutation)
			require.GreaterOrEqual(t, len(headerMutation), 1)
			require.Equal(t, pathHeaderName, headerMutation[0].Key())
			require.Equal(t, tc.expPath, headerMutation[0].Value())

			// Verify the model is stored
			require.Equal(t, "test-model", impl.requestModel)
		})
	}
}

func TestCreateFileOpenAIToOpenAITranslatorResponseHeaders(t *testing.T) {
	translator := NewCreateFileOpenAIToOpenAITranslator("v1", "")
	headerMutation, err := translator.ResponseHeaders(map[string]string{})
	require.NoError(t, err)
	require.Nil(t, headerMutation)
}

func TestCreateFileOpenAIToOpenAITranslatorResponseBody(t *testing.T) {
	for _, tc := range []struct {
		name         string
		responseBody string
		expError     bool
		expModel     string
	}{
		{
			name: "valid_file_object",
			responseBody: `{
				"id": "file-123",
				"object": "file",
				"bytes": 1024,
				"created_at": 1677649420,
				"filename": "test.txt",
				"purpose": "assistants"
			}`,
			expModel: "",
		},
		{
			name: "valid_file_object_with_all_fields",
			responseBody: `{
				"id": "file-456",
				"object": "file",
				"bytes": 2048,
				"created_at": 1677649420,
				"filename": "data.jsonl",
				"purpose": "fine-tune",
				"status": "uploaded"
			}`,
			expModel: "",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewCreateFileOpenAIToOpenAITranslator("v1", "")
			impl := translator.(*openAIToOpenAITranslatorV1CreateFile)
			impl.requestModel = "gpt-4"

			respHeaders := map[string]string{
				"content-type": "application/json",
			}

			_, bodyMutation, tokenUsage, _, err := translator.ResponseBody(
				respHeaders,
				strings.NewReader(tc.responseBody),
				true,
				nil,
			)

			if tc.expError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tokenUsageFrom(-1, -1, -1, -1, -1), tokenUsage)

			// Verify body was mutated with encoded ID
			require.NotNil(t, bodyMutation)
		})
	}
}

func TestCreateFileOpenAIToOpenAITranslatorResponseError(t *testing.T) {
	translator := NewCreateFileOpenAIToOpenAITranslator("v1", "")

	t.Run("json_error_passthrough", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "400",
			contentTypeHeaderName: jsonContentType,
		}
		errorBody := `{"error": {"message": "Invalid file format", "type": "InvalidRequestError", "code": null}}`

		_, _, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
	})

	t.Run("non_json_error", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "500",
			contentTypeHeaderName: "text/plain",
		}
		errorBody := "Internal Server Error"

		_, _, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
	})
}

func TestRetrieveFileOpenAIToOpenAITranslatorRequestBody(t *testing.T) {
	for _, tc := range []struct {
		name              string
		modelNameOverride internalapi.ModelNameOverride
		originalFileID    string
		decodedFileID     string
		expPath           string
	}{
		{
			name:           "valid_file_id",
			originalFileID: "file-123",
			decodedFileID:  "file-123",
			expPath:        "/v1/files/file-123",
		},
		{
			name:           "encoded_file_id",
			originalFileID: "file-456",
			decodedFileID:  "file-456",
			expPath:        "/v1/files/file-456",
		},
		{
			name:              "with_model_override",
			modelNameOverride: "custom-model",
			originalFileID:    "file-789",
			decodedFileID:     "file-789",
			expPath:           "/v1/files/file-789",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewRetrieveFileOpenAIToOpenAITranslator("v1", tc.modelNameOverride)

			reqHeaders := map[string]string{
				internalapi.OriginalFileIDHeaderKey: tc.originalFileID,
				internalapi.DecodedFileIDHeaderKey:  tc.decodedFileID,
			}

			headerMutation, _, err := translator.RequestBody(reqHeaders, []byte{}, &struct{}{}, false)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)
			require.GreaterOrEqual(t, len(headerMutation), 1)
			require.Equal(t, pathHeaderName, headerMutation[0].Key())
			require.Equal(t, tc.expPath, headerMutation[0].Value())
		})
	}
}

func TestRetrieveFileOpenAIToOpenAITranslatorResponseHeaders(t *testing.T) {
	translator := NewRetrieveFileOpenAIToOpenAITranslator("v1", "")
	headerMutation, err := translator.ResponseHeaders(map[string]string{})
	require.NoError(t, err)
	require.Nil(t, headerMutation)
}

func TestRetrieveFileOpenAIToOpenAITranslatorResponseBody(t *testing.T) {
	for _, tc := range []struct {
		name         string
		responseBody string
		expError     bool
	}{
		{
			name: "valid_file_object",
			responseBody: `{
				"id": "file-123",
				"object": "file",
				"bytes": 1024,
				"created_at": 1677649420,
				"filename": "test.txt",
				"purpose": "assistants"
			}`,
		},
		{
			name: "file_with_status",
			responseBody: `{
				"id": "file-456",
				"object": "file",
				"bytes": 2048,
				"created_at": 1677649420,
				"filename": "data.jsonl",
				"purpose": "fine-tune",
				"status": "processed"
			}`,
		},
		{
			name:         "invalid_json",
			responseBody: `invalid json`,
			expError:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewRetrieveFileOpenAIToOpenAITranslator("v1", "")

			respHeaders := map[string]string{
				"content-type": "application/json",
			}

			_, _, tokenUsage, _, err := translator.ResponseBody(
				respHeaders,
				strings.NewReader(tc.responseBody),
				true,
				nil,
			)

			if tc.expError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tokenUsageFrom(-1, -1, -1, -1, -1), tokenUsage)

			// bodyMutation may be nil if requestFileID is not set
		})
	}
}

func TestRetrieveFileOpenAIToOpenAITranslatorResponseError(t *testing.T) {
	translator := NewRetrieveFileOpenAIToOpenAITranslator("v1", "")

	t.Run("file_not_found_error", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "404",
			contentTypeHeaderName: jsonContentType,
		}
		errorBody := `{"error": {"message": "File not found", "type": "NotFoundError", "code": null}}`

		_, _, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
	})

	t.Run("authorization_error", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "401",
			contentTypeHeaderName: jsonContentType,
		}
		errorBody := `{"error": {"message": "Unauthorized", "type": "AuthenticationError", "code": null}}`

		_, _, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
	})
}

func TestRetrieveFileContentOpenAIToOpenAITranslatorRequestBody(t *testing.T) {
	for _, tc := range []struct {
		name          string
		decodedFileID string
		expPath       string
	}{
		{
			name:          "valid_file_id",
			decodedFileID: "file-123",
			expPath:       "/v1/files/file-123/content",
		},
		{
			name:          "different_file_id",
			decodedFileID: "file-456",
			expPath:       "/v1/files/file-456/content",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewRetrieveFileContentOpenAIToOpenAITranslator("v1", "")

			reqHeaders := map[string]string{
				internalapi.DecodedFileIDHeaderKey: tc.decodedFileID,
			}

			headerMutation, bodyMutation, err := translator.RequestBody(reqHeaders, []byte{}, &struct{}{}, false)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)
			require.GreaterOrEqual(t, len(headerMutation), 1)
			require.Equal(t, pathHeaderName, headerMutation[0].Key())
			require.Equal(t, tc.expPath, headerMutation[0].Value())
			require.Nil(t, bodyMutation)
		})
	}
}

func TestRetrieveFileContentOpenAIToOpenAITranslatorResponseHeaders(t *testing.T) {
	translator := NewRetrieveFileContentOpenAIToOpenAITranslator("v1", "")
	headerMutation, err := translator.ResponseHeaders(map[string]string{})
	require.NoError(t, err)
	require.Nil(t, headerMutation)
}

func TestRetrieveFileContentOpenAIToOpenAITranslatorResponseBody(t *testing.T) {
	t.Run("file_content_passthrough", func(t *testing.T) {
		translator := NewRetrieveFileContentOpenAIToOpenAITranslator("v1", "")
		respHeaders := map[string]string{
			"content-type": "application/octet-stream",
		}
		fileContent := "This is the file content"

		headerMutation, bodyMutation, tokenUsage, _, err := translator.ResponseBody(
			respHeaders,
			strings.NewReader(fileContent),
			true,
			nil,
		)

		require.NoError(t, err)
		require.Nil(t, headerMutation)
		require.Nil(t, bodyMutation)
		require.Equal(t, tokenUsageFrom(-1, -1, -1, -1, -1), tokenUsage)
	})
}

func TestRetrieveFileContentOpenAIToOpenAITranslatorResponseError(t *testing.T) {
	translator := NewRetrieveFileContentOpenAIToOpenAITranslator("v1", "")

	t.Run("file_not_found", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "404",
			contentTypeHeaderName: jsonContentType,
		}
		errorBody := `{"error": {"message": "File not found", "type": "NotFoundError", "code": null}}`

		_, _, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
	})
}

func TestDeleteFileOpenAIToOpenAITranslatorRequestBody(t *testing.T) {
	for _, tc := range []struct {
		name           string
		originalFileID string
		decodedFileID  string
		expPath        string
	}{
		{
			name:           "valid_file_id",
			originalFileID: "file-123",
			decodedFileID:  "file-123",
			expPath:        "/v1/files/file-123",
		},
		{
			name:           "different_file_id",
			originalFileID: "file-789",
			decodedFileID:  "file-789",
			expPath:        "/v1/files/file-789",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewDeleteFileOpenAIToOpenAITranslator("v1", "")

			reqHeaders := map[string]string{
				internalapi.OriginalFileIDHeaderKey: tc.originalFileID,
				internalapi.DecodedFileIDHeaderKey:  tc.decodedFileID,
			}

			headerMutation, _, err := translator.RequestBody(reqHeaders, []byte{}, &struct{}{}, false)
			require.NoError(t, err)
			require.NotNil(t, headerMutation)
			require.GreaterOrEqual(t, len(headerMutation), 1)
			require.Equal(t, pathHeaderName, headerMutation[0].Key())
			require.Equal(t, tc.expPath, headerMutation[0].Value())
		})
	}
}

func TestDeleteFileOpenAIToOpenAITranslatorResponseHeaders(t *testing.T) {
	translator := NewDeleteFileOpenAIToOpenAITranslator("v1", "")
	headerMutation, err := translator.ResponseHeaders(map[string]string{})
	require.NoError(t, err)
	require.Nil(t, headerMutation)
}

func TestDeleteFileOpenAIToOpenAITranslatorResponseBody(t *testing.T) {
	for _, tc := range []struct {
		name         string
		responseBody string
		expError     bool
	}{
		{
			name: "valid_delete_response",
			responseBody: `{
				"id": "file-123",
				"object": "file",
				"deleted": true
			}`,
		},
		{
			name: "file_deleted",
			responseBody: `{
				"id": "file-456",
				"object": "file",
				"deleted": true
			}`,
		},
		{
			name:         "invalid_json",
			responseBody: `invalid json`,
			expError:     true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			translator := NewDeleteFileOpenAIToOpenAITranslator("v1", "")

			respHeaders := map[string]string{
				"content-type": "application/json",
			}

			_, bodyMutation, tokenUsage, _, err := translator.ResponseBody(
				respHeaders,
				strings.NewReader(tc.responseBody),
				true,
				nil,
			)

			if tc.expError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.Equal(t, tokenUsageFrom(-1, -1, -1, -1, -1), tokenUsage)
			require.NotNil(t, bodyMutation)
		})
	}
}

func TestDeleteFileOpenAIToOpenAITranslatorResponseError(t *testing.T) {
	translator := NewDeleteFileOpenAIToOpenAITranslator("v1", "")

	t.Run("file_not_found", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "404",
			contentTypeHeaderName: jsonContentType,
		}
		errorBody := `{"error": {"message": "File not found", "type": "NotFoundError", "code": null}}`

		_, _, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
	})

	t.Run("permission_denied", func(t *testing.T) {
		respHeaders := map[string]string{
			statusHeaderName:      "403",
			contentTypeHeaderName: jsonContentType,
		}
		errorBody := `{"error": {"message": "Permission denied", "type": "PermissionError", "code": null}}`

		_, _, err := translator.ResponseError(respHeaders, strings.NewReader(errorBody))
		require.NoError(t, err)
	})
}
