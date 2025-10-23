// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.


package backendauth

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/internal/filterapi"
)

// BenchmarkAWSHandler_Do benchmarks the Do method with different credential sources.
// Run with: go test -bench=BenchmarkAWSHandler_Do -benchmem -tags=benchmark ./internal/extproc/backendauth/
func BenchmarkAWSHandler_Do(b *testing.B) {
	b.Run("file_credentials", func(b *testing.B) {
		awsFileBody := "[default]\naws_access_key_id=AKIAIOSFODNN7EXAMPLE\naws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n"
		handler, err := newAWSHandler(b.Context(), &filterapi.AWSAuth{
			CredentialFileLiteral: awsFileBody,
			Region:                "us-east-1",
		})
		require.NoError(b, err)

		reqHeaders, headerMut, bodyMut := createTestRequest(
			"POST",
			"/model/anthropic.claude-v2/converse",
			[]byte(`{"messages": [{"role": "user", "content": [{"text": "Hello"}]}]}`),
		)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			err := handler.Do(b.Context(), reqHeaders, headerMut, bodyMut)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("default_chain_env_credentials", func(b *testing.B) {
		b.Setenv("AWS_ACCESS_KEY_ID", "AKIAIOSFODNN7EXAMPLE")
		b.Setenv("AWS_SECRET_ACCESS_KEY", "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY")

		handler, err := newAWSHandler(b.Context(), &filterapi.AWSAuth{
			Region: "us-east-1",
		})
		require.NoError(b, err)

		reqHeaders, headerMut, bodyMut := createTestRequest(
			"POST",
			"/model/anthropic.claude-v2/converse",
			[]byte(`{"messages": [{"role": "user", "content": [{"text": "Hello"}]}]}`),
		)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			err := handler.Do(b.Context(), reqHeaders, headerMut, bodyMut)
			if err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("concurrent", func(b *testing.B) {
		awsFileBody := "[default]\naws_access_key_id=AKIAIOSFODNN7EXAMPLE\naws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n"
		handler, err := newAWSHandler(b.Context(), &filterapi.AWSAuth{
			CredentialFileLiteral: awsFileBody,
			Region:                "us-east-1",
		})
		require.NoError(b, err)

		b.RunParallel(func(pb *testing.PB) {
			for pb.Next() {
				reqHeaders, headerMut, bodyMut := createTestRequest(
					"POST",
					"/model/anthropic.claude-v2/converse",
					[]byte(`{"messages": [{"role": "user", "content": [{"text": "Hello"}]}]}`),
				)
				err := handler.Do(b.Context(), reqHeaders, headerMut, bodyMut)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	})

	b.Run("just_credential_retrieve", func(b *testing.B) {
		awsFileBody := "[default]\naws_access_key_id=AKIAIOSFODNN7EXAMPLE\naws_secret_access_key=wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY\n"
		handler, err := newAWSHandler(b.Context(), &filterapi.AWSAuth{
			CredentialFileLiteral: awsFileBody,
			Region:                "us-east-1",
		})
		require.NoError(b, err)

		awsH := handler.(*awsHandler)

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			_, err := awsH.credentialsProvider.Retrieve(b.Context())
			if err != nil {
				b.Fatal(err)
			}
		}
	})
}
