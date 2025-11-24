// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package filterapi

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/llmcostcel"
)

func TestServer_LoadConfig(t *testing.T) {
	now := time.Now()

	t.Run("ok", func(t *testing.T) {
		config := &Config{
			LLMRequestCosts: []LLMRequestCost{
				{MetadataKey: "key", Type: LLMRequestCostTypeOutputToken},
				{MetadataKey: "cel_key", Type: LLMRequestCostTypeCEL, CEL: "1 + 1"},
			},
			Backends: []Backend{
				{Name: "kserve", Schema: VersionedAPISchema{Name: APISchemaOpenAI}},
				{Name: "awsbedrock", Schema: VersionedAPISchema{Name: APISchemaAWSBedrock}},
				{Name: "openai", Schema: VersionedAPISchema{Name: APISchemaOpenAI}, Auth: &BackendAuth{APIKey: &APIKeyAuth{Key: "dummy"}}},
			},
			Models: []Model{
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
		rc, err := NewRuntimeConfig(t.Context(), config, func(_ context.Context, b *BackendAuth) (BackendAuthHandler, error) {
			require.NotNil(t, b)
			require.NotNil(t, b.APIKey)
			require.Equal(t, "dummy", b.APIKey.Key)
			return nil, nil
		})
		require.NoError(t, err)

		require.NotNil(t, rc)

		require.Len(t, rc.RequestCosts, 2)
		require.Equal(t, LLMRequestCostTypeOutputToken, rc.RequestCosts[0].Type)
		require.Equal(t, "key", rc.RequestCosts[0].MetadataKey)
		require.Equal(t, LLMRequestCostTypeCEL, rc.RequestCosts[1].Type)
		require.Equal(t, "1 + 1", rc.RequestCosts[1].CEL)
		prog := rc.RequestCosts[1].CELProg
		require.NotNil(t, prog)
		val, err := llmcostcel.EvaluateProgram(prog, "", "", 1, 1, 1, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(2), val)
		require.Equal(t, config.Models, rc.DeclaredModels)
	})
}
