// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// ErrCredentialMissing is returned when the per-request credential source is configured,
// the source is absent, and FallbackToConfigured is false.
// ProcessRequestHeaders converts this to a 401 ImmediateResponse.
var ErrCredentialMissing = errors.New("per-request credential missing")

// envoyMetadataKey is the context key used to thread Envoy's MetadataContext into handler Do() calls.
type envoyMetadataKey struct{}

// WithEnvoyMetadata stores the Envoy dynamic metadata context so the credential override
// handler can read it without changing the BackendAuthHandler interface.
// When md is nil (Envoy sent no metadata), ctx is returned unchanged so that
// envoyMetadataFromContext still returns nil — the handler then follows its fallback path.
func WithEnvoyMetadata(ctx context.Context, md *corev3.Metadata) context.Context {
	if md == nil {
		return ctx
	}
	return context.WithValue(ctx, envoyMetadataKey{}, md)
}

// envoyMetadataFromContext retrieves the Envoy metadata stored by WithEnvoyMetadata.
func envoyMetadataFromContext(ctx context.Context) *corev3.Metadata {
	md, _ := ctx.Value(envoyMetadataKey{}).(*corev3.Metadata)
	return md
}

// applyCredentialFn sets the appropriate output header(s) for a given auth type
// using the supplied per-request credential string.
type applyCredentialFn func(requestHeaders map[string]string, credential string) ([]internalapi.Header, error)

// applyBearerCredential sets Authorization: Bearer <credential>.
func applyBearerCredential(requestHeaders map[string]string, credential string) ([]internalapi.Header, error) {
	v := fmt.Sprintf("Bearer %s", credential)
	requestHeaders["Authorization"] = v
	return []internalapi.Header{{"Authorization", v}}, nil
}

// applyAnthropicCredential sets x-api-key: <credential>.
func applyAnthropicCredential(requestHeaders map[string]string, credential string) ([]internalapi.Header, error) {
	requestHeaders["x-api-key"] = credential
	return []internalapi.Header{{"x-api-key", credential}}, nil
}

// applyGoogleAICredential sets x-goog-api-key: <credential>.
func applyGoogleAICredential(requestHeaders map[string]string, credential string) ([]internalapi.Header, error) {
	requestHeaders["x-goog-api-key"] = credential
	return []internalapi.Header{{"x-goog-api-key", credential}}, nil
}

// applyAzureAPIKeyCredential sets api-key: <credential>.
func applyAzureAPIKeyCredential(requestHeaders map[string]string, credential string) ([]internalapi.Header, error) {
	requestHeaders["api-key"] = credential
	return []internalapi.Header{{"api-key", credential}}, nil
}

// makeGCPApplyFn returns an applyCredentialFn for GCP that also rewrites the :path header
// with the region and project prefix, matching the behaviour of gcpHandler.Do().
// Note: like gcpHandler.Do, the resulting path has a double slash when the original :path
// starts with "/" (e.g. "/v1/projects/.../region//models/..."). This is intentional parity.
func makeGCPApplyFn(region, projectName string) applyCredentialFn {
	return func(requestHeaders map[string]string, credential string) ([]internalapi.Header, error) {
		path := requestHeaders[":path"]
		if path == "" {
			return nil, fmt.Errorf("missing ':path' header in the request")
		}
		prefixPath := fmt.Sprintf("/v1/projects/%s/locations/%s", projectName, region)
		newPath := fmt.Sprintf("%s/%s", prefixPath, path)
		requestHeaders[":path"] = newPath
		v := fmt.Sprintf("Bearer %s", credential)
		requestHeaders["Authorization"] = v
		return []internalapi.Header{{":path", newPath}, {"Authorization", v}}, nil
	}
}

// credentialOverrideHandler wraps an inner BackendAuthHandler and sources the upstream
// credential per-request when BackendAuth.CredentialOverride is configured.
// When the per-request source is absent and FallbackToConfigured is true, it delegates
// to the inner handler. When FallbackToConfigured is false, it returns ErrCredentialMissing.
type credentialOverrideHandler struct {
	inner   filterapi.BackendAuthHandler
	config  *filterapi.CredentialOverride
	applyFn applyCredentialFn
}

// Do implements filterapi.BackendAuthHandler.
func (h *credentialOverrideHandler) Do(ctx context.Context, requestHeaders map[string]string, mutatedBody []byte) ([]internalapi.Header, error) {
	credential := h.resolveCredential(ctx, requestHeaders)
	if credential == "" {
		if !h.config.FallbackToConfigured {
			return nil, ErrCredentialMissing
		}
		return h.inner.Do(ctx, requestHeaders, mutatedBody)
	}
	return h.applyFn(requestHeaders, credential)
}

// resolveCredential reads the per-request credential from the configured source.
// Returns an empty string when the source is absent.
func (h *credentialOverrideHandler) resolveCredential(ctx context.Context, requestHeaders map[string]string) string {
	o := h.config
	if o.HeaderName != "" {
		return strings.TrimSpace(requestHeaders[o.HeaderName])
	}
	if o.DynamicMetadataNamespace != "" {
		md := envoyMetadataFromContext(ctx)
		if md == nil {
			return ""
		}
		ns, ok := md.GetFilterMetadata()[o.DynamicMetadataNamespace]
		if !ok || ns == nil {
			return ""
		}
		val, ok := ns.GetFields()[o.DynamicMetadataKey]
		if !ok || val == nil {
			return ""
		}
		return strings.TrimSpace(val.GetStringValue())
	}
	return ""
}
