package backendauth

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestNewHandler(t *testing.T) {
	awsFile := t.TempDir() + "/awstest"
	err := os.WriteFile(awsFile, []byte(`
[default]
aws_access_key_id = test
aws_secret_access_key = test
`), 0o600)
	require.NoError(t, err)

	apiKeyFile := t.TempDir() + "/apikey"
	err = os.WriteFile(apiKeyFile, []byte("TEST"), 0o600)
	require.NoError(t, err)

	for _, tt := range []struct {
		name   string
		config *filterapi.BackendAuth
	}{
		{
			name: "AWSAuth",
			config: &filterapi.BackendAuth{AWSAuth: &filterapi.AWSAuth{
				Region: "us-west-2", CredentialFileName: awsFile,
			}},
		},
		{
			name: "APIKey",
			config: &filterapi.BackendAuth{
				APIKey: &filterapi.APIKeyAuth{Filename: apiKeyFile},
			},
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewHandler(t.Context(), tt.config)
			require.NoError(t, err)
		})
	}
}
