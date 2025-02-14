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

func TestParseAWSCredentialsFile(t *testing.T) {
	profile := "default"
	accessKey := "AKIAXXXXXXXXXXXXXXXX"
	secretKey := "XXXXXXXXXXXXXXXXXXXX"
	sessionToken := "XXXXXXXXXXXXXXXXXXXX"
	region := "us-west-2"
	awsCred := parseAWSCredentialsFile(fmt.Sprintf("[%s]\naws_access_key_id=%s\naws_secret_access_key=%s\naws_session_token=%s\nregion=%s", profile, accessKey,
		secretKey, sessionToken, region))
	require.NotNil(t, awsCred)
	defaultProfile, ok := awsCred.profiles[profile]
	require.True(t, ok)
	require.NotNil(t, defaultProfile)
	require.Equal(t, accessKey, defaultProfile.accessKeyID)
	require.Equal(t, secretKey, defaultProfile.secretAccessKey)
	require.Equal(t, sessionToken, defaultProfile.sessionToken)
	require.Equal(t, region, defaultProfile.region)
}

func TestFormatAWSCredentialsFile(t *testing.T) {
	emptyCredentialsFile := awsCredentialsFile{map[string]*awsCredentials{}}
	require.Empty(t, formatAWSCredentialsFile(&emptyCredentialsFile))

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

	require.Equal(t, awsCred, formatAWSCredentialsFile(&awsCredentialsFile{profiles: map[string]*awsCredentials{"default": &credentials}}))
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

	updateAWSCredentialsInSecret(secret, &awsCredentialsFile{profiles: map[string]*awsCredentials{"default": &credentials}})
	require.Len(t, secret.Data, 1)

	val, ok := secret.Data[awsCredentialsKey]
	require.True(t, ok)
	require.NotEmpty(t, val)
}
