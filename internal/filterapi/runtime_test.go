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
		val, err := llmcostcel.EvaluateProgram(prog, "", "", 1, 1, 1, 1, 1)
		require.NoError(t, err)
		require.Equal(t, uint64(2), val)
		require.Equal(t, config.Models, rc.DeclaredModels)
	})
}

// TestNewRuntimeConfig_BackendLevelLLMRequestCosts tests that backend-level LLMRequestCosts are
// properly compiled into RuntimeRequestCosts. This is part of the fix for Issue #1688.
func TestNewRuntimeConfig_BackendLevelLLMRequestCosts(t *testing.T) {
	config := &Config{
		// Global costs for backward compatibility
		LLMRequestCosts: []LLMRequestCost{
			{MetadataKey: "global_key", Type: LLMRequestCostTypeOutputToken},
		},
		Backends: []Backend{
			{
				Name:   "free-backend",
				Schema: VersionedAPISchema{Name: APISchemaOpenAI},
				// Backend-specific costs
				LLMRequestCosts: []LLMRequestCost{
					{MetadataKey: "billing", Type: LLMRequestCostTypeCEL, CEL: "0"},
				},
			},
			{
				Name:   "paid-backend",
				Schema: VersionedAPISchema{Name: APISchemaOpenAI},
				// Backend-specific costs with different CEL expression
				LLMRequestCosts: []LLMRequestCost{
					{MetadataKey: "billing", Type: LLMRequestCostTypeCEL, CEL: "input_tokens * uint(2) + output_tokens * uint(3)"},
				},
			},
		},
	}

	rc, err := NewRuntimeConfig(t.Context(), config, nil)
	require.NoError(t, err)
	require.NotNil(t, rc)

	// Verify global costs are still compiled.
	require.Len(t, rc.RequestCosts, 1)
	require.Equal(t, LLMRequestCostTypeOutputToken, rc.RequestCosts[0].Type)
	require.Equal(t, "global_key", rc.RequestCosts[0].MetadataKey)

	// Verify free-backend has its own costs compiled.
	freeBackend, ok := rc.Backends["free-backend"]
	require.True(t, ok, "free-backend should exist")
	require.Len(t, freeBackend.RequestCosts, 1)
	require.Equal(t, LLMRequestCostTypeCEL, freeBackend.RequestCosts[0].Type)
	require.Equal(t, "billing", freeBackend.RequestCosts[0].MetadataKey)
	require.NotNil(t, freeBackend.RequestCosts[0].CELProg)

	// Evaluate the CEL program for free-backend (should always return 0).
	val, err := llmcostcel.EvaluateProgram(freeBackend.RequestCosts[0].CELProg, "", "", 100, 0, 0, 50, 150)
	require.NoError(t, err)
	require.Equal(t, uint64(0), val, "free-backend CEL should return 0")

	// Verify paid-backend has its own costs compiled.
	paidBackend, ok := rc.Backends["paid-backend"]
	require.True(t, ok, "paid-backend should exist")
	require.Len(t, paidBackend.RequestCosts, 1)
	require.Equal(t, LLMRequestCostTypeCEL, paidBackend.RequestCosts[0].Type)
	require.Equal(t, "billing", paidBackend.RequestCosts[0].MetadataKey)
	require.NotNil(t, paidBackend.RequestCosts[0].CELProg)

	// Evaluate the CEL program for paid-backend.
	// input_tokens * uint(2) + output_tokens * uint(3) = 100 * 2 + 50 * 3 = 200 + 150 = 350
	val, err = llmcostcel.EvaluateProgram(paidBackend.RequestCosts[0].CELProg, "", "", 100, 0, 0, 50, 150)
	require.NoError(t, err)
	require.Equal(t, uint64(350), val, "paid-backend CEL should calculate correctly")
}

// TestNewRuntimeConfig_BackendLevelInvalidCEL tests that invalid CEL in backend-level LLMRequestCosts
// returns an error during runtime config creation.
func TestNewRuntimeConfig_BackendLevelInvalidCEL(t *testing.T) {
	config := &Config{
		Backends: []Backend{
			{
				Name:   "backend-with-invalid-cel",
				Schema: VersionedAPISchema{Name: APISchemaOpenAI},
				LLMRequestCosts: []LLMRequestCost{
					// Invalid CEL expression - syntax error
					{MetadataKey: "cost", Type: LLMRequestCostTypeCEL, CEL: "invalid syntax ((("},
				},
			},
		},
	}

	_, err := NewRuntimeConfig(t.Context(), config, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot create CEL program for backend")
}
