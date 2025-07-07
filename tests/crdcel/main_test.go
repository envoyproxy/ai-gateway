// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build test_crdcel

package celvalidation

import (
	"embed"
	"path"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/yaml"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	testsinternal "github.com/envoyproxy/ai-gateway/tests/internal"
)

//go:embed testdata
var testdata embed.FS

func TestAIGatewayRoutes(t *testing.T) {
	c, _, _ := testsinternal.NewEnvTest(t)
	ctx := t.Context()

	for _, tc := range []struct {
		name   string
		expErr string
	}{
		{name: "basic.yaml"},
		{name: "llmcosts.yaml"},
		{name: "parent_refs.yaml"},
		{name: "parent_refs_default_kind.yaml"},
		{
			name:   "non_openai_schema.yaml",
			expErr: `spec.schema: Invalid value: "object": failed rule: self.name == 'OpenAI'`,
		},
		{
			name:   "unknown_schema.yaml",
			expErr: "spec.schema.name: Unsupported value: \"SomeRandomVendor\": supported values: \"OpenAI\", \"AWSBedrock\"",
		},
		{
			name:   "target_refs_with_parent_refs.yaml",
			expErr: `spec: Invalid value: "object": targetRefs is deprecated, use parentRefs only`,
		},
		{
			name:   "parent_refs_invalid_kind.yaml",
			expErr: `spec.parentRefs: Invalid value: "array": only Gateway is supported`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := testdata.ReadFile(path.Join("testdata/aigatewayroutes", tc.name))
			require.NoError(t, err)

			aiGatewayRoute := &aigv1a1.AIGatewayRoute{}
			err = yaml.UnmarshalStrict(data, aiGatewayRoute)
			require.NoError(t, err)

			if tc.expErr != "" {
				require.ErrorContains(t, c.Create(ctx, aiGatewayRoute), tc.expErr)
			} else {
				require.NoError(t, c.Create(ctx, aiGatewayRoute))
				require.NoError(t, c.Delete(ctx, aiGatewayRoute))
			}
		})
	}
}

func TestAIServiceBackends(t *testing.T) {
	c, _, _ := testsinternal.NewEnvTest(t)
	ctx := t.Context()

	for _, tc := range []struct {
		name   string
		expErr string
	}{
		{name: "basic.yaml"},
		{name: "basic-eg-backend-aws.yaml"},
		{name: "basic-eg-backend-azure.yaml"},
		{
			name:   "unknown_schema.yaml",
			expErr: "spec.schema.name: Unsupported value: \"SomeRandomVendor\": supported values: \"OpenAI\", \"AWSBedrock\"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := testdata.ReadFile(path.Join("testdata/aiservicebackends", tc.name))
			require.NoError(t, err)

			aiBackend := &aigv1a1.AIServiceBackend{}
			err = yaml.UnmarshalStrict(data, aiBackend)
			require.NoError(t, err)

			if tc.expErr != "" {
				require.ErrorContains(t, c.Create(ctx, aiBackend), tc.expErr)
			} else {
				require.NoError(t, c.Create(ctx, aiBackend))
				require.NoError(t, c.Delete(ctx, aiBackend))
			}
		})
	}
}

func TestBackendSecurityPolicies(t *testing.T) {
	c, _, _ := testsinternal.NewEnvTest(t)
	ctx := t.Context()

	for _, tc := range []struct {
		name   string
		expErr string
	}{
		{name: "basic.yaml"},
		{
			name:   "unknown_provider.yaml",
			expErr: "spec.type: Unsupported value: \"UnknownType\": supported values: \"APIKey\", \"AWSCredentials\", \"AzureCredentials\"",
		},
		{
			name:   "missing_type.yaml",
			expErr: "spec.type: Unsupported value: \"\": supported values: \"APIKey\", \"AWSCredentials\", \"AzureCredentials\"",
		},
		{
			name:   "multiple_security_policies.yaml",
			expErr: "Too many: 3: must have at most 2 items",
		},
		{
			name:   "azure_credentials_missing_client_id.yaml",
			expErr: "spec.azureCredentials.clientID in body should be at least 1 chars long",
		},
		{
			name:   "azure_credentials_missing_tenant_id.yaml",
			expErr: "spec.azureCredentials.tenantID in body should be at least 1 chars long",
		},
		{
			name:   "azure_missing_auth.yaml",
			expErr: "Exactly one of clientSecretRef or oidcExchangeToken must be specified",
		},
		{
			name:   "azure_multiple_auth.yaml",
			expErr: "Exactly one of clientSecretRef or oidcExchangeToken must be specified",
		},
		// CEL validation test cases - these should fail due to type mismatch
		{
			name:   "apikey_with_aws_credentials.yaml",
			expErr: "When type is APIKey, only apiKey field should be set",
		},
		{
			name:   "apikey_with_azure_credentials.yaml",
			expErr: "When type is APIKey, only apiKey field should be set",
		},
		{
			name:   "apikey_with_gcp_credentials.yaml",
			expErr: "When type is APIKey, only apiKey field should be set",
		},
		{
			name:   "apikey_with_nil_configuration.yaml",
			expErr: "When type is APIKey, only apiKey field should be set",
		},
		{
			name:   "aws_with_azure_credentials.yaml",
			expErr: "When type is AWSCredentials, only awsCredentials field should be set",
		},
		{
			name:   "azure_with_gcp_credentials.yaml",
			expErr: "When type is AzureCredentials, only azureCredentials field should be set",
		},
		{
			name:   "gcp_with_apikey.yaml",
			expErr: "When type is GCPCredentials, only gcpCredentials field should be set",
		},
		// Valid test cases - these should pass
		{name: "azure_oidc.yaml"},
		{name: "azure_valid_credentials.yaml"},
		{name: "aws_credential_file.yaml"},
		{name: "aws_oidc.yaml"},
		{name: "gcp_oidc.yaml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := testdata.ReadFile(path.Join("testdata/backendsecuritypolicies", tc.name))
			require.NoError(t, err)

			backendSecurityPolicy := &aigv1a1.BackendSecurityPolicy{}
			err = yaml.UnmarshalStrict(data, backendSecurityPolicy)
			require.NoError(t, err)

			if tc.expErr != "" {
				require.ErrorContains(t, c.Create(ctx, backendSecurityPolicy), tc.expErr)
			} else {
				require.NoError(t, c.Create(ctx, backendSecurityPolicy))
				require.NoError(t, c.Delete(ctx, backendSecurityPolicy))
			}
		})
	}
}
