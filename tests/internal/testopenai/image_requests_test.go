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

func TestImageCassettes(t *testing.T) {
	testCassettes(t, ImageCassettes(), imageRequests)
}

func TestNewRequestImages(t *testing.T) {
	tests, err := buildTestCases(t, imageRequests)
	require.NoError(t, err)
	for i := range tests {
		// Currently only basic happy-path image generation cassette
		tests[i].expectedStatus = http.StatusOK
	}
	testNewRequest(t, tests)
}
