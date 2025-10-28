// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package tokenprovider

import (
	"context"
	"net/http"
	"net/url"
	"os"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// azureManagedIdentityTokenProvider is a provider that implements TokenProvider interface for Azure Managed Identity access tokens.
// It uses DefaultAzureCredential which supports multiple authentication methods including managed identity, workload identity, and environment variables.
type azureManagedIdentityTokenProvider struct {
	credential  azcore.TokenCredential
	tokenOption policy.TokenRequestOptions
}

// NewAzureManagedIdentityTokenProvider creates a new TokenProvider using Azure DefaultAzureCredential.
// This supports:
// - Environment variables (AZURE_CLIENT_ID, AZURE_TENANT_ID, AZURE_CLIENT_SECRET, AZURE_FEDERATED_TOKEN_FILE)
// - AKS Workload Identity (via service account annotations and federated token file)
// - System-assigned managed identity (when clientID is empty)
// - User-assigned managed identity (when clientID is provided)
// - Azure CLI credentials (for development scenarios).
func NewAzureManagedIdentityTokenProvider(_ context.Context, clientID string, tokenOption policy.TokenRequestOptions) (TokenProvider, error) {
	clientOptions := GetDefaultAzureCredentialOptions()

	var credential azcore.TokenCredential
	var err error

	if clientID != "" {
		// User-assigned managed identity - specify the client ID.
		managedIDOptions := &azidentity.ManagedIdentityCredentialOptions{
			ID: azidentity.ClientID(clientID),
		}
		if clientOptions != nil {
			managedIDOptions.ClientOptions = clientOptions.ClientOptions
		}
		credential, err = azidentity.NewManagedIdentityCredential(managedIDOptions)
	} else {
		// Use DefaultAzureCredential which will try multiple credential types.
		// Including system-assigned managed identity, workload identity, environment variables, etc.
		credential, err = azidentity.NewDefaultAzureCredential(clientOptions)
	}

	if err != nil {
		return nil, err
	}

	return &azureManagedIdentityTokenProvider{
		credential:  credential,
		tokenOption: tokenOption,
	}, nil
}

// GetToken implements TokenProvider.GetToken method to retrieve an Azure access token and its expiration time.
func (a *azureManagedIdentityTokenProvider) GetToken(ctx context.Context) (TokenExpiry, error) {
	azureToken, err := a.credential.GetToken(ctx, a.tokenOption)
	if err != nil {
		return TokenExpiry{}, err
	}
	return TokenExpiry{Token: azureToken.Token, ExpiresAt: azureToken.ExpiresOn}, nil
}

// GetDefaultAzureCredentialOptions returns the client options for DefaultAzureCredential,
// including proxy configuration if set via environment variable.
func GetDefaultAzureCredentialOptions() *azidentity.DefaultAzureCredentialOptions {
	if azureProxyURL := os.Getenv("AI_GATEWAY_AZURE_PROXY_URL"); azureProxyURL != "" {
		proxyURL, err := url.Parse(azureProxyURL)
		if err == nil {
			customTransport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
			customHTTPClient := &http.Client{Transport: customTransport}
			return &azidentity.DefaultAzureCredentialOptions{
				ClientOptions: azcore.ClientOptions{
					Transport: customHTTPClient,
				},
			}
		}
	}
	return nil
}
