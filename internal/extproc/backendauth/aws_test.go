package backendauth

import (
	"os"
	"sync"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestNewAWSHandler(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")

	handler, err := newAWSHandler(t.Context(), &filterapi.AWSAuth{})
	require.NoError(t, err)
	require.NotNil(t, handler)
}

func TestAWSHandler_Do(t *testing.T) {
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "secret")

	// Test AWS credential file.
	awsFileBody := "[default]\nAWS_ACCESS_KEY_ID=test\nAWS_SECRET_ACCESS_KEY=secret\n"
	awsCredentialFile := t.TempDir() + "/aws_handler"

	file, err := os.Create(awsCredentialFile)
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	_, err = file.WriteString(awsFileBody)
	require.NoError(t, err)
	require.NoError(t, file.Sync())

	credentialFileHandler, err := newAWSHandler(t.Context(), &filterapi.AWSAuth{
		CredentialFileName: awsCredentialFile,
		Region:             "us-east-1",
	})
	require.NoError(t, err)

	// Handler.Do is called concurrently, so we test it with 100 goroutines to ensure it is thread-safe.
	var wg sync.WaitGroup
	wg.Add(100)
	for range 100 {
		go func() {
			defer wg.Done()
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
			err := credentialFileHandler.Do(t.Context(), requestHeaders, headerMut, bodyMut)
			require.NoError(t, err)

			// Ensures that the headers are set.
			headers := map[string]string{}
			for _, h := range headerMut.SetHeaders {
				headers[h.Header.Key] = h.Header.Value
			}
			require.Contains(t, headers, "X-Amz-Date")
			require.Contains(t, headers, "Authorization")
		}()
	}

	wg.Wait()
}
