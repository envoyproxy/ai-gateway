// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package awsauth

import (
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/require"
)

const testCredentialsFile = "[default]\naws_access_key_id=AKIAIOSFODNN7EXAMPLE\naws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n" // #nosec G101

func TestNewSigner_CredentialsFile(t *testing.T) {
	signer, err := NewSigner(t.Context(), Config{
		Region:                "us-east-1",
		CredentialFileLiteral: testCredentialsFile,
	})
	require.NoError(t, err)
	require.NotNil(t, signer)

	creds, err := signer.Retrieve(t.Context())
	require.NoError(t, err)
	require.Equal(t, "AKIAIOSFODNN7EXAMPLE", creds.AccessKeyID)
}

func TestNewSigner_DefaultChainEnv(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "env-key-id")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "env-secret")

	signer, err := NewSigner(t.Context(), Config{Region: "us-west-2"})
	require.NoError(t, err)

	creds, err := signer.Retrieve(t.Context())
	require.NoError(t, err)
	require.Equal(t, "env-key-id", creds.AccessKeyID)
	require.Equal(t, "env-secret", creds.SecretAccessKey)
}

func TestSigner_Sign(t *testing.T) {
	signer, err := NewSigner(t.Context(), Config{
		Region:                "us-east-1",
		CredentialFileLiteral: testCredentialsFile,
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, "https://bedrock-agentcore.us-east-1.amazonaws.com/mcp", nil)
	require.NoError(t, err)

	require.NoError(t, signer.Sign(t.Context(), req, []byte(`{"jsnrpc":"2.0"}`), "bedrock-agentcore", "us-east-1", time.Now()))

	auth := req.Header.Get("Authorization")
	require.NotEmpty(t, auth)
	require.Contains(t, auth, "AWS4-HMAC-SHA256")
	require.Contains(t, auth, "Credential=AKIAIOSFODNN7EXAMPLE")
	// The service and region must be embedded in the credential scope.
	require.Contains(t, auth, "/us-east-1/bedrock-agentcore/aws4_request")
	require.NotEmpty(t, req.Header.Get("X-Amz-Date"))
}

func TestSigner_SignSessionToken(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "ASIATESTACCESSKEY")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
	t.Setenv("AWS_SESSION_TOKEN", "temporary-session-token-xyz")

	signer, err := NewSigner(t.Context(), Config{Region: "eu-central-1"})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, "https://example.eu-central-1.amazonaws.com/mcp", nil)
	require.NoError(t, err)
	require.NoError(t, signer.Sign(t.Context(), req, nil, "execute-api", "eu-central-1", time.Now()))

	require.Equal(t, "temporary-session-token-xyz", req.Header.Get("X-Amz-Security-Token"))
}

func TestSigner_Sign_PathAndQueryAreSigned(t *testing.T) {
	signer, err := NewSigner(t.Context(), Config{
		Region:                "us-east-1",
		CredentialFileLiteral: testCredentialsFile,
	})
	require.NoError(t, err)

	// A fixed signing time makes SigV4 deterministic, so signatures are comparable and any
	// difference is attributable to the request, not the timestamp.
	signingTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	body := []byte(`{"jsonrpc":"2.0"}`)
	sign := func(url string) string {
		req, reqErr := http.NewRequest(http.MethodPost, url, nil)
		require.NoError(t, reqErr)
		require.NoError(t, signer.Sign(t.Context(), req, body, "bedrock-agentcore", "us-east-1", signingTime))
		return req.Header.Get("Authorization")
	}

	base := "https://bedrock-agentcore.us-east-1.amazonaws.com/runtimes/arn/invocations"

	// Control: identical inputs yield identical signatures.
	sig1 := sign(base)
	sig2 := sign(base)
	require.Equal(t, sig1, sig2)

	// The query string is part of the canonical request, so adding one (e.g. AgentCore's
	// "?qualifier=DEFAULT") must change the signature. This guards the path+query signing
	// relied on by Bedrock AgentCore invoke URLs.
	require.NotEqual(t, sign(base), sign(base+"?qualifier=DEFAULT"))

	// The path itself is signed too.
	require.NotEqual(t, sign(base), sign("https://bedrock-agentcore.us-east-1.amazonaws.com/mcp"))
}

func TestNewSigner_InvalidProfile(t *testing.T) {
	_, err := NewSigner(t.Context(), Config{
		Region:                "us-east-1",
		CredentialFileLiteral: testCredentialsFile,
		Profile:               "nonexistent-profile",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, `cannot load shared config profile "nonexistent-profile"`)
}

func TestNewSigner_EmptyCredentialsProfile(t *testing.T) {
	_, err := NewSigner(t.Context(), Config{
		Region:                "us-east-1",
		CredentialFileLiteral: "[empty]\n",
		Profile:               "empty",
	})
	require.Error(t, err)
	require.ErrorContains(t, err, `does not contain any credentials`)
}

func TestSigner_SignHTTP_EmptyCredentials(t *testing.T) {
	signer := &Signer{
		credentialsProvider: credentials.StaticCredentialsProvider{Value: aws.Credentials{}},
		signer:              v4.NewSigner(),
	}

	req, err := http.NewRequest(http.MethodPost, "https://example.us-east-1.amazonaws.com/mcp", nil)
	require.NoError(t, err)

	err = signer.SignHTTP(t.Context(), req, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", "execute-api", "us-east-1", time.Now())
	require.Error(t, err)
	require.ErrorContains(t, err, "cannot retrieve AWS credentials")
}

func TestSigner_ConcurrentSign(t *testing.T) {
	signer, err := NewSigner(t.Context(), Config{
		Region:                "us-east-1",
		CredentialFileLiteral: testCredentialsFile,
	})
	require.NoError(t, err)

	var wg sync.WaitGroup
	wg.Add(50)
	for range 50 {
		go func() {
			defer wg.Done()
			req, reqErr := http.NewRequest(http.MethodPost, "https://example.us-east-1.amazonaws.com/mcp", nil)
			require.NoError(t, reqErr)
			require.NoError(t, signer.Sign(t.Context(), req, []byte(`{}`), "example", "us-east-1", time.Now()))
			require.NotEmpty(t, req.Header.Get("Authorization"))
		}()
	}
	wg.Wait()
}
