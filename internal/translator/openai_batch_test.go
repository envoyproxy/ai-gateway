// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// batchTestHeaders returns a minimal header map as seen by the translator at request time.
func batchTestHeaders(model, backend string) map[string]string {
	return map[string]string{
		internalapi.ModelNameHeaderKeyDefault: model,
		internalapi.BackendNameHeaderKey:      backend,
	}
}

// batchRetrieveCancelHeaders adds OriginalFileIDHeaderKey and DecodedFileIDHeaderKey on top.
func batchRetrieveCancelHeaders(model, backend, originalID, decodedID string) map[string]string {
	h := batchTestHeaders(model, backend)
	h[internalapi.OriginalFileIDHeaderKey] = originalID
	h[internalapi.DecodedFileIDHeaderKey] = decodedID
	return h
}

// getField is a convenience wrapper around gjson for test assertions.
func getField(body []byte, path string) string {
	return gjson.GetBytes(body, path).String()
}

// TestCreateBatch_ResponseBody_EncodesAllIDs verifies that create-batch ResponseBody encodes
// the batch id (batch_ prefix), output_file_id and error_file_id (file- prefix).
func TestCreateBatch_ResponseBody_EncodesAllIDs(t *testing.T) {
	tr := NewCreateBatchOpenAIToOpenAITranslator("", "")
	_, _, _ = tr.RequestBody(batchTestHeaders("gpt-4o-mini", "default.openai"), nil, nil, false)

	rawBody := `{"id":"batch_abc","status":"completed","output_file_id":"file-out123","error_file_id":"file-err456","object":"batch"}`
	_, body, _, _, err := tr.ResponseBody(nil, strings.NewReader(rawBody), false, nil)
	require.NoError(t, err)

	// batch id must use batch_ prefix and decode correctly.
	encodedBatchID := getField(body, "id")
	require.True(t, strings.HasPrefix(encodedBatchID, BatchIDPrefix), "batch id must use batch_ prefix")
	m, b, rawID, err := DecodeFileIDWithRouting(encodedBatchID)
	require.NoError(t, err)
	require.Equal(t, "batch_abc", rawID)
	require.Equal(t, "gpt-4o-mini", m)
	require.Equal(t, "default.openai", b)

	// output_file_id must use file- prefix.
	encodedOut := getField(body, "output_file_id")
	require.True(t, strings.HasPrefix(encodedOut, FileIDPrefix), "output_file_id must use file- prefix")
	m, b, rawID, err = DecodeFileIDWithRouting(encodedOut)
	require.NoError(t, err)
	require.Equal(t, "file-out123", rawID)
	require.Equal(t, "gpt-4o-mini", m)
	require.Equal(t, "default.openai", b)

	// error_file_id must use file- prefix.
	encodedErr := getField(body, "error_file_id")
	require.True(t, strings.HasPrefix(encodedErr, FileIDPrefix), "error_file_id must use file- prefix")
	m, b, rawID, err = DecodeFileIDWithRouting(encodedErr)
	require.NoError(t, err)
	require.Equal(t, "file-err456", rawID)
	require.Equal(t, "gpt-4o-mini", m)
	require.Equal(t, "default.openai", b)
}

// TestCreateBatch_ResponseBody_AbsentFileIDsUnchanged verifies that absent/empty
// output_file_id and error_file_id are not written into the response.
func TestCreateBatch_ResponseBody_AbsentFileIDsUnchanged(t *testing.T) {
	tr := NewCreateBatchOpenAIToOpenAITranslator("", "")
	_, _, _ = tr.RequestBody(batchTestHeaders("gpt-4o-mini", "default.openai"), nil, nil, false)

	rawBody := `{"id":"batch_xyz","status":"in_progress","object":"batch"}`
	_, body, _, _, err := tr.ResponseBody(nil, strings.NewReader(rawBody), false, nil)
	require.NoError(t, err)

	require.Empty(t, getField(body, "output_file_id"))
	require.Empty(t, getField(body, "error_file_id"))
}

// TestListBatches_ResponseBody_Passthrough verifies list-batches ResponseBody returns upstream
// response as-is without encoding IDs, similar to ListFiles endpoint.
func TestListBatches_ResponseBody_Passthrough(t *testing.T) {
	tr := NewListBatchesOpenAIToOpenAITranslator("", "")
	_, _, _ = tr.RequestBody(batchTestHeaders("gpt-4o", "default.openai"), nil, nil, false)

	rawBody := `{"object":"list","data":[` +
		`{"id":"batch_1","status":"completed","output_file_id":"file-o1","error_file_id":"file-e1"},` +
		`{"id":"batch_2","status":"in_progress"}` +
		`]}`
	headers, body, _, _, err := tr.ResponseBody(nil, strings.NewReader(rawBody), false, nil)
	require.NoError(t, err)

	// Verify response is returned as-is (passthrough), with no mutation.
	require.Nil(t, headers)
	require.Nil(t, body)
}

