// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

const azureScopeURL = "https://cognitiveservices.azure.com/.default"

type azureHandler struct {
	// For controller-managed token rotation (client secret, OIDC)
	azureAccessToken string
	// For extproc-managed workload identity
	useManagedIdentity bool
	credential         azcore.TokenCredential
	tokenOptions       policy.TokenRequestOptions
	cachedToken        string
	tokenExpiry        time.Time
	mu                 sync.RWMutex
}

func newAzureHandler(auth *filterapi.AzureAuth) (filterapi.BackendAuthHandler, error) {
	if auth.UseManagedIdentity {
		// Extproc-managed workload identity: obtain tokens dynamically
		credential, err := createAzureCredential(auth.ClientID, auth.TenantID)
		if err != nil {
			return nil, fmt.Errorf("failed to create Azure credential: %w", err)
		}
		return &azureHandler{
			useManagedIdentity: true,
			credential:         credential,
			tokenOptions:       policy.TokenRequestOptions{Scopes: []string{azureScopeURL}},
		}, nil
	}
	// Controller-managed token rotation: use pre-obtained token
	return &azureHandler{
		useManagedIdentity: false,
		azureAccessToken:   strings.TrimSpace(auth.AccessToken),
	}, nil
}

// createAzureCredential creates an Azure credential based on the environment and configuration.
// Supports AKS Workload Identity, user-assigned managed identity, and system-assigned managed identity.
func createAzureCredential(clientID, tenantID string) (azcore.TokenCredential, error) {
	clientOptions := getDefaultAzureCredentialOptions()

	// Check if running in AKS Workload Identity environment
	federatedTokenFile := os.Getenv("AZURE_FEDERATED_TOKEN_FILE")
	envTenantID := os.Getenv("AZURE_TENANT_ID")
	envClientID := os.Getenv("AZURE_CLIENT_ID")

	if federatedTokenFile != "" && (tenantID != "" || envTenantID != "") {
		// Use Workload Identity - this is the AKS Workload Identity pattern
		if tenantID == "" {
			tenantID = envTenantID
		}
		if clientID == "" {
			clientID = envClientID
		}
		workloadIDOptions := &azidentity.WorkloadIdentityCredentialOptions{
			ClientID:      clientID,
			TenantID:      tenantID,
			TokenFilePath: federatedTokenFile,
		}
		if clientOptions != nil {
			workloadIDOptions.ClientOptions = clientOptions.ClientOptions
		}
		return azidentity.NewWorkloadIdentityCredential(workloadIDOptions)
	} else if clientID != "" {
		// User-assigned managed identity - specify the client ID.
		// This uses Azure VM/VMSS Managed Identity via IMDS
		managedIDOptions := &azidentity.ManagedIdentityCredentialOptions{
			ID: azidentity.ClientID(clientID),
		}
		if clientOptions != nil {
			managedIDOptions.ClientOptions = clientOptions.ClientOptions
		}
		return azidentity.NewManagedIdentityCredential(managedIDOptions)
	}
	// Use DefaultAzureCredential which will try multiple credential types,
	// including system-assigned managed identity
	return azidentity.NewDefaultAzureCredential(clientOptions)
}

// getDefaultAzureCredentialOptions returns the client options for Azure credentials,
// including proxy configuration if set via environment variable.
func getDefaultAzureCredentialOptions() *azidentity.DefaultAzureCredentialOptions {
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

// Do implements [Handler.Do].
//
// For controller-managed tokens: Uses the pre-obtained access token.
// For extproc-managed workload identity: Obtains tokens dynamically using Azure SDK.
func (a *azureHandler) Do(ctx context.Context, requestHeaders map[string]string, _ []byte) ([]internalapi.Header, error) {
	var token string
	var err error

	if a.useManagedIdentity {
		// Get token dynamically using workload identity
		token, err = a.getToken(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get Azure token: %w", err)
		}
	} else {
		// Use pre-obtained token from controller
		token = a.azureAccessToken
	}

	authorizationValue := fmt.Sprintf("Bearer %s", token)
	requestHeaders["Authorization"] = authorizationValue
	return []internalapi.Header{{"Authorization", authorizationValue}}, nil
}

// getToken retrieves an Azure access token, using cached token if still valid.
func (a *azureHandler) getToken(ctx context.Context) (string, error) {
	// Check if cached token is still valid (with 5-minute buffer)
	a.mu.RLock()
	if a.cachedToken != "" && time.Now().Add(5*time.Minute).Before(a.tokenExpiry) {
		token := a.cachedToken
		a.mu.RUnlock()
		return token, nil
	}
	a.mu.RUnlock()

	// Need to get a new token
	a.mu.Lock()
	defer a.mu.Unlock()

	// Double-check after acquiring write lock
	if a.cachedToken != "" && time.Now().Add(5*time.Minute).Before(a.tokenExpiry) {
		return a.cachedToken, nil
	}

	// Get new token from Azure
	azureToken, err := a.credential.GetToken(ctx, a.tokenOptions)
	if err != nil {
		return "", err
	}

	// Cache the new token
	a.cachedToken = azureToken.Token
	a.tokenExpiry = azureToken.ExpiresOn
	return azureToken.Token, nil
}
