// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extensionserver

import (
	"testing"

	"github.com/go-logr/logr"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	logger := logr.Discard()
	s := New(logger)
	require.NotNil(t, s)
}

func TestCheck(t *testing.T) {
	logger := logr.Discard()
	s := New(logger)
	_, err := s.Check(t.Context(), nil)
	require.NoError(t, err)
}

func TestWatch(t *testing.T) {
	logger := logr.Discard()
	s := New(logger)
	err := s.Watch(nil, nil)
	require.Error(t, err)
	require.Equal(t, "rpc error: code = Unimplemented desc = Watch is not implemented", err.Error())
}
