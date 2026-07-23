// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"errors"
	"fmt"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

// makeOverrideConfig returns a CredentialOverride using a request header source.
func makeHeaderOverride(headerName string, fallback bool) *filterapi.CredentialOverride {
	return &filterapi.CredentialOverride{
		HeaderName:           headerName,
		FallbackToConfigured: fallback,
		InputHeaderToRemove:  headerName,
	}
}

// makeMetadataOverride returns a CredentialOverride using dynamic metadata source.
func makeMetadataOverride(namespace, key string, fallback bool) *filterapi.CredentialOverride {
	return &filterapi.CredentialOverride{
		DynamicMetadataNamespace: namespace,
		DynamicMetadataKey:       key,
		FallbackToConfigured:     fallback,
	}
}

// metadataContext builds an Envoy MetadataContext with a single string value.
func metadataContext(namespace, key, value string) *corev3.Metadata {
	return &corev3.Metadata{
		FilterMetadata: map[string]*structpb.Struct{
			namespace: {
				Fields: map[string]*structpb.Value{
					key: structpb.NewStringValue(value),
				},
			},
		},
	}
}

func TestCredentialOverrideHandler_FromRequestHeaders(t *testing.T) {
	t.Run("header present uses override credential", func(t *testing.T) {
		inner, err := newAPIKeyHandler(&filterapi.APIKeyAuth{Key: "static-key"})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeHeaderOverride("x-aigw-api-key", true),
			applyFn: applyBearerCredential,
		}

		headers := map[string]string{"x-aigw-api-key": "per-request-key"}
		hdrs, err := h.Do(t.Context(), headers, nil)
		require.NoError(t, err)
		require.Equal(t, "Bearer per-request-key", headers["Authorization"])
		require.Len(t, hdrs, 1)
		require.Equal(t, "Authorization", hdrs[0][0])
		require.Equal(t, "Bearer per-request-key", hdrs[0][1])
	})

	t.Run("header absent fallback=true uses static credential", func(t *testing.T) {
		inner, err := newAPIKeyHandler(&filterapi.APIKeyAuth{Key: "static-key"})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeHeaderOverride("x-aigw-api-key", true),
			applyFn: applyBearerCredential,
		}

		headers := map[string]string{}
		hdrs, err := h.Do(t.Context(), headers, nil)
		require.NoError(t, err)
		require.Equal(t, "Bearer static-key", headers["Authorization"])
		require.Len(t, hdrs, 1)
	})

	t.Run("header absent fallback=false returns ErrCredentialMissing", func(t *testing.T) {
		inner, err := newAPIKeyHandler(&filterapi.APIKeyAuth{Key: "static-key"})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeHeaderOverride("x-aigw-api-key", false),
			applyFn: applyBearerCredential,
		}

		headers := map[string]string{}
		_, err = h.Do(t.Context(), headers, nil)
		require.ErrorIs(t, err, ErrCredentialMissing)
	})

	t.Run("header present strips whitespace", func(t *testing.T) {
		inner, err := newAPIKeyHandler(&filterapi.APIKeyAuth{Key: "static-key"})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeHeaderOverride("x-aigw-api-key", false),
			applyFn: applyBearerCredential,
		}

		headers := map[string]string{"x-aigw-api-key": "  my-key  "}
		_, err = h.Do(t.Context(), headers, nil)
		require.NoError(t, err)
		require.Equal(t, "Bearer my-key", headers["Authorization"])
	})
}

