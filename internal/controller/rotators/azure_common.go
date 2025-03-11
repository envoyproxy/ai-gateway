// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package rotators

import (
	corev1 "k8s.io/api/core/v1"

	"github.com/envoyproxy/ai-gateway/constants"
	"github.com/envoyproxy/ai-gateway/internal/controller/tokenprovider"
)

func populateAzureAccessToken(secret *corev1.Secret, token *tokenprovider.TokenExpiry) {
	updateExpirationSecretAnnotation(secret, token.ExpiresAt)

	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data[constants.AzureAccessTokenKey] = []byte(token.Token)
}
