// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package extproc

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"log/slog"

	"github.com/andybalholm/brotli"
	corev3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	extprocv3 "github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"

	"github.com/envoyproxy/ai-gateway/internal/extproc/bodymutator"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// contentDecodingResult contains the result of content decoding operation.
type contentDecodingResult struct {
	reader    io.Reader
	isEncoded bool
}

// decodeContentIfNeeded decompresses the response body based on the content-encoding header.
// Currently, supports gzip and brotli encoding, but can be extended to support other encodings in the future.
// Returns a reader for the (potentially decompressed) body and metadata about the encoding.
func decodeContentIfNeeded(body []byte, contentEncoding string) (contentDecodingResult, error) {
	switch contentEncoding {
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return contentDecodingResult{}, fmt.Errorf("failed to decode gzip: %w", err)
		}
		return contentDecodingResult{
			reader:    reader,
			isEncoded: true,
		}, nil
	case "br":
		reader := brotli.NewReader(bytes.NewReader(body))
		return contentDecodingResult{
			reader:    reader,
			isEncoded: true,
		}, nil
	default:
		return contentDecodingResult{
			reader:    bytes.NewReader(body),
			isEncoded: false,
		}, nil
	}
}

// removeContentEncodingIfNeeded removes the content-encoding header if the body was modified and was encoded.
// This is needed when the transformation modifies the body content but the response was originally compressed.
func removeContentEncodingIfNeeded(headerMutation *extprocv3.HeaderMutation, bodyMutation *extprocv3.BodyMutation, isEncoded bool) *extprocv3.HeaderMutation {
	if bodyMutation != nil && isEncoded {
		if headerMutation == nil {
			headerMutation = &extprocv3.HeaderMutation{}
		}
		// TODO: this is a hotfix, we should update this to recompress since its in the header
		// If the upstream response was compressed and we decompressed it,
		// ensure we remove the content-encoding header.
		//
		// This is only needed when the transformation is actually modifying the body.
		headerMutation.RemoveHeaders = append(headerMutation.RemoveHeaders, "content-encoding")
	}
	return headerMutation
}

// isGoodStatusCode checks if the HTTP status code of the upstream response is successful.
// The 2xx - Successful: The request is received by upstream and processed successfully.
// https://developer.mozilla.org/en-US/docs/Web/HTTP/Status#successful_responses
func isGoodStatusCode(code int) bool {
	return code >= 200 && code < 300
}

// mutationsFromTranslationResult creates header and body mutations based on the results of the translation.
//
// Note that newBody is nil-sensitive, so if it is nil, no body mutation will be created. If it is the empty
// slice, not nil, then a body mutation will be created with an empty body, which can be used to clear the body of the response.
func mutationsFromTranslationResult(newHeaders []internalapi.Header, newBody []byte) (
	header *extprocv3.HeaderMutation,
	body *extprocv3.BodyMutation,
) {
	header = &extprocv3.HeaderMutation{}
	for _, h := range newHeaders {
		header.SetHeaders = append(header.SetHeaders, &corev3.HeaderValueOption{
			AppendAction: corev3.HeaderValueOption_OVERWRITE_IF_EXISTS_OR_ADD,
			Header: &corev3.HeaderValue{
				Key:      h.Key(),
				RawValue: []byte(h.Value()),
			},
		})
	}
	if newBody != nil {
		body = &extprocv3.BodyMutation{Mutation: &extprocv3.BodyMutation_Body{Body: newBody}}
	}
	return
}

// applyBodyMutation applies body mutations from the route and also restores original body on retry.
// This utility function handles both creating new mutations and modifying existing ones.
func applyBodyMutation(bodyMutator *bodymutator.BodyMutator, bodyMutation *extprocv3.BodyMutation, originalRequestBodyRaw []byte, onRetry bool, logger *slog.Logger) *extprocv3.BodyMutation {
	if bodyMutator == nil {
		return bodyMutation
	}

	if bodyMutation == nil {
		mutatedBody, mutationErr := bodyMutator.Mutate(originalRequestBodyRaw, onRetry)
		if mutationErr != nil {
			logger.Error("failed to apply body mutation on original request body", "error", mutationErr)
		} else {
			bodyMutation = &extprocv3.BodyMutation{
				Mutation: &extprocv3.BodyMutation_Body{Body: mutatedBody},
			}
		}
	} else if bodyMutation.GetBody() != nil && len(bodyMutation.GetBody()) > 0 {
		mutatedBody, mutationErr := bodyMutator.Mutate(bodyMutation.GetBody(), onRetry)
		if mutationErr != nil {
			logger.Error("failed to apply body mutation", "error", mutationErr)
		} else {
			bodyMutation.Mutation = &extprocv3.BodyMutation_Body{Body: mutatedBody}
		}
	}

	return bodyMutation
}
