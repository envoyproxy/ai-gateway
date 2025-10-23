// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"sync"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

// Test helper to extract headers from HeaderMutation
func extractHeaders(headerMut *extprocv3.HeaderMutation) map[string]string {
	headers := map[string]string{}
	for _, h := range headerMut.SetHeaders {
		value := h.Header.Value
		if value == "" && len(h.Header.RawValue) > 0 {
			value = string(h.Header.RawValue)
		}
		headers[h.Header.Key] = value
	}
	return headers
}

// Test helper to create a test request
func createTestRequest(method, path string, body []byte) (map[string]string, *extprocv3.HeaderMutation, *extprocv3.BodyMutation) {
	requestHeaders := map[string]string{":method": method}
	headerMut := &extprocv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{Header: &corev3.HeaderValue{Key: ":path", Value: path}},
		},
	}
	bodyMut := &extprocv3.BodyMutation{}
	if len(body) > 0 {
		bodyMut.Mutation = &extprocv3.BodyMutation_Body{Body: body}
	}
	return requestHeaders, headerMut, bodyMut
}

func TestNewAWSHandler(t *testing.T) {
	t.Run("credentials file", func(t *testing.T) {
		awsFileBody := "[default]\naws_access_key_id=test\naws_secret_access_key=secret\n"
		handler, err := newAWSHandler(t.Context(), &filterapi.AWSAuth{
			CredentialFileLiteral: awsFileBody,
			Region:                "us-east-1",
		})
		require.NoError(t, err)
		require.NotNil(t, handler)

		awsH, ok := handler.(*awsHandler)
		require.True(t, ok)
		require.Equal(t, "us-east-1", awsH.region)
		require.NotNil(t, awsH.credentialsProvider)
		require.NotNil(t, awsH.signer)
	})

	t.Run("default credential chain with environment variables", func(t *testing.T) {
		// Set temporary environment variables for testing
		t.Setenv("AWS_ACCESS_KEY_ID", "test-key-id")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")

		// Note: AWS SDK's default credential chain will try multiple sources:
		// 1. Environment variables (AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY)
		// 2. Web identity token (IRSA) - AWS_ROLE_ARN, AWS_WEB_IDENTITY_TOKEN_FILE
		// 3. EKS Pod Identity
		// 4. EC2 instance metadata
		// 5. Shared credentials file
		//
		// This test validates the default credential chain works with environment variables
		handler, err := newAWSHandler(t.Context(), &filterapi.AWSAuth{
			Region: "us-west-2",
		})
		require.NoError(t, err)
		require.NotNil(t, handler)

		awsH, ok := handler.(*awsHandler)
		require.True(t, ok)
		require.Equal(t, "us-west-2", awsH.region)

		// Verify credentials can be retrieved from environment
		creds, err := awsH.credentialsProvider.Retrieve(t.Context())
		require.NoError(t, err)
		require.Equal(t, "test-key-id", creds.AccessKeyID)
		require.Equal(t, "test-secret-key", creds.SecretAccessKey)
	})

	t.Run("default credential chain without credentials", func(t *testing.T) {
		// Clear AWS environment variables to ensure no credentials are available
		t.Setenv("AWS_ACCESS_KEY_ID", "")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "")
		t.Setenv("AWS_SESSION_TOKEN", "")
		t.Setenv("AWS_PROFILE", "")
		t.Setenv("AWS_SHARED_CREDENTIALS_FILE", "/dev/null")
		t.Setenv("AWS_CONFIG_FILE", "/dev/null")
		t.Setenv("AWS_ROLE_ARN", "")
		t.Setenv("AWS_WEB_IDENTITY_TOKEN_FILE", "")

		handler, err := newAWSHandler(t.Context(), &filterapi.AWSAuth{
			Region: "us-east-1",
		})
		// Handler creation should succeed even without credentials
		// (credentials are retrieved lazily at signing time)
		require.NoError(t, err)
		require.NotNil(t, handler)

		// But calling Do() should fail when no credentials are available
		reqHeaders, headerMut, bodyMut := createTestRequest("POST", "/model/test/converse", []byte(`{"test": "data"}`))
		err = handler.Do(t.Context(), reqHeaders, headerMut, bodyMut)
		require.Error(t, err)
		require.Contains(t, err.Error(), "cannot retrieve AWS credentials")
	})

	t.Run("nil config", func(t *testing.T) {
		handler, err := newAWSHandler(t.Context(), nil)
		require.Error(t, err)
		require.Nil(t, handler)
		require.Contains(t, err.Error(), "aws auth configuration is required")
	})
}

