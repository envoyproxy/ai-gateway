// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	typev3 "github.com/envoyproxy/go-control-plane/envoy/type/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/apischema/openai"
)

func TestModels_ProcessRequestHeaders(t *testing.T) {
	cfg := &processorConfig{declaredModels: []string{"openai", "aws-bedrock"}}
	p := NewModelsProcessor(cfg, nil, slog.Default())
	res, err := p.ProcessRequestHeaders(t.Context(), &corev3.HeaderMap{
		Headers: []*corev3.HeaderValue{{Key: "foo", Value: "bar"}},
	})
	require.NoError(t, err)

	ir, ok := res.Response.(*extprocv3.ProcessingResponse_ImmediateResponse)
	require.True(t, ok)
	require.Equal(t, typev3.StatusCode(200), ir.ImmediateResponse.Status.Code)
	require.Equal(t, uint32(0), ir.ImmediateResponse.GrpcStatus.Status)

	respHeaders := headers(ir.ImmediateResponse.Headers.SetHeaders)
	require.Equal(t, "application/json", respHeaders["content-type"])

	var models openai.ModelList
	require.NoError(t, json.Unmarshal(ir.ImmediateResponse.Body, &models))
	require.Equal(t, "list", models.Object)
	require.Len(t, models.Data, len(cfg.declaredModels))
	for i, m := range cfg.declaredModels {
		require.Equal(t, m, models.Data[i].ID)
		require.Equal(t, "model", models.Data[i].Object)
		require.False(t, time.Time(models.Data[i].Created).IsZero())
	}
}

func TestModels_UnimplementedMethods(t *testing.T) {
	p := &modelsProcessor{}
	_, err := p.ProcessRequestBody(t.Context(), &extprocv3.HttpBody{})
	require.ErrorIs(t, err, errUnexpectedCall)
	_, err = p.ProcessResponseHeaders(t.Context(), &corev3.HeaderMap{})
	require.ErrorIs(t, err, errUnexpectedCall)
	_, err = p.ProcessResponseBody(t.Context(), &extprocv3.HttpBody{})
	require.ErrorIs(t, err, errUnexpectedCall)
}

func headers(in []*corev3.HeaderValueOption) map[string]string {
	h := make(map[string]string)
	for _, v := range in {
		h[v.Header.Key] = string(v.Header.RawValue)
	}
	return h
}
