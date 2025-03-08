// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package rotators

import (
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"

	"github.com/envoyproxy/ai-gateway/constants"
)

func TestPopulateAzureAccessToken(t *testing.T) {
	secret := &corev1.Secret{}
	expiration := time.Now()

	azureToken := azcore.AccessToken{Token: "some-azure-token", ExpiresOn: expiration}
	populateAzureAccessToken(secret, &azureToken)

	annotation, ok := secret.Annotations[ExpirationTimeAnnotationKey]
	require.True(t, ok)
	require.Equal(t, expiration.Format(time.RFC3339), annotation)

	require.Len(t, secret.Data, 1)
	val, ok := secret.Data[constants.AzureAccessTokenKey]
	require.True(t, ok)
	require.NotEmpty(t, val)
}