func TestAWSHandler_Do(t *testing.T) {
	t.Run("concurrent signing with credentials file", func(t *testing.T) {
		awsFileBody := "[default]\naws_access_key_id=test\naws_secret_access_key=secret\n"
		handler, err := newAWSHandler(t.Context(), &filterapi.AWSAuth{
			CredentialFileLiteral: awsFileBody,
			Region:                "us-east-1",
		})
		require.NoError(t, err)

		// Handler.Do is called concurrently, so we test it with 100 goroutines to ensure it is thread-safe.
		var wg sync.WaitGroup
		wg.Add(100)
		for range 100 {
			go func() {
				defer wg.Done()
				reqHeaders, headerMut, bodyMut := createTestRequest(
					"POST",
					"/model/some-random-model/converse",
					[]byte(`{"messages": [{"role": "user", "content": [{"text": "Say this is a test!"}]}]}`),
				)
				err := handler.Do(t.Context(), reqHeaders, headerMut, bodyMut)
				require.NoError(t, err)

				headers := extractHeaders(headerMut)
				require.Contains(t, headers, "X-Amz-Date")
				require.Contains(t, headers, "Authorization")
			}()
		}
		wg.Wait()
	})

	t.Run("default credential chain with static env vars", func(t *testing.T) {
		// Test the default credential chain with basic static credentials
		// This validates IRSA/Pod Identity mechanism (credentials from environment)
		t.Setenv("AWS_ACCESS_KEY_ID", "AKIAIOSFODNN7EXAMPLE")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")

		handler, err := newAWSHandler(t.Context(), &filterapi.AWSAuth{
			Region: "us-east-1",
		})
		require.NoError(t, err)

		reqHeaders, headerMut, bodyMut := createTestRequest(
			"POST",
			"/model/amazon.titan-text-express-v1/invoke",
			[]byte(`{"inputText": "Hello from default chain"}`),
		)

		err = handler.Do(t.Context(), reqHeaders, headerMut, bodyMut)
		require.NoError(t, err)

		headers := extractHeaders(headerMut)
		require.Contains(t, headers, "X-Amz-Date")
		require.Contains(t, headers, "Authorization")
		// Verify the authorization header contains the access key ID
		require.Contains(t, headers["Authorization"], "Credential=AKIAIOSFODNN7EXAMPLE")
	})

	t.Run("session token in headers", func(t *testing.T) {
		// Test that session tokens (temporary credentials from STS/IRSA) are properly included
		t.Setenv("AWS_ACCESS_KEY_ID", "ASIATESTACCESSKEY")
		t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret-key")
		t.Setenv("AWS_SESSION_TOKEN", "temporary-session-token-xyz")

		handler, err := newAWSHandler(t.Context(), &filterapi.AWSAuth{
			Region: "eu-central-1",
		})
		require.NoError(t, err)

		reqHeaders, headerMut, bodyMut := createTestRequest(
			"POST",
			"/model/anthropic.claude-v2/converse",
			[]byte(`{"messages": []}`),
		)

		err = handler.Do(t.Context(), reqHeaders, headerMut, bodyMut)
		require.NoError(t, err)

		headers := extractHeaders(headerMut)
		require.Contains(t, headers, "X-Amz-Date")
		require.Contains(t, headers, "Authorization")
		require.Contains(t, headers, "X-Amz-Security-Token")
		require.Equal(t, "temporary-session-token-xyz", headers["X-Amz-Security-Token"])
	})

	t.Run("different HTTP methods", func(t *testing.T) {
		awsFileBody := "[default]\naws_access_key_id=test\naws_secret_access_key=secret\n"
		handler, err := newAWSHandler(t.Context(), &filterapi.AWSAuth{
			CredentialFileLiteral: awsFileBody,
			Region:                "us-east-1",
		})
		require.NoError(t, err)

		methods := []string{"POST", "GET", "PUT"}
		for _, method := range methods {
			reqHeaders, headerMut, bodyMut := createTestRequest(
				method,
				"/model/test-model/invoke",
				[]byte(`{"test": "data"}`),
			)
			err := handler.Do(t.Context(), reqHeaders, headerMut, bodyMut)
			require.NoError(t, err, "Failed for method: %s", method)

			headers := extractHeaders(headerMut)
			require.Contains(t, headers, "Authorization", "Missing Authorization for method: %s", method)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		awsFileBody := "[default]\naws_access_key_id=test\naws_secret_access_key=secret\n"
		handler, err := newAWSHandler(t.Context(), &filterapi.AWSAuth{
			CredentialFileLiteral: awsFileBody,
			Region:                "ap-northeast-1",
		})
		require.NoError(t, err)

		reqHeaders, headerMut, bodyMut := createTestRequest("GET", "/models", nil)
		err = handler.Do(t.Context(), reqHeaders, headerMut, bodyMut)
		require.NoError(t, err)

		headers := extractHeaders(headerMut)
		require.Contains(t, headers, "Authorization")
		require.Contains(t, headers, "X-Amz-Date")
	})

	t.Run("multiple regions", func(t *testing.T) {
		awsFileBody := "[default]\naws_access_key_id=test\naws_secret_access_key=secret\n"
		regions := []string{"us-east-1", "eu-west-1", "ap-southeast-1"}

		for _, region := range regions {
			handler, err := newAWSHandler(t.Context(), &filterapi.AWSAuth{
				CredentialFileLiteral: awsFileBody,
				Region:                region,
			})
			require.NoError(t, err)

			reqHeaders, headerMut, bodyMut := createTestRequest(
				"POST",
				"/model/test/converse",
				[]byte(`{"test": "data"}`),
			)
			err = handler.Do(t.Context(), reqHeaders, headerMut, bodyMut)
			require.NoError(t, err, "Failed for region: %s", region)
		}
	})
}
