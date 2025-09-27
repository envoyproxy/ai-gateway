// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func Test_healthcheck(t *testing.T) {
	tests := []struct {
		name        string
		closeServer bool
		statusCode  int
		respBody    string
		expOut      string
		expErr      string
	}{
		{
			name:       "success",
			statusCode: http.StatusOK,
			respBody:   "OK",
			expOut:     "OK",
		},
		{
			name:       "unhealthy status",
			statusCode: http.StatusServiceUnavailable,
			respBody:   "not ready",
			expErr:     "unhealthy: status 503, body: not ready",
		},
		{
			name:       "internal error",
			statusCode: http.StatusInternalServerError,
			respBody:   "server error",
			expErr:     "unhealthy: status 500, body: server error",
		},
		{
			name:        "connection failure",
			closeServer: true,
			expErr:      "failed to connect to admin server",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.statusCode)
				_, _ = w.Write([]byte(tt.respBody))
			}))
			t.Cleanup(s.Close)

			u, err := url.Parse(s.URL)
			require.NoError(t, err)
			port, err := strconv.Atoi(u.Port())
			require.NoError(t, err)

			if tt.closeServer {
				s.Close()
			}

			stdout := &bytes.Buffer{}
			err = healthcheck(t.Context(), port, stdout, nil)

			if tt.expErr != "" {
				require.Equal(t, tt.expErr, err.Error())
				require.Empty(t, stdout.String())
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expOut, stdout.String())
			}
		})
	}
}
