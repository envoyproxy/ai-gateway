// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestNewHandler(t *testing.T) {
	for _, tt := range []struct {
		name   string
		config *filterapi.BackendAuth
	}{
		{
			name: "AWSAuth",
			config: &filterapi.BackendAuth{AWSAuth: &filterapi.AWSAuth{
				Region: "us-west-2", CredentialFileLiteral: `
[default]
aws_access_key_id = test
aws_secret_access_key = test
`,
			}},
		},
		{
			name: "APIKey",
			config: &filterapi.BackendAuth{
				APIKey: &filterapi.APIKeyAuth{Key: "TEST"},
			},
		},
		{
			name: "AzureAuth",
			config: &filterapi.BackendAuth{
				AzureAuth: &filterapi.AzureAuth{AccessToken: "some-access-token"},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewHandler(t.Context(), tt.config)
			require.NoError(t, err)
		})
	}
}
