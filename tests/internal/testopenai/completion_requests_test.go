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

func TestCompletionCassettes(t *testing.T) {
	testCassettes(t, CompletionCassettes(), completionRequests)
}

func TestNewRequestCompletion(t *testing.T) {
	tests, err := buildTestCases(t, completionRequests)
	require.NoError(t, err)
	for i := range tests {
		switch tests[i].cassette {
		case CassetteCompletionBadRequest:
			tests[i].expectedStatus = http.StatusBadRequest
		case CassetteCompletionUnknownModel:
			tests[i].expectedStatus = http.StatusNotFound
		default:
			tests[i].expectedStatus = http.StatusOK
		}
	}
	testNewRequest(t, tests)
}
