// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLogRequestHeaderAttributes(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		t.Cleanup(func() { SetLogRequestHeaderAttributes(nil) })
		SetLogRequestHeaderAttributes(nil)
		require.Nil(t, getLogRequestHeaderAttributes())
	})
	t.Run("empty", func(t *testing.T) {
		t.Cleanup(func() { SetLogRequestHeaderAttributes(nil) })
		SetLogRequestHeaderAttributes(map[string]string{})
		require.Nil(t, getLogRequestHeaderAttributes())
	})
	t.Run("copy-on-read", func(t *testing.T) {
		t.Cleanup(func() { SetLogRequestHeaderAttributes(nil) })
		attrs := map[string]string{
			"x-user-id":    "user.id",
			"x-session-id": "session.id",
		}
		SetLogRequestHeaderAttributes(attrs)
		actual := getLogRequestHeaderAttributes()
		require.Equal(t, attrs, actual)

		actual["x-session-id"] = "changed"
		actualAgain := getLogRequestHeaderAttributes()
		require.Equal(t, attrs, actualAgain)
	})
}
