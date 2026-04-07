// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"net/http"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAudioTranscriptionCassettes(t *testing.T) {
	testCassettes(t, AudioTranscriptionCassettes(), audioTranscriptionRequests)
}

func TestAudioTranslationCassettes(t *testing.T) {
	testCassettes(t, AudioTranslationCassettes(), audioTranslationRequests)
}

func TestNewRequestAudioTranscription(t *testing.T) {
	if savedOpenAIKey != "" {
		t.Setenv("OPENAI_API_KEY", savedOpenAIKey)
	}

	server, err := NewServer(os.Stdout, 0)
	require.NoError(t, err)
	defer server.Close()

	for _, cassette := range AudioTranscriptionCassettes() {
		t.Run(cassette.String(), func(t *testing.T) {
			req, err := NewRequest(t.Context(), server.URL(), cassette)
			require.NoError(t, err)
			require.Equal(t, http.MethodPost, req.Method)
			require.Contains(t, req.Header.Get("Content-Type"), "multipart/form-data")
			require.Equal(t, cassette.String(), req.Header.Get(CassetteNameHeader))

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}

func TestNewRequestAudioTranslation(t *testing.T) {
	if savedOpenAIKey != "" {
		t.Setenv("OPENAI_API_KEY", savedOpenAIKey)
	}

	server, err := NewServer(os.Stdout, 0)
	require.NoError(t, err)
	defer server.Close()

	for _, cassette := range AudioTranslationCassettes() {
		t.Run(cassette.String(), func(t *testing.T) {
			req, err := NewRequest(t.Context(), server.URL(), cassette)
			require.NoError(t, err)
			require.Equal(t, http.MethodPost, req.Method)
			require.Contains(t, req.Header.Get("Content-Type"), "multipart/form-data")
			require.Equal(t, cassette.String(), req.Header.Get(CassetteNameHeader))

			resp, err := http.DefaultClient.Do(req)
			require.NoError(t, err)
			defer resp.Body.Close()
			require.Equal(t, http.StatusOK, resp.StatusCode)
		})
	}
}
