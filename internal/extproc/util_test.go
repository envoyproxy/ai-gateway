// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"bytes"
	"compress/gzip"
	"io"
	"testing"

	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
	"github.com/stretchr/testify/require"
)

func TestIsGoodStatusCode(t *testing.T) {
	for _, s := range []int{200, 201, 299} {
		require.True(t, isGoodStatusCode(s))
	}
	for _, s := range []int{100, 300, 400, 500} {
		require.False(t, isGoodStatusCode(s))
	}
}

func TestDecodeContentIfNeeded(t *testing.T) {
	tests := []struct {
		name         string
		body         []byte
		encoding     string
		wantEncoded  bool
		wantEncoding string
		wantErr      bool
	}{
		{
			name:         "plain body",
			body:         []byte("hello world"),
			encoding:     "",
			wantEncoded:  false,
			wantEncoding: "",
			wantErr:      false,
		},
		{
			name:         "unsupported encoding",
			body:         []byte("hello world"),
			encoding:     "deflate",
			wantEncoded:  false,
			wantEncoding: "",
			wantErr:      false,
		},
		{
			name: "valid gzip",
			body: func() []byte {
				var b bytes.Buffer
				w := gzip.NewWriter(&b)
				_, err := w.Write([]byte("abc"))
				if err != nil {
					panic(err)
				}
				w.Close()
				return b.Bytes()
			}(),
			encoding:     "gzip",
			wantEncoded:  true,
			wantEncoding: "gzip",
			wantErr:      false,
		},
		{
			name:         "invalid gzip",
			body:         []byte("not a gzip"),
			encoding:     "gzip",
			wantEncoded:  false,
			wantEncoding: "",
			wantErr:      true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := decodeContentIfNeeded(tt.body, tt.encoding)
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.wantEncoded, res.isEncoded)
			if !tt.wantEncoded {
				out, _ := io.ReadAll(res.reader)
				require.Equal(t, tt.body, out)
			} else if tt.encoding == "gzip" && !tt.wantErr {
				out, _ := io.ReadAll(res.reader)
				require.Equal(t, []byte("abc"), out)
			}
		})
	}
}

func TestRemoveContentEncodingIfNeeded(t *testing.T) {
	tests := []struct {
		name        string
		hm          *extprocv3.HeaderMutation
		bm          *extprocv3.BodyMutation
		isEncoded   bool
		wantRemoved bool
	}{
		{
			name:        "no body mutation, not encoded",
			hm:          nil,
			bm:          nil,
			isEncoded:   false,
			wantRemoved: false,
		},
		{
			name:        "body mutation, not encoded",
			hm:          nil,
			bm:          &extprocv3.BodyMutation{},
			isEncoded:   false,
			wantRemoved: false,
		},
		{
			name:        "body mutation, encoded",
			hm:          nil,
			bm:          &extprocv3.BodyMutation{},
			isEncoded:   true,
			wantRemoved: true,
		},
		{
			name:        "existing header mutation, body mutation, encoded",
			hm:          &extprocv3.HeaderMutation{RemoveHeaders: []string{"foo"}},
			bm:          &extprocv3.BodyMutation{},
			isEncoded:   true,
			wantRemoved: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := removeContentEncodingIfNeeded(tt.hm, tt.bm, tt.isEncoded)
			if tt.wantRemoved {
				require.Contains(t, res.RemoveHeaders, "content-encoding")
			} else if res != nil {
				require.NotContains(t, res.RemoveHeaders, "content-encoding")
			}
		})
	}
}

func TestHeaderMutationCarrier(t *testing.T) {
	t.Run("Get panics", func(t *testing.T) {
		carrier := &headerMutationCarrier{m: &extprocv3.HeaderMutation{}}
		require.Panics(t, func() { carrier.Get("test-key") })
	})

	t.Run("Keys panics", func(t *testing.T) {
		carrier := &headerMutationCarrier{m: &extprocv3.HeaderMutation{}}
		require.Panics(t, func() { carrier.Keys() })
	})

	t.Run("Set headers", func(t *testing.T) {
		mutation := &extprocv3.HeaderMutation{}
		carrier := &headerMutationCarrier{m: mutation}

		carrier.Set("trace-id", "12345")
		carrier.Set("span-id", "67890")

		require.Equal(t, &extprocv3.HeaderMutation{
			SetHeaders: []*corev3.HeaderValueOption{
				{
					Header: &corev3.HeaderValue{
						Key:      "trace-id",
						RawValue: []byte("12345"),
					},
				},
				{
					Header: &corev3.HeaderValue{
						Key:      "span-id",
						RawValue: []byte("67890"),
					},
				},
			},
		}, mutation)
	})
}
