// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package runtimefc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
)

func TestServer_LoadConfig(t *testing.T) {
	now := time.Now()

	t.Run("ok", func(t *testing.T) {
		config := &filterapi.Config{
			LLMRequestCosts: []filterapi.LLMRequestCost{
				{MetadataKey: "key", Type: filterapi.LLMRequestCostTypeOutputToken},
				{MetadataKey: "cel_key", Type: filterapi.LLMRequestCostTypeCEL, CEL: "1 + 1"},
			},
			Backends: []filterapi.Backend{
				{Name: "kserve", Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}},
				{Name: "awsbedrock", Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaAWSBedrock}},
				{Name: "openai", Schema: filterapi.VersionedAPISchema{Name: filterapi.APISchemaOpenAI}, Auth: &filterapi.BackendAuth{APIKey: &filterapi.APIKeyAuth{Key: ""}}},
			},
			Models: []filterapi.Model{
				{
					Name:      "llama3.3333",
					OwnedBy:   "meta",
					CreatedAt: now,
				},
				{
					Name:      "gpt4.4444",
					OwnedBy:   "openai",
					CreatedAt: now,
				},
			},
		}
		rc, err := NewRuntimeFilterConfig(t.Context(), config)
		require.NoError(t, err)

		require.NotNil(t, rc)

		require.Len(t, rc.RequestCosts, 2)
		require.Equal(t, filterapi.LLMRequestCostTypeOutputToken, rc.RequestCosts[0].Type)
		require.Equal(t, "key", rc.RequestCosts[0].MetadataKey)
		require.Equal(t, filterapi.LLMRequestCostTypeCEL, rc.RequestCosts[1].Type)
		require.Equal(t, "1 + 1", rc.RequestCosts[1].CEL)
		prog := rc.RequestCosts[1].CELProg
		require.NotNil(t, prog)
		val, err := llmcostcel.EvaluateProgram(prog, "", "", 1, 1, 1, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(2), val)
		require.Equal(t, config.Models, rc.DeclaredModels)
	})
}
