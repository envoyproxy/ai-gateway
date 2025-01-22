package backendauth

import (
	"os"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterconfig"
)

func TestNewAWSHandler(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")

	handler, err := newAWSHandler(nil)
	require.NoError(t, err)
	require.NotNil(t, handler)
}

func TestAWSHandler_Do(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")

	envHandler, err := newAWSHandler(nil)
	require.NoError(t, err)

	// Test AWS credential file.
	awsFileBody := "[default]\nAWS_ACCESS_KEY_ID=test\nAWS_SECRET_ACCESS_KEY=secret\n"
	awsCredentialFile := t.TempDir() + "/aws_handler"

	file, err := os.Create(awsCredentialFile)
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	_, err = file.WriteString(awsFileBody)
	require.NoError(t, err)
	require.NoError(t, file.Sync())

	credentialFileHandler, err := newAWSHandler(&filterconfig.AWSAuth{
		CredentialFileName: awsCredentialFile,
		Region:             "us-east-1",
	})

	for _, tc := range []struct {
		name    string
		handler *awsHandler
	}{
		{
			name:    "Using ENV",
			handler: envHandler,
		},
		{
			name:    "Using AWS Credential File",
			handler: credentialFileHandler,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requestHeaders := map[string]string{":method": "POST"}
			headerMut := &extprocv3.HeaderMutation{
				SetHeaders: []*corev3.HeaderValueOption{
					{Header: &corev3.HeaderValue{
						Key:   ":path",
						Value: "/model/some-random-model/converse",
					}},
				},
			}
			bodyMut := &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{
					Body: []byte(`{"messages": [{"role": "user", "content": [{"text": "Say this is a test!"}]}]}`),
				},
			}
			err = tc.handler.Do(requestHeaders, headerMut, bodyMut)
			require.NoError(t, err)
		})
	}
}
