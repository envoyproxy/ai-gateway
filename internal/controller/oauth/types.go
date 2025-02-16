// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package oauth

import (
	"context"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"golang.org/x/oauth2"
)

// TokenProvider defines the interface for OAuth token providers.
type TokenProvider interface {
	// FetchToken will obtain oauth token using oidc credentials.
	FetchToken(ctx context.Context) (*oauth2.Token, error)
	// SetOIDC will update the locally stored OIDC credentials.
	SetOIDC(oidc egv1a1.OIDC)
}
