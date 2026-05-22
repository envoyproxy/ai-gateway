// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package piiredaction

import (
	"errors"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubTransformer is a minimal BodyTransformer used to exercise the registry.
type stubTransformer struct{}

func (stubTransformer) NewWrapper(*slog.Logger) Wrapper { return nil }

func TestNew_NoneIsDisabled(t *testing.T) {
	bt, err := New(ProviderNone, nil)
	require.NoError(t, err)
	require.Nil(t, bt, "ProviderNone must yield a nil transformer so callers can treat redaction as disabled")
}

func TestNew_UnknownProviderErrors(t *testing.T) {
	// presidio is a recognised name but is intentionally not registered yet.
	_, err := New(ProviderPresidio, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown provider")
}

func TestRegisterAndNew(t *testing.T) {
	const p Provider = "test-only-provider"

	var gotLogger bool
	Register(p, func(logger *slog.Logger) (BodyTransformer, error) {
		gotLogger = logger != nil
		return stubTransformer{}, nil
	})

	bt, err := New(p, slog.Default())
	require.NoError(t, err)
	require.NotNil(t, bt)
	require.True(t, gotLogger, "factory should receive the supplied logger")
}

func TestNew_FactoryErrorPropagates(t *testing.T) {
	const p Provider = "test-only-failing-provider"
	sentinel := errors.New("boom")
	Register(p, func(*slog.Logger) (BodyTransformer, error) { return nil, sentinel })

	_, err := New(p, nil)
	require.ErrorIs(t, err, sentinel)
}
