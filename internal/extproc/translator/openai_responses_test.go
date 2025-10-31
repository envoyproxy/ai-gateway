// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"testing"

	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/require"
	"k8s.io/utils/ptr"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

// Tests for Responses API translator (openai_responses.go)
func TestResponsesRequestBody_PathAndModelOverride(t *testing.T) {
	// Ensure path header is set and model override applies
	req := &openai.ResponseRequest{Model: "gpt-4o", Stream: false}
	o := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)
	hm, bm, err := o.RequestBody(nil, req, false)
	require.NoError(t, err)
	require.Nil(t, bm)
	require.NotNil(t, hm)
	require.Len(t, hm.SetHeaders, 1)
	require.Equal(t, ":path", hm.SetHeaders[0].Header.Key)
	require.Equal(t, "/v1/responses", string(hm.SetHeaders[0].Header.RawValue))

	// With override
	override := "gpt-4o-2024-11-20"
	o2 := NewResponsesOpenAIToOpenAITranslator("v1", override).(*openAIToOpenAITranslatorV1Responses)
	// Use a minimal original body to avoid unmarshalling custom union types during mutation
	rawReq := []byte(`{"model":"gpt-4o"}`)
	hm2, bm2, err := o2.RequestBody(rawReq, req, true)
	require.NoError(t, err)
	require.NotNil(t, bm2)
	// ensure body mutation contains the overridden model
	var newReq openai.ResponseRequest
	err = json.Unmarshal(bm2.Mutation.(*extprocv3.BodyMutation_Body).Body, &newReq)
	require.NoError(t, err)
	require.Equal(t, override, newReq.Model)
	require.NotNil(t, hm2)
	require.Len(t, hm2.SetHeaders, 2)
	require.Equal(t, "content-length", hm2.SetHeaders[1].Header.Key)
	require.Equal(t, strconv.Itoa(len(bm2.Mutation.(*extprocv3.BodyMutation_Body).Body)), string(hm2.SetHeaders[1].Header.RawValue))
}

func TestResponses_ResponseBody_NonStreaming_UsageAndModelFallback(t *testing.T) {
	// Non-streaming response with usage and model
	translator := NewResponsesOpenAIToOpenAITranslator("v1", "")

	var resp openai.ResponseResponse
	resp.Model = "gpt-4o-2024-08-06"
	resp.Usage = &openai.ResponseUsage{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}

	body, err := json.Marshal(resp)
	require.NoError(t, err)

	_, _, tokenUsage, responseModel, err := translator.ResponseBody(nil, bytes.NewReader(body), true, nil)
	require.NoError(t, err)
	require.Equal(t, "gpt-4o-2024-08-06", responseModel)
	require.Equal(t, uint32(10), tokenUsage.InputTokens)
	require.Equal(t, uint32(5), tokenUsage.OutputTokens)

	// Response without model should fallback to requestModel
	o := NewResponsesOpenAIToOpenAITranslator("v1", "").(*openAIToOpenAITranslatorV1Responses)
	// set requestModel manually to simulate previous RequestBody call
	o.requestModel = "gpt-4o-mini"

	resp2 := openai.ResponseResponse{}
	resp2.Usage = &openai.ResponseUsage{TotalTokens: 7}
	body2, err := json.Marshal(resp2)
	require.NoError(t, err)

	_, _, tokenUsage2, responseModel2, err := o.ResponseBody(nil, bytes.NewReader(body2), true, nil)
	require.NoError(t, err)
	require.Equal(t, "gpt-4o-mini", responseModel2)
	require.Equal(t, uint32(7), tokenUsage2.TotalTokens)
}

func TestResponses_HandleStreamingResponse_SSEExtraction(t *testing.T) {
	o := &openAIToOpenAITranslatorV1Responses{stream: true}

	sse := `data: {"type":"response.delta","response":{"id":"r1","model":"gpt-4o-2024-11-20"}}

data: {"type":"response.completed","response":{"model":"gpt-4o-2024-11-20","usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}}}

data: [DONE]

`

	_, _, tokenUsage, responseModel, err := o.ResponseBody(nil, bytes.NewReader([]byte(sse)), true, nil)
	require.NoError(t, err)
	require.Equal(t, uint32(3), tokenUsage.InputTokens)
	require.Equal(t, uint32(2), tokenUsage.OutputTokens)
	require.Equal(t, uint32(5), tokenUsage.TotalTokens)
	require.Equal(t, "gpt-4o-2024-11-20", responseModel)
}

func TestResponses_HandleStreamingResponse_PartialBufferingAndInvalidJSON(t *testing.T) {
	o := &openAIToOpenAITranslatorV1Responses{stream: true}

	// send partial invalid data first, then valid completed event
	part1 := []byte("data: invalid\n\n")
	part2 := []byte("data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"total_tokens\":42}}}\n\n")

	// feed first part
	_, _, tokenUsage, _, err := o.ResponseBody(nil, bytes.NewReader(part1), false, nil)
	require.NoError(t, err)
	// no endOfStream yet, so no token usage
	require.Equal(t, LLMTokenUsage{}, tokenUsage)

	// feed second part and mark endOfStream
	_, _, tokenUsage2, responseModel2, err := o.ResponseBody(nil, bytes.NewReader(part2), true, nil)
	require.NoError(t, err)
	require.Equal(t, LLMTokenUsage{TotalTokens: 42}, tokenUsage2)
	require.Empty(t, responseModel2) // no model in completed event, and no requestModel set
}

func TestResponses_ResponseError_Conversion(t *testing.T) {
	// Non-json upstream error -> convert to OpenAI error
	o := &openAIToOpenAITranslatorV1Responses{}
	headers := map[string]string{":status": "503", "content-type": "text/plain"}
	input := bytes.NewBuffer([]byte("service not available"))

	hm, bm, err := o.ResponseError(headers, input)
	require.NoError(t, err)
	require.NotNil(t, bm)
	require.NotNil(t, hm)

	body := bm.Mutation.(*extprocv3.BodyMutation_Body).Body
	var got openai.Error
	err = json.Unmarshal(body, &got)
	require.NoError(t, err)

	want := openai.Error{
		Type: "error",
		Error: openai.ErrorType{
			Type:    openAIBackendError,
			Message: "service not available",
			Code:    ptr.To("503"),
		},
	}

	if !cmp.Equal(got, want) {
		t.Fatalf("unexpected error conversion: %s", cmp.Diff(got, want))
	}
}

func TestResponses_StreamingReadError(t *testing.T) {
	o := &openAIToOpenAITranslatorV1Responses{stream: true}
	pr, pw := io.Pipe()
	_ = pw.CloseWithError(fmt.Errorf("read failure"))

	_, _, _, _, err := o.ResponseBody(nil, pr, false, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "failed to read body")
}
