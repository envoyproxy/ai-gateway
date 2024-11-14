package e2e

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tetratelabs/ai-gateway/internal/apischema/openai"
)

func initAWSSetting(t *testing.T, baseManifestPath string) (manifestPath, modelName string) {
	awsAccessKeyID := os.Getenv("TEST_AWS_ACCESS_KEY_ID")
	awsSecretAccessKey := os.Getenv("TEST_AWS_SECRET_ACCESS_KEY")
	awsRegion := os.Getenv("TEST_AWS_REGION")
	awsBedrockModelName := os.Getenv("TEST_AWS_BEDROCK_MODEL_NAME")
	if awsAccessKeyID == "" || awsSecretAccessKey == "" {
		t.Skip("TEST_AWS_ACCESS_KEY_ID and TEST_AWS_SECRET_ACCESS_KEY are required")
	}
	if awsRegion == "" {
		t.Skip("TEST_AWS_REGION is required")
	}
	if awsBedrockModelName == "" {
		t.Skip("TEST_AWS_BEDROCK_MODEL_NAME is required")
	}

	original, err := os.ReadFile(baseManifestPath)
	require.NoError(t, err)
	// Replace AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY with actual values.
	updated := []byte(strings.ReplaceAll(string(original), "AWS_ACCESS_KEY_ID", awsAccessKeyID))
	updated = []byte(strings.ReplaceAll(string(updated), "AWS_SECRET_ACCESS_KEY", awsSecretAccessKey))
	updated = []byte(strings.ReplaceAll(string(updated), "AWS_REGION", awsRegion))

	// Write the updated manifest to a temporary file.
	tmpdir := t.TempDir()
	tmpfile := filepath.Join(tmpdir, filepath.Base(baseManifestPath))
	require.NoError(t, os.WriteFile(tmpfile, updated, 0o600))
	return tmpfile, awsBedrockModelName
}

func testAWSInlineCredentials(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	manifestPath, modelName := initAWSSetting(t, "testdata/aws/inline.yaml")

	// Apply the updated manifest.
	require.NoError(t, applyManifest(ctx, manifestPath))
	defer func() {
		require.NoError(t, deleteManifest(ctx, manifestPath))
	}()

	// Wait for the pod to be ready.
	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=awsinline-gateway"
	requireWaitForPodReady(t, egNamespace, egSelector)

	t.Run("non-streaming", func(t *testing.T) {
		body := fmt.Sprintf(`{"model": "%s","messages": [{"role": "user", "content": "Say this is a test!"}],"temperature": 0.7}`, modelName)
		require.Eventually(t, func() bool {
			fwd := newPortForwarder(t, egNamespace, egSelector, egDefaultPort)
			require.NoError(t, fwd.Start())
			defer fwd.Stop()

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			req, err := http.NewRequestWithContext(ctx, http.MethodPost,
				fmt.Sprintf("http://%s/v1/chat/completions", fwd.Address()),
				strings.NewReader(body))
			require.NoError(t, err)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")
			req.Header.Set("x-ai-gateway-llm-backend", "aws-bedrock")

			client := http.Client{}
			resp, err := client.Do(req)
			require.NoError(t, err)
			defer func() {
				require.NoError(t, resp.Body.Close())
			}()
			actualResponseBody, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			fmt.Println("response status code: ", resp.StatusCode)
			fmt.Println("response body: ", string(actualResponseBody))
			if resp.StatusCode != http.StatusOK {
				return false
			}

			var b openai.ChatCompletionResponse
			require.NoError(t, json.Unmarshal(actualResponseBody, &b))
			fmt.Println("choices ", b.Choices)
			return len(b.Choices) > 0
		}, 3*time.Minute, 10*time.Second)
	})

	t.Run("streaming", func(t *testing.T) {
		t.Skip("TODO")
		fwd := newPortForwarder(t, egNamespace, egSelector, egDefaultPort)
		require.NoError(t, fwd.Start())
		defer fwd.Stop()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		// The body with "stream": true.
		body := fmt.Sprintf(`{"model": "%s","messages": [{"role": "user", "content": "Say this is a test!"}],"temperature": 0.7, "stream": true}`, modelName)
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			fmt.Sprintf("http://%s/v1/chat/completions", fwd.Address()),
			strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		// This is required for streaming, though it's not merged in the upstream SDKs:
		// * https://github.com/openai/openai-go/pull/94
		// * https://github.com/openai/openai-node/pull/1145
		// * https://github.com/openai/openai-python/pull/1815
		req.Header.Set("Accept", "text/event-stream")
		req.Header.Set("x-ai-gateway-llm-backend", "aws-bedrock")

		client := http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() {
			require.NoError(t, resp.Body.Close())
		}()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		// Reading the body line by line.
		var events []string
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			t.Log(scanner.Text())
			events = append(events, scanner.Text())
		}
		require.NoError(t, scanner.Err())

		// Last one must be [DONE].
		require.Equal(t, "data: [DONE]", events[len(events)-1])
	})

	// This ensures that non-AWS backends do not go through the AWS signing process.
	t.Run("non-aws-backend", func(t *testing.T) {
		newDefaultV1ChatCompletionCase("testupstream", "foo").
			setNonExpectedRequestHeaders("Authorization").
			run(t, egSelector, http.StatusOK)
	})
}

func testAWSCredentialsFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	tmpfile, modelName := initAWSSetting(t, "testdata/aws/credentials_file.yaml")

	// Apply the updated manifest.
	require.NoError(t, applyManifest(ctx, tmpfile))
	defer func() {
		require.NoError(t, deleteManifest(ctx, tmpfile))
	}()

	// Wait for the pod to be ready.
	const egSelector = "gateway.envoyproxy.io/owning-gateway-name=awscredentialsfile-gateway"
	requireWaitForPodReady(t, egNamespace, egSelector)

	body := fmt.Sprintf(`{"model": "%s","messages": [{"role": "user", "content": "Say this is a test!"}],"temperature": 0.7}`, modelName)
	require.Eventually(t, func() bool {
		fwd := newPortForwarder(t, egNamespace, egSelector, egDefaultPort)
		require.NoError(t, fwd.Start())
		defer fwd.Stop()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(ctx, http.MethodPost,
			fmt.Sprintf("http://%s/v1/chat/completions", fwd.Address()),
			strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		req.Header.Set("x-ai-gateway-llm-backend", "aws-bedrock")

		client := http.Client{}
		resp, err := client.Do(req)
		require.NoError(t, err)
		defer func() {
			require.NoError(t, resp.Body.Close())
		}()
		actualResponseBody, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		fmt.Println("response status code: ", resp.StatusCode)
		fmt.Println("response body: ", string(actualResponseBody))
		if resp.StatusCode != http.StatusOK {
			return false
		}
		var b openai.ChatCompletionResponse
		require.NoError(t, json.Unmarshal(actualResponseBody, &b))
		fmt.Println("choices ", b.Choices)
		return len(b.Choices) > 0
	}, 3*time.Minute, 10*time.Second)
}