func TestCredentialOverrideHandler_FromDynamicMetadata(t *testing.T) {
	const ns = "envoy.filters.http.ext_authz"
	const key = "upstream_api_key"

	t.Run("metadata present uses override credential", func(t *testing.T) {
		inner, err := newAPIKeyHandler(&filterapi.APIKeyAuth{Key: "static-key"})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeMetadataOverride(ns, key, true),
			applyFn: applyBearerCredential,
		}

		ctx := WithEnvoyMetadata(t.Context(), metadataContext(ns, key, "meta-key"))
		headers := map[string]string{}
		hdrs, err := h.Do(ctx, headers, nil)
		require.NoError(t, err)
		require.Equal(t, "Bearer meta-key", headers["Authorization"])
		require.Len(t, hdrs, 1)
	})

	t.Run("metadata absent fallback=true uses static credential", func(t *testing.T) {
		inner, err := newAPIKeyHandler(&filterapi.APIKeyAuth{Key: "static-key"})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeMetadataOverride(ns, key, true),
			applyFn: applyBearerCredential,
		}

		headers := map[string]string{}
		hdrs, err := h.Do(t.Context(), headers, nil)
		require.NoError(t, err)
		require.Equal(t, "Bearer static-key", headers["Authorization"])
		require.Len(t, hdrs, 1)
	})

	t.Run("metadata absent fallback=false returns ErrCredentialMissing", func(t *testing.T) {
		inner, err := newAPIKeyHandler(&filterapi.APIKeyAuth{Key: "static-key"})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeMetadataOverride(ns, key, false),
			applyFn: applyBearerCredential,
		}

		_, err = h.Do(t.Context(), map[string]string{}, nil)
		require.ErrorIs(t, err, ErrCredentialMissing)
	})

	t.Run("no metadata context in ctx returns empty", func(t *testing.T) {
		inner, err := newAPIKeyHandler(&filterapi.APIKeyAuth{Key: "static-key"})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeMetadataOverride(ns, key, false),
			applyFn: applyBearerCredential,
		}

		// Context has no Envoy metadata — no WithEnvoyMetadata call.
		_, err = h.Do(t.Context(), map[string]string{}, nil)
		require.ErrorIs(t, err, ErrCredentialMissing)
	})

	t.Run("nil metadata context is safe", func(t *testing.T) {
		inner, err := newAPIKeyHandler(&filterapi.APIKeyAuth{Key: "static-key"})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeMetadataOverride(ns, key, true),
			applyFn: applyBearerCredential,
		}

		ctx := WithEnvoyMetadata(t.Context(), nil)
		hdrs, err := h.Do(ctx, map[string]string{}, nil)
		require.NoError(t, err)
		require.Equal(t, "Bearer static-key", hdrs[0][1])
	})
}

