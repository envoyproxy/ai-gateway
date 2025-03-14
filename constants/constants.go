// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package constants

import "time"

const (
	APIKey = "apiKey"
	// AzureAccessTokenKey is the key used to store Azure access token in Kubernetes secrets.
	AzureAccessTokenKey = "azureAccessToken"
	// AwsCredentialsKey is the key used to store AWS credentials in Kubernetes secrets.
	AwsCredentialsKey = "credentials"
	ClientSecretKey   = "client-secret"

	// PreRotationWindow specifies how long before expiry to rotate credentials.
	// Temporarily a fixed duration.
	PreRotationWindow = 5 * time.Minute
)
