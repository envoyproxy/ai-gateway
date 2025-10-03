// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testopenai

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEmbeddingsCassettes(t *testing.T) {
	testCassettes(t, EmbeddingsCassettes(), embeddingsRequests)
}

func TestNewRequestEmbeddings(t *testing.T) {
	tests, err := buildTestCases(t, embeddingsRequests)
	require.NoError(t, err)
	for i := range tests {
		switch tests[i].cassette {
		case CassetteEmbeddingsBadRequest:
			tests[i].expectedStatus = http.StatusBadRequest
		case CassetteEmbeddingsUnknownModel:
			tests[i].expectedStatus = http.StatusNotFound
		default:
			tests[i].expectedStatus = http.StatusOK
		}
	}
	testNewRequest(t, tests)
}