func TestCredentialOverrideHandler_PerAuthType(t *testing.T) {
	t.Run("anthropic sets x-api-key", func(t *testing.T) {
		inner, err := newAnthropicAPIKeyHandler(&filterapi.AnthropicAPIKeyAuth{Key: "static"})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeHeaderOverride("x-aigw-anthropic-api-key", false),
			applyFn: applyAnthropicCredential,
		}

		headers := map[string]string{"x-aigw-anthropic-api-key": "per-req"}
		_, err = h.Do(t.Context(), headers, nil)
		require.NoError(t, err)
		require.Equal(t, "per-req", headers["x-api-key"])
	})

	t.Run("google ai sets x-goog-api-key", func(t *testing.T) {
		inner, err := newGoogleAIKeyHandler(&filterapi.GoogleAIKeyAuth{Key: "static"})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeHeaderOverride("x-aigw-goog-api-key", false),
			applyFn: applyGoogleAICredential,
		}

		headers := map[string]string{"x-aigw-goog-api-key": "per-req"}
		_, err = h.Do(t.Context(), headers, nil)
		require.NoError(t, err)
		require.Equal(t, "per-req", headers["x-goog-api-key"])
	})

	t.Run("azure api key sets api-key", func(t *testing.T) {
		inner, err := newAzureAPIKeyHandler(&filterapi.AzureAPIKeyAuth{Key: "static"})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeHeaderOverride("x-aigw-azure-api-key", false),
			applyFn: applyAzureAPIKeyCredential,
		}

		headers := map[string]string{"x-aigw-azure-api-key": "per-req"}
		_, err = h.Do(t.Context(), headers, nil)
		require.NoError(t, err)
		require.Equal(t, "per-req", headers["api-key"])
	})

	t.Run("azure credentials sets Authorization Bearer", func(t *testing.T) {
		inner, err := newAzureHandler(&filterapi.AzureAuth{AccessToken: "static"})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeHeaderOverride("x-aigw-azure-access-token", false),
			applyFn: applyBearerCredential,
		}

		headers := map[string]string{"x-aigw-azure-access-token": "per-req-token"}
		_, err = h.Do(t.Context(), headers, nil)
		require.NoError(t, err)
		require.Equal(t, "Bearer per-req-token", headers["Authorization"])
	})

	t.Run("gcp credentials sets Authorization Bearer and rewrites path", func(t *testing.T) {
		inner, err := newGCPHandler(t.Context(), &filterapi.GCPAuth{
			AccessToken: "static-token",
			Region:      "us-central1",
			ProjectName: "my-project",
		})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeHeaderOverride("x-aigw-gcp-access-token", false),
			applyFn: makeGCPApplyFn("us-central1", "my-project"),
		}

		headers := map[string]string{
			"x-aigw-gcp-access-token": "per-req-gcp-token",
			":path":                   "/models/gemini/generate",
		}
		hdrs, err := h.Do(t.Context(), headers, nil)
		require.NoError(t, err)
		require.Equal(t, "Bearer per-req-gcp-token", headers["Authorization"])
		// Double slash matches gcpHandler.Do() behaviour when :path starts with "/".
		require.Equal(t, "/v1/projects/my-project/locations/us-central1//models/gemini/generate", headers[":path"])
		require.Len(t, hdrs, 2)
	})

	t.Run("gcp missing :path returns error", func(t *testing.T) {
		inner, err := newGCPHandler(t.Context(), &filterapi.GCPAuth{
			AccessToken: "static-token",
			Region:      "us-central1",
			ProjectName: "my-project",
		})
		require.NoError(t, err)

		h := &credentialOverrideHandler{
			inner:   inner,
			config:  makeHeaderOverride("x-aigw-gcp-access-token", false),
			applyFn: makeGCPApplyFn("us-central1", "my-project"),
		}

		headers := map[string]string{"x-aigw-gcp-access-token": "per-req-gcp-token"}
		_, err = h.Do(t.Context(), headers, nil)
		require.Error(t, err)
		require.Contains(t, err.Error(), ":path")
	})
}

func TestNewHandler_WrapsWithOverride(t *testing.T) {
	t.Run("nil CredentialOverride returns plain handler", func(t *testing.T) {
		h, err := NewHandler(t.Context(), &filterapi.BackendAuth{
			APIKey: &filterapi.APIKeyAuth{Key: "k"},
		})
		require.NoError(t, err)
		_, ok := h.(*credentialOverrideHandler)
		require.False(t, ok, "no override configured — should not be wrapped")
	})

	t.Run("CredentialOverride wraps handler", func(t *testing.T) {
		h, err := NewHandler(t.Context(), &filterapi.BackendAuth{
			APIKey: &filterapi.APIKeyAuth{Key: "k"},
			CredentialOverride: &filterapi.CredentialOverride{
				HeaderName:           "x-aigw-api-key",
				FallbackToConfigured: true,
			},
		})
		require.NoError(t, err)
		_, ok := h.(*credentialOverrideHandler)
		require.True(t, ok, "override configured — handler should be wrapped")
	})
}

func TestErrCredentialMissing_IsSentinel(t *testing.T) {
	// errors.New does not wrap, so the sentinel is not detected.
	unrelated := errors.New("wrapped: " + ErrCredentialMissing.Error())
	require.NotErrorIs(t, unrelated, ErrCredentialMissing)

	// fmt.Errorf with %w wraps, so the sentinel IS detected.
	wrapped := fmt.Errorf("outer: %w", ErrCredentialMissing)
	require.ErrorIs(t, wrapped, ErrCredentialMissing)
}
