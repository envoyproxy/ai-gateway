// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package rotators

import (
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestNewSTSClient(t *testing.T) {
	stsClient := NewSTSClient(aws.Config{Region: "us-west-2"})
	require.NotNil(t, stsClient)
}

func TestFormatAWSCredentialsFile(t *testing.T) {
	profile := "default"
	accessKey := "AKIAXXXXXXXXXXXXXXXX"
	secretKey := "XXXXXXXXXXXXXXXXXXXX"
	sessionToken := "XXXXXXXXXXXXXXXXXXXX"
	region := "us-west-2"
	credentials := awsCredentials{
		profile:         profile,
		accessKeyID:     accessKey,
		secretAccessKey: secretKey,
		sessionToken:    sessionToken,
		region:          region,
	}

	awsCred := fmt.Sprintf("[%s]\naws_access_key_id = %s\naws_secret_access_key = %s\naws_session_token = %s\nregion = %s\n", profile, accessKey,
		secretKey, sessionToken, region)

	require.Equal(t, awsCred, formatAWSCredentialsFile(&awsCredentialsFile{credentials}))
}

func TestUpdateAWSCredentialsInSecret(t *testing.T) {
	secret := &corev1.Secret{}

	credentials := awsCredentials{
		profile:         "default",
		accessKeyID:     "accessKey",
		secretAccessKey: "secretKey",
		sessionToken:    "sessionToken",
		region:          "region",
	}

	updateAWSCredentialsInSecret(secret, &awsCredentialsFile{credentials})
	require.Len(t, secret.Data, 1)

	val, ok := secret.Data[AwsCredentialsKey]
	require.True(t, ok)
	require.NotEmpty(t, val)
}
