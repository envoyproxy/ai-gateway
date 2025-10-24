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

func TestDecodeContentWithBuffering(t *testing.T) {
	t.Run("successful buffering and decompression", func(t *testing.T) {
		// Test complete gzip buffering scenario
		completeSSEResponse := `data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}

data: {"type":"message_delta","usage":{"output_tokens":5}}

data: {"type":"message_delta","usage":{"output_tokens":3}}

data: [DONE]

`

		// Create gzipped version
		var gzipBuf bytes.Buffer
		gzipWriter := gzip.NewWriter(&gzipBuf)
		_, err := gzipWriter.Write([]byte(completeSSEResponse))
		require.NoError(t, err)
		gzipWriter.Close()
		gzippedData := gzipBuf.Bytes()

		// Split into chunks to simulate streaming
		chunk1 := gzippedData[:len(gzippedData)/3]
		chunk2 := gzippedData[len(gzippedData)/3 : 2*len(gzippedData)/3]
		chunk3 := gzippedData[2*len(gzippedData)/3:]

		var buffer []byte

		// Test chunk 1 - should buffer
		res1, err := decodeContentWithBuffering(chunk1, "gzip", &buffer, false)
		require.NoError(t, err)
		require.True(t, res1.isEncoded)
		data1, _ := io.ReadAll(res1.reader)
		require.Empty(t, data1)          // Should return empty reader while buffering
		require.Equal(t, chunk1, buffer) // Should be in buffer

		// Test chunk 2 - should continue buffering
		res2, err := decodeContentWithBuffering(chunk2, "gzip", &buffer, false)
		require.NoError(t, err)
		require.True(t, res2.isEncoded)
		data2, _ := io.ReadAll(res2.reader)
		require.Empty(t, data2) // Should return empty reader while buffering
		expectedBuffer := make([]byte, 0, len(chunk1)+len(chunk2))
		expectedBuffer = append(expectedBuffer, chunk1...)
		expectedBuffer = append(expectedBuffer, chunk2...)
		require.Equal(t, expectedBuffer, buffer) // Should accumulate in buffer

		// Test chunk 3 (endOfStream=true) - should decompress complete buffer
		res3, err := decodeContentWithBuffering(chunk3, "gzip", &buffer, true)
		require.NoError(t, err)
		require.True(t, res3.isEncoded)
		data3, _ := io.ReadAll(res3.reader)
		require.Equal(t, []byte(completeSSEResponse), data3) // Should return decompressed data
		require.Empty(t, buffer)                             // Buffer should be cleared
	})

	t.Run("invalid gzip header at end of stream", func(t *testing.T) {
		var buffer []byte
		invalidGzipData := []byte("not a gzip header")

		// Should pass through data when endOfStream=true and invalid gzip
		res, err := decodeContentWithBuffering(invalidGzipData, "gzip", &buffer, true)
		require.NoError(t, err)
		require.True(t, res.isEncoded)

		data, _ := io.ReadAll(res.reader)
		require.Equal(t, invalidGzipData, data)
		require.Empty(t, buffer) // Buffer should be cleared
	})

	t.Run("decompression fails at end of stream", func(t *testing.T) {
		var buffer []byte
		// Create truncated gzip data that has valid header but incomplete content
		var gzipBuf bytes.Buffer
		gzipWriter := gzip.NewWriter(&gzipBuf)
		_, err := gzipWriter.Write([]byte("test data"))
		require.NoError(t, err)
		gzipWriter.Close()
		truncatedGzip := gzipBuf.Bytes()[:15] // Truncate to make decompression fail

		// Should pass through data when endOfStream=true and decompression fails
		res, err := decodeContentWithBuffering(truncatedGzip, "gzip", &buffer, true)
		require.NoError(t, err)
		require.True(t, res.isEncoded)

		data, _ := io.ReadAll(res.reader)
		require.Equal(t, truncatedGzip, data)
		require.Empty(t, buffer) // Buffer should be cleared
	})

	t.Run("empty buffer case", func(t *testing.T) {
		var buffer []byte
		emptyBody := []byte{}

		// Should handle empty body gracefully
		res, err := decodeContentWithBuffering(emptyBody, "gzip", &buffer, false)
		require.NoError(t, err)
		require.True(t, res.isEncoded)

		data, _ := io.ReadAll(res.reader)
		require.Empty(t, data)   // Should return empty reader for empty body
		require.Empty(t, buffer) // Buffer should remain empty
	})

	t.Run("non-gzip encoding", func(t *testing.T) {
		var buffer []byte
		testData := []byte("plain text data")

		// Should pass through non-gzip data unchanged
		res, err := decodeContentWithBuffering(testData, "deflate", &buffer, false)
		require.NoError(t, err)
		require.False(t, res.isEncoded)

		data, _ := io.ReadAll(res.reader)
		require.Equal(t, testData, data)
		require.Empty(t, buffer) // Buffer should remain empty for non-gzip
	})

	t.Run("empty encoding", func(t *testing.T) {
		var buffer []byte
		testData := []byte("plain text data")

		// Should pass through data with empty encoding
		res, err := decodeContentWithBuffering(testData, "", &buffer, false)
		require.NoError(t, err)
		require.False(t, res.isEncoded)

		data, _ := io.ReadAll(res.reader)
		require.Equal(t, testData, data)
		require.Empty(t, buffer) // Buffer should remain empty for non-gzip
	})

	t.Run("invalid gzip header with endOfStream=false", func(t *testing.T) {
		var buffer []byte
		invalidGzipData := []byte("not a gzip header")

		// Should buffer and return empty reader when endOfStream=false and invalid gzip
		res, err := decodeContentWithBuffering(invalidGzipData, "gzip", &buffer, false)
		require.NoError(t, err)
		require.True(t, res.isEncoded)

		data, _ := io.ReadAll(res.reader)
		require.Empty(t, data)                    // Should return empty reader while buffering
		require.Equal(t, invalidGzipData, buffer) // Should accumulate in buffer
	})
}

func TestNonGzipPassthrough(t *testing.T) {
	// Test that non-gzip data passes through unchanged
	testData := []byte(`{"type":"message_start","usage":{"input_tokens":10}}`)

	res, err := decodeContentIfNeeded(testData, "")
	require.NoError(t, err)
	require.False(t, res.isEncoded)

	output, _ := io.ReadAll(res.reader)
	require.Equal(t, testData, output)
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
