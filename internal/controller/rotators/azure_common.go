// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package rotators

import (
	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	corev1 "k8s.io/api/core/v1"

	"github.com/envoyproxy/ai-gateway/constants"
)

func populateAzureAccessToken(secret *corev1.Secret, azureToken *azcore.AccessToken) {
	updateExpirationSecretAnnotation(secret, azureToken.ExpiresOn)

	if secret.Data == nil {
		secret.Data = make(map[string][]byte)
	}
	secret.Data[constants.AzureAccessTokenKey] = []byte(azureToken.Token)
}
