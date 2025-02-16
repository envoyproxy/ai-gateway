// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package oauth

import (
	"context"

	"golang.org/x/oauth2"
)

// TokenProvider defines the interface for OAuth token providers.
type TokenProvider interface {
	// FetchToken will obtain oauth token using oidc credentials.
	FetchToken(ctx context.Context) (*oauth2.Token, error)
}
