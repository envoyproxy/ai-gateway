// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"fmt"
	"strings"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

type azureHandler struct {
	azureAccessToken string
}

func newAzureHandler(auth *filterapi.AzureAuth) (Handler, error) {
	return &azureHandler{azureAccessToken: strings.TrimSpace(auth.AccessToken)}, nil
}

// Do implements [Handler.Do].
//
// Extracts the azure access token from the local file and set it as an authorization header.
func (a *azureHandler) Do(_ context.Context, requestHeaders map[string]string, _ []byte) ([][2]string, error) {
	requestHeaders["Authorization"] = fmt.Sprintf("Bearer %s", a.azureAccessToken)
	return [][2]string{{"Authorization", fmt.Sprintf("Bearer %s", a.azureAccessToken)}}, nil
}
