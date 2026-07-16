// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"context"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

func TestNewAzureHandler(t *testing.T) {
	auth := filterapi.AzureAuth{AccessToken: " some-access-token \n"}
	handler, err := newAzureHandler(&auth)
	require.NoError(t, err)
	require.NotNil(t, handler)

	require.Equal(t, "some-access-token", handler.(*azureHandler).azureAccessToken)
}

func TestNewAzureHandler_Do(t *testing.T) {
	auth := filterapi.AzureAuth{AccessToken: "some-access-token"}
	handler, err := newAzureHandler(&auth)
	require.NoError(t, err)
	require.NotNil(t, handler)

	requestHeaders := map[string]string{":method": "POST", ":path": "/model/some-random-model/chat/completion"}
	headers, err := handler.Do(t.Context(), requestHeaders, []byte(`{"messages": [{"role": "user", "content": [{"text": "Say this is a test!"}]}]}`))
	require.NoError(t, err)

	bearerToken, ok := requestHeaders["Authorization"]
	require.True(t, ok)
	require.Equal(t, "Bearer some-access-token", bearerToken)

	require.Len(t, headers, 1)
	require.Equal(t, "Authorization", headers[0][0])
	require.Equal(t, "Bearer some-access-token", headers[0][1])
}

// fakeTokenCredential is a stub azcore.TokenCredential used to exercise the ambient
// Azure Workload Identity path without contacting Entra.
type fakeTokenCredential struct {
	token string
	err   error
}

func (f *fakeTokenCredential) GetToken(_ context.Context, _ policy.TokenRequestOptions) (azcore.AccessToken, error) {
	if f.err != nil {
		return azcore.AccessToken{}, f.err
	}
	return azcore.AccessToken{Token: f.token}, nil
}

func TestAzureHandler_Do_WorkloadIdentity(t *testing.T) {
	handler := newAzureHandlerWithCredential(&fakeTokenCredential{token: "ambient-wi-token"})

	requestHeaders := map[string]string{":method": "POST", ":path": "/model/some-random-model/chat/completion"}
	headers, err := handler.Do(t.Context(), requestHeaders, nil)
	require.NoError(t, err)

	require.Equal(t, "Bearer ambient-wi-token", requestHeaders["Authorization"])
	require.Len(t, headers, 1)
	require.Equal(t, "Authorization", headers[0][0])
	require.Equal(t, "Bearer ambient-wi-token", headers[0][1])
}
