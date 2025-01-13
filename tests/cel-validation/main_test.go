//go:build test_cel_validation

package celvalidation

import (
	"context"
	"embed"
	"path"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/util/yaml"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/tests"
)

//go:embed testdata
var testdata embed.FS

func TestLLMRoutes(t *testing.T) {
	c, _, _ := tests.NewEnvTest(t)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(30*time.Second))
	defer cancel()

	for _, tc := range []struct {
		name   string
		expErr string
	}{
		{name: "basic.yaml"},
		{
			name:   "non_openai_schema.yaml",
			expErr: `spec.inputSchema: Invalid value: "object": failed rule: self.schema == 'OpenAI'`,
		},
		{
			name:   "unknown_schema.yaml",
			expErr: "spec.inputSchema.schema: Unsupported value: \"SomeRandomVendor\": supported values: \"OpenAI\", \"AWSBedrock\"",
		},
		{
			name:   "unsupported_match.yaml",
			expErr: "spec.rules[0].matches[0].headers: Invalid value: \"array\": currently only exact match is supported",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := testdata.ReadFile(path.Join("testdata/llmroutes", tc.name))
			require.NoError(t, err)

			llmRoute := &aigv1a1.LLMRoute{}
			err = yaml.UnmarshalStrict(data, llmRoute)
			require.NoError(t, err)

			if tc.expErr != "" {
				require.ErrorContains(t, c.Create(ctx, llmRoute), tc.expErr)
			} else {
				require.NoError(t, c.Create(ctx, llmRoute))
				require.NoError(t, c.Delete(ctx, llmRoute))
			}
		})
	}
}

func TestLLMBackends(t *testing.T) {
	c, _, _ := tests.NewEnvTest(t)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(30*time.Second))
	defer cancel()

	for _, tc := range []struct {
		name   string
		expErr string
	}{
		{name: "basic.yaml"},
		{name: "basic-eg-backend.yaml"},
		{
			name:   "unknown_schema.yaml",
			expErr: "spec.outputSchema.schema: Unsupported value: \"SomeRandomVendor\": supported values: \"OpenAI\", \"AWSBedrock\"",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data, err := testdata.ReadFile(path.Join("testdata/llmbackends", tc.name))
			require.NoError(t, err)

			llmBackend := &aigv1a1.LLMBackend{}
			err = yaml.UnmarshalStrict(data, llmBackend)
			require.NoError(t, err)

			if tc.expErr != "" {
				require.ErrorContains(t, c.Create(ctx, llmBackend), tc.expErr)
			} else {
				require.NoError(t, c.Create(ctx, llmBackend))
				require.NoError(t, c.Delete(ctx, llmBackend))
			}
		})
	}
}

func TestBackendSecurityPolicies(t *testing.T) {
	c, _, _ := tests.NewEnvTest(t)

	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(30*time.Second))
	defer cancel()

	for _, tc := range []struct {
		name   string
		expErr string
	}{
		{name: "basic.yaml"},
		{
			name:   "unknown_provider.yaml",
			expErr: "spec.type: Unsupported value: \"UnknownType\": supported values: \"APIKey\", \"AWSCredentials\"",
		},
		{
			name:   "missing_type.yaml",
			expErr: "spec.type: Unsupported value: \"\": supported values: \"APIKey\", \"AWSCredentials\"",
		},
		{
			name:   "multiple_security_policies.yaml",
			expErr: "Too many: 3: must have at most 2 items",
		},
		{name: "aws_credential_file.yaml"},
		{name: "aws_oidc.yaml"},
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