// TestRetrieveBatch_ResponseBody_EncodesAllIDs verifies retrieve-batch ResponseBody echoes the
// batch id and encodes output_file_id / error_file_id.
func TestRetrieveBatch_ResponseBody_EncodesAllIDs(t *testing.T) {
	tr := NewRetrieveBatchOpenAIToOpenAITranslator("", "")
	encodedBatchID := EncodeFileIDWithRouting("batch_abc", "gpt-4o-mini", "default.openai", "batch")
	_, _, _ = tr.RequestBody(
		batchRetrieveCancelHeaders("gpt-4o-mini", "default.openai", encodedBatchID, "batch_abc"),
		nil, nil, false,
	)

	rawBody := `{"id":"batch_abc","status":"completed","output_file_id":"file-out789","error_file_id":"file-err321","object":"batch"}`
	_, body, _, _, err := tr.ResponseBody(nil, strings.NewReader(rawBody), false, nil)
	require.NoError(t, err)

	// id is echoed back as the original encoded batch id (not re-encoded).
	require.Equal(t, encodedBatchID, getField(body, "id"))

	encodedOut := getField(body, "output_file_id")
	require.True(t, strings.HasPrefix(encodedOut, FileIDPrefix))
	_, _, rawOut, err := DecodeFileIDWithRouting(encodedOut)
	require.NoError(t, err)
	require.Equal(t, "file-out789", rawOut)

	encodedErr := getField(body, "error_file_id")
	require.True(t, strings.HasPrefix(encodedErr, FileIDPrefix))
	_, _, rawErr, err := DecodeFileIDWithRouting(encodedErr)
	require.NoError(t, err)
	require.Equal(t, "file-err321", rawErr)
}

// TestCancelBatch_ResponseBody_EncodesAllIDs verifies cancel-batch ResponseBody echoes the
// batch id and encodes output_file_id; empty error_file_id is left unchanged.
func TestCancelBatch_ResponseBody_EncodesAllIDs(t *testing.T) {
	tr := NewCancelBatchOpenAIToOpenAITranslator("", "")
	encodedBatchID := EncodeFileIDWithRouting("batch_xyz", "gpt-4.1", "openai-primary", "batch")
	_, _, _ = tr.RequestBody(
		batchRetrieveCancelHeaders("gpt-4.1", "openai-primary", encodedBatchID, "batch_xyz"),
		nil, nil, false,
	)

	rawBody := `{"id":"batch_xyz","status":"cancelling","output_file_id":"file-partial","error_file_id":"","object":"batch"}`
	_, body, _, _, err := tr.ResponseBody(nil, strings.NewReader(rawBody), false, nil)
	require.NoError(t, err)

	require.Equal(t, encodedBatchID, getField(body, "id"))

	encodedOut := getField(body, "output_file_id")
	require.True(t, strings.HasPrefix(encodedOut, FileIDPrefix))
	_, _, rawOut, err := DecodeFileIDWithRouting(encodedOut)
	require.NoError(t, err)
	require.Equal(t, "file-partial", rawOut)

	// Empty error_file_id must stay empty – do not encode an empty string.
	require.Empty(t, getField(body, "error_file_id"))
}

// TestCreateBatch_ResponseBody_EmptyFileIDsUnchanged verifies that explicitly empty
// output_file_id and error_file_id are left as empty strings (not encoded).
func TestCreateBatch_ResponseBody_EmptyFileIDsUnchanged(t *testing.T) {
	tr := NewCreateBatchOpenAIToOpenAITranslator("", "")
	_, _, _ = tr.RequestBody(batchTestHeaders("gpt-4o-mini", "default.openai"), nil, nil, false)

	rawBody := `{"id":"batch_empty","status":"completed","output_file_id":"","error_file_id":"","object":"batch"}`
	_, body, _, _, err := tr.ResponseBody(nil, strings.NewReader(rawBody), false, nil)
	require.NoError(t, err)

	require.Empty(t, getField(body, "output_file_id"))
	require.Empty(t, getField(body, "error_file_id"))
}
