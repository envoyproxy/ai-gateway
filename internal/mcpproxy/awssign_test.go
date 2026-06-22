// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package mcpproxy

import (
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/awsauth"
	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

const testAWSCredentialsFile = "[default]\naws_access_key_id=AKIAIOSFODNN7EXAMPLE\naws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n" // #nosec G101

func TestAWSBackendSigner_Sign(t *testing.T) {
	signer, err := newAWSBackendSigner(t.Context(), &filterapi.MCPBackendAWSAuth{
		Region:                "us-east-1",
		Service:               "bedrock-agentcore",
		Host:                  "bedrock-agentcore.us-east-1.amazonaws.com",
		Path:                  "/mcp",
		CredentialFileLiteral: testAWSCredentialsFile,
	})
	require.NoError(t, err)

	// The request goes to the local backend listener, but it should be signed as if it
	// targets the backend host/path (which Envoy rewrites it to).
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:1234", nil)
	require.NoError(t, err)

	require.NoError(t, signer.sign(t.Context(), req, []byte(`{"jsonrpc":"2.0","method":"tools/list"}`)))

	auth := req.Header.Get("Authorization")
	require.Contains(t, auth, "AWS4-HMAC-SHA256")
	require.Contains(t, auth, "Credential=AKIAIOSFODNN7EXAMPLE")
	require.Contains(t, auth, "/us-east-1/bedrock-agentcore/aws4_request")
	// Only "host" should be in the signed headers, mirroring the Bedrock signing path.
	require.Contains(t, auth, "SignedHeaders=host")
	require.NotEmpty(t, req.Header.Get("X-Amz-Date"))
	// The local listener address must remain the request target; signing only adds headers.
	require.Equal(t, "127.0.0.1:1234", req.URL.Host)
}

func TestAWSBackendSigner_Sign_PathWithQuery(t *testing.T) {
	// Bedrock AgentCore invoke URLs carry a URL-encoded ARN in the path plus a
	// "?qualifier=..." query string. Signing must handle that path+query without error
	// and still leave the request targeting the local listener.
	signer, err := newAWSBackendSigner(t.Context(), &filterapi.MCPBackendAWSAuth{
		Region:                "us-east-1",
		Service:               "bedrock-agentcore",
		Host:                  "bedrock-agentcore.us-east-1.amazonaws.com",
		Path:                  "/runtimes/arn%3Aaws%3Abedrock-agentcore%3Aus-east-1%3A123%3Aruntime%2Ffoo/invocations?qualifier=DEFAULT",
		CredentialFileLiteral: testAWSCredentialsFile,
	})
	require.NoError(t, err)

	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:1234", nil)
	require.NoError(t, err)

	require.NoError(t, signer.sign(t.Context(), req, []byte(`{"jsonrpc":"2.0","method":"tools/list"}`)))

	auth := req.Header.Get("Authorization")
	require.Contains(t, auth, "AWS4-HMAC-SHA256")
	require.Contains(t, auth, "Credential=AKIAIOSFODNN7EXAMPLE")
	require.Contains(t, auth, "/us-east-1/bedrock-agentcore/aws4_request")
	require.Contains(t, auth, "SignedHeaders=host")
	require.NotEmpty(t, req.Header.Get("X-Amz-Date"))
	// The local listener address must remain the request target; signing only adds headers.
	require.Equal(t, "127.0.0.1:1234", req.URL.Host)
}

func brokenAWSBackendSigner() *awsBackendSigner {
	return &awsBackendSigner{
		signer:  awsauth.NewSignerWithProvider(credentials.StaticCredentialsProvider{Value: aws.Credentials{}}),
		region:  "us-east-1",
		service: "aws-mcp",
		host:    "aws-mcp.us-east-1.api.aws",
		path:    "/mcp",
	}
}

func TestAWSBackendSigner_Sign_EmptyCredentials(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:1234", nil)
	require.NoError(t, err)

	err = brokenAWSBackendSigner().sign(t.Context(), req, []byte(`{"jsonrpc":"2.0"}`))
	require.Error(t, err)
	require.ErrorContains(t, err, "cannot retrieve AWS credentials")
}

func TestLoadConfig_BuildsAWSSigners(t *testing.T) {
	p := &ProxyConfig{mcpProxyConfig: &mcpProxyConfig{}, toolChangeSignaler: newMultiWatcherSignaler()}
	err := p.LoadConfig(t.Context(), &filterapi.Config{
		MCPConfig: &filterapi.MCPConfig{
			BackendListenerAddr: "http://localhost:8080",
			Routes: []filterapi.MCPRoute{
				{
					Name: "default/route",
					Backends: []filterapi.MCPBackend{
						{
							Name: "aws-backend",
							AWSAuth: &filterapi.MCPBackendAWSAuth{
								Region:                "us-east-1",
								Service:               "bedrock-agentcore",
								Host:                  "bedrock-agentcore.us-east-1.amazonaws.com",
								Path:                  "/mcp",
								CredentialFileLiteral: testAWSCredentialsFile,
							},
						},
						{Name: "plain-backend"},
					},
				},
			},
		},
	})
	require.NoError(t, err)

	route := p.routes["default/route"]
	require.NotNil(t, route)
	require.Contains(t, route.awsSigners, "aws-backend")
	require.NotContains(t, route.awsSigners, "plain-backend")
}

func TestLoadConfig_InvalidAWSCredentials(t *testing.T) {
	p := &ProxyConfig{mcpProxyConfig: &mcpProxyConfig{}, toolChangeSignaler: newMultiWatcherSignaler()}
	err := p.LoadConfig(t.Context(), &filterapi.Config{
		MCPConfig: &filterapi.MCPConfig{
			BackendListenerAddr: "http://localhost:8080",
			Routes: []filterapi.MCPRoute{{
				Name: "default/route",
				Backends: []filterapi.MCPBackend{{
					Name: "aws-backend",
					AWSAuth: &filterapi.MCPBackendAWSAuth{
						Region:                "us-east-1",
						Service:               "bedrock-agentcore",
						Host:                  "bedrock-agentcore.us-east-1.amazonaws.com",
						Path:                  "/mcp",
						CredentialFileLiteral: "not a valid credentials file",
					},
				}},
			}},
		},
	})
	require.Error(t, err)
	require.ErrorContains(t, err, `failed to build AWS signer for backend "aws-backend"`)
}
