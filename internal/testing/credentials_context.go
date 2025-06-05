// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package internaltesting

import (
	"cmp"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// RequiredCredential is a bit flag for the required credentials.
type RequiredCredential byte

const (
	// RequiredCredentialOpenAI is the bit flag for the OpenAI API key.
	RequiredCredentialOpenAI RequiredCredential = 1 << iota
	// RequiredCredentialAWS is the bit flag for the AWS credentials.
	RequiredCredentialAWS
	// RequiredCredentialAzure is the bit flag for the Azure access token.
	RequiredCredentialAzure
)

// CredentialsContext holds the context for the credentials used in the tests.
type CredentialsContext struct {
	// OpenAIValid, AWSValid, AzureValid are true if the credentials are set and ready to use the real services.
	OpenAIValid, AWSValid, AzureValid bool
	// OpenAIAPIKey is the OpenAI API key. This defaults to "dummy-openai-api-key" if not set.
	OpenAIAPIKey string
	// OpenAIAPIKeyFilePath is the path to the temporary file containing the OpenAIAPIKey.
	OpenAIAPIKeyFilePath string
	// AWSFileLiteral contains the AWS credentials in the format of a file literal.
	AWSFileLiteral string
	// AWSFilePath is the path to the temporary file containing the AWS credentials (or dummy credentials).
	AWSFilePath string
	// AzureAccessToken is the Azure access token. This defaults to "dummy-azure-access-token" if not set.
	AzureAccessToken string
	// AzureAccessTokenFilePath is the path to the temporary file containing the Azure access token (or dummy token).
	AzureAccessTokenFilePath string
}

// MaybeSkip skips the test if the required credentials are not set.
func (c CredentialsContext) MaybeSkip(t *testing.T, required RequiredCredential) {
	if required&RequiredCredentialOpenAI != 0 && !c.OpenAIValid {
		t.Skip("skipping test as OpenAI API key is not set in TEST_OPENAI_API_KEY")
	}
	if required&RequiredCredentialAWS != 0 && !c.AWSValid {
		t.Skip("skipping test as AWS credentials are not set in TEST_AWS_ACCESS_KEY_ID and TEST_AWS_SECRET_ACCESS_KEY")
	}
	if required&RequiredCredentialAzure != 0 && !c.AzureValid {
		t.Skip("skipping test as Azure credentials are not set in TEST_AZURE_ACCESS_TOKEN")
	}
}

// RequireNewCredentialsContext creates a new credential context for the tests from the environment variables.
func RequireNewCredentialsContext(t *testing.T) (ctx CredentialsContext) {
	// Set up credential file for OpenAI.
	openAIAPIKeyEnv := os.Getenv("TEST_OPENAI_API_KEY")
	openAIAPIKeyVal := cmp.Or(openAIAPIKeyEnv, "dummy-openai-api-key")

	openAIAPIKeyFilePath := t.TempDir() + "/open-ai-api-key"
	openaiFile, err := os.Create(openAIAPIKeyFilePath)
	require.NoError(t, err)
	_, err = openaiFile.WriteString(openAIAPIKeyVal)
	require.NoError(t, err)

	// Set up credential file for Azure.
	azureAccessTokenEnv := os.Getenv("TEST_AZURE_ACCESS_TOKEN")
	azureAccessToken := cmp.Or(azureAccessTokenEnv, "dummy-azure-access-token")
	azureAccessTokenFilePath := t.TempDir() + "/azureAccessToken"
	azureFile, err := os.Create(azureAccessTokenFilePath)
	require.NoError(t, err)
	_, err = azureFile.WriteString(azureAccessToken)
	require.NoError(t, err)

	// Set up credential file for AWS.
	awsAccessKeyID := os.Getenv("TEST_AWS_ACCESS_KEY_ID")
	awsSecretAccessKey := os.Getenv("TEST_AWS_SECRET_ACCESS_KEY")
	awsSessionToken := os.Getenv("TEST_AWS_SESSION_TOKEN")
	var awsCredentialsBody string
	if awsSessionToken != "" {
		awsCredentialsBody = fmt.Sprintf("[default]\nAWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\nAWS_SESSION_TOKEN=%s\n",
			cmp.Or(awsAccessKeyID, "dummy_access_key_id"), cmp.Or(awsSecretAccessKey, "dummy_secret_access_key"), awsSessionToken)
	} else {
		awsCredentialsBody = fmt.Sprintf("[default]\nAWS_ACCESS_KEY_ID=%s\nAWS_SECRET_ACCESS_KEY=%s\n",
			cmp.Or(awsAccessKeyID, "dummy_access_key_id"), cmp.Or(awsSecretAccessKey, "dummy_secret_access_key"))
	}
	awsFilePath := t.TempDir() + "/aws-credential-file"
	awsFile, err := os.Create(awsFilePath)
	require.NoError(t, err)
	defer func() { require.NoError(t, awsFile.Close()) }()
	_, err = awsFile.WriteString(awsCredentialsBody)
	require.NoError(t, err)

	return CredentialsContext{
		OpenAIValid:              openAIAPIKeyEnv != "",
		AWSValid:                 awsAccessKeyID != "" && awsSecretAccessKey != "",
		AzureValid:               azureAccessTokenEnv != "",
		OpenAIAPIKey:             openAIAPIKeyVal,
		OpenAIAPIKeyFilePath:     openAIAPIKeyFilePath,
		AWSFileLiteral:           awsCredentialsBody,
		AWSFilePath:              awsFilePath,
		AzureAccessToken:         azureAccessToken,
		AzureAccessTokenFilePath: azureAccessTokenFilePath,
	}
}
