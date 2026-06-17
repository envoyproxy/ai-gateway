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
				{MetadataKey: "key", RouteName: "ns/test-route", Type: LLMRequestCostTypeOutputToken},
				{MetadataKey: "cel_key", RouteName: "ns/test-route", Type: LLMRequestCostTypeCEL, CEL: "1 + 1"},
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
		val, err := llmcostcel.EvaluateProgram(prog, "", "", "", 1, 1, 1, 1, 1, 0)
		require.NoError(t, err)
		require.Equal(t, uint64(2), val)
		require.Equal(t, config.Models, rc.DeclaredModels)
	})

	t.Run("with global costs", func(t *testing.T) {
		config := &Config{
			GlobalLLMRequestCosts: []GlobalLLMRequestCost{
				{MetadataKey: "global_input", Type: LLMRequestCostTypeInputToken},
				{MetadataKey: "global_cel", Type: LLMRequestCostTypeCEL, CEL: "input_tokens + output_tokens"},
			},
			LLMRequestCosts: []LLMRequestCost{
				{MetadataKey: "route_output", RouteName: "ns/route1", Type: LLMRequestCostTypeOutputToken},
			},
		}
		rc, err := NewRuntimeConfig(t.Context(), config, func(_ context.Context, _ *BackendAuth) (BackendAuthHandler, error) {
			return nil, nil
		})
		require.NoError(t, err)

		require.Len(t, rc.GlobalRequestCosts, 2)
		require.Equal(t, LLMRequestCostTypeInputToken, rc.GlobalRequestCosts[0].Type)
		require.Equal(t, "global_input", rc.GlobalRequestCosts[0].MetadataKey)
		require.Equal(t, LLMRequestCostTypeCEL, rc.GlobalRequestCosts[1].Type)
		require.Equal(t, "input_tokens + output_tokens", rc.GlobalRequestCosts[1].CEL)
		require.NotNil(t, rc.GlobalRequestCosts[1].CELProg)

		require.Len(t, rc.RequestCosts, 1)
		require.Equal(t, "route_output", rc.RequestCosts[0].MetadataKey)
		require.Equal(t, "ns/route1", rc.RequestCosts[0].RouteName)
	})

	t.Run("error - invalid CEL in global cost", func(t *testing.T) {
		config := &Config{
			GlobalLLMRequestCosts: []GlobalLLMRequestCost{
				{MetadataKey: "bad_cel", Type: LLMRequestCostTypeCEL, CEL: "invalid syntax ++"},
			},
		}
		_, err := NewRuntimeConfig(t.Context(), config, func(_ context.Context, _ *BackendAuth) (BackendAuthHandler, error) {
			return nil, nil
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot create CEL program for global cost")
	})

	t.Run("error - invalid CEL in route cost", func(t *testing.T) {
		config := &Config{
			LLMRequestCosts: []LLMRequestCost{
				{MetadataKey: "bad_cel", RouteName: "ns/route1", Type: LLMRequestCostTypeCEL, CEL: "bad syntax @@"},
			},
		}
		_, err := NewRuntimeConfig(t.Context(), config, func(_ context.Context, _ *BackendAuth) (BackendAuthHandler, error) {
			return nil, nil
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot create CEL program for cost")
	})

	t.Run("error - route cost with empty RouteName", func(t *testing.T) {
		config := &Config{
			LLMRequestCosts: []LLMRequestCost{
				{MetadataKey: "missing_route", RouteName: "", Type: LLMRequestCostTypeInputToken},
			},
		}
		_, err := NewRuntimeConfig(t.Context(), config, func(_ context.Context, _ *BackendAuth) (BackendAuthHandler, error) {
			return nil, nil
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "must have non-empty RouteName")
		require.Contains(t, err.Error(), "missing_route")
	})

	t.Run("RateLimitsFromHeaders passed through to RuntimeConfig", func(t *testing.T) {
		config := &Config{
			GlobalRateLimitsFromHeaders: []GlobalRateLimitFromHeader{
				{MetadataKey: "llm_input_limit", Header: "x-aigw-limit-input"},
			},
			RateLimitsFromHeaders: []RateLimitFromHeader{
				{MetadataKey: "llm_output_limit", Header: "x-aigw-limit-output", RouteName: "ns/r"},
			},
		}
		rc, err := NewRuntimeConfig(t.Context(), config, func(_ context.Context, _ *BackendAuth) (BackendAuthHandler, error) {
			return nil, nil
		})
		require.NoError(t, err)
		require.Len(t, rc.GlobalRateLimitsFromHeaders, 1)
		require.Equal(t, "llm_input_limit", rc.GlobalRateLimitsFromHeaders[0].MetadataKey)
		require.Equal(t, "x-aigw-limit-input", rc.GlobalRateLimitsFromHeaders[0].Header)
		require.Len(t, rc.RateLimitsFromHeaders, 1)
		require.Equal(t, "llm_output_limit", rc.RateLimitsFromHeaders[0].MetadataKey)
		require.Equal(t, "ns/r", rc.RateLimitsFromHeaders[0].RouteName)
	})

	t.Run("error - route-scoped RateLimitFromHeader with empty RouteName", func(t *testing.T) {
		config := &Config{
			RateLimitsFromHeaders: []RateLimitFromHeader{
				{MetadataKey: "llm_limit", Header: "x-aigw-limit", RouteName: ""},
			},
		}
		_, err := NewRuntimeConfig(t.Context(), config, func(_ context.Context, _ *BackendAuth) (BackendAuthHandler, error) {
			return nil, nil
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "must have non-empty RouteName")
		require.Contains(t, err.Error(), "llm_limit")
	})

	t.Run("error - route-scoped RateLimitFromHeader with empty Header", func(t *testing.T) {
		config := &Config{
			RateLimitsFromHeaders: []RateLimitFromHeader{
				{MetadataKey: "llm_limit", Header: "", RouteName: "ns/r"},
			},
		}
		_, err := NewRuntimeConfig(t.Context(), config, func(_ context.Context, _ *BackendAuth) (BackendAuthHandler, error) {
			return nil, nil
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "must have non-empty Header")
	})

	t.Run("error - route-scoped RateLimitFromHeader with empty MetadataKey", func(t *testing.T) {
		config := &Config{
			RateLimitsFromHeaders: []RateLimitFromHeader{
				{MetadataKey: "", Header: "x-aigw-limit", RouteName: "ns/r"},
			},
		}
		_, err := NewRuntimeConfig(t.Context(), config, func(_ context.Context, _ *BackendAuth) (BackendAuthHandler, error) {
			return nil, nil
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "non-empty MetadataKey")
	})

	t.Run("error - global RateLimitFromHeader with empty Header", func(t *testing.T) {
		config := &Config{
			GlobalRateLimitsFromHeaders: []GlobalRateLimitFromHeader{
				{MetadataKey: "llm_limit", Header: ""},
			},
		}
		_, err := NewRuntimeConfig(t.Context(), config, func(_ context.Context, _ *BackendAuth) (BackendAuthHandler, error) {
			return nil, nil
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "must have non-empty Header")
		require.Contains(t, err.Error(), "llm_limit")
	})

	t.Run("error - global RateLimitFromHeader with empty MetadataKey", func(t *testing.T) {
		config := &Config{
			GlobalRateLimitsFromHeaders: []GlobalRateLimitFromHeader{
				{MetadataKey: "", Header: "x-aigw-limit"},
			},
		}
		_, err := NewRuntimeConfig(t.Context(), config, func(_ context.Context, _ *BackendAuth) (BackendAuthHandler, error) {
			return nil, nil
		})
		require.Error(t, err)
		require.Contains(t, err.Error(), "non-empty MetadataKey")
	})
}
