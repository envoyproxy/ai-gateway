// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package backendauth

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/envoyproxy/ai-gateway/filterapi"
)

func TestNewAzureHandler_MissingConfigFile(t *testing.T) {
	handler, err := newAzureHandler(&filterapi.AzureAuth{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to read azure access token file")
	require.Nil(t, handler)
}

func TestNewAzureHandler_EmptyAccessToken(t *testing.T) {
	fileContent := "[default]\nazure_access_token=\n"
	fileName := t.TempDir() + "/azure_token"
	file, err := os.Create(fileName)

	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	_, err = file.WriteString(fileContent)
	require.NoError(t, err)
	require.NoError(t, file.Sync())

	handler, err := newAzureHandler(&filterapi.AzureAuth{Filename: fileName})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "azure_access_token not found in the secret file")
	require.Nil(t, handler)
}

func TestNewAzureHandler(t *testing.T) {
	fileContent := "[default]\nazure_access_token=test\n"
	fileName := t.TempDir() + "/azure_token"
	file, err := os.Create(fileName)

	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	_, err = file.WriteString(fileContent)
	require.NoError(t, err)
	require.NoError(t, file.Sync())

	handler, err := newAzureHandler(&filterapi.AzureAuth{Filename: fileName})
	require.NoError(t, err)
	require.NotNil(t, handler)
}
