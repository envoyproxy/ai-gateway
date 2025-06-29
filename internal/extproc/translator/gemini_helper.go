// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package translator

import (
	"fmt"
	"strconv"

	"github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	"github.com/envoyproxy/go-control-plane/envoy/service/ext_proc/v3"
)

const (
	GCPModelPublisherGoogle    = "google"
	GCPModelPublisherAnthropic = "anthropic"
	GCPMethodGenerateContent   = "generateContent"
	HTTPHeaderKeyContentLength = "content-length"
)

func buildGCPModelPathSuffix(publisher, model, gcpMethod string) string {
	pathSuffix := fmt.Sprintf("publishers/%s/models/%s:%s", publisher, model, gcpMethod)
	return pathSuffix
}

// buildGCPRequestMutations creates header and body mutations for GCP requests
// It sets the ":path" header, the "content-length" header and the request body.
func buildGCPRequestMutations(path string, reqBody []byte) (*ext_procv3.HeaderMutation, *ext_procv3.BodyMutation) {
	var bodyMutation *ext_procv3.BodyMutation

	// Create header mutation
	headerMutation := &ext_procv3.HeaderMutation{
		SetHeaders: []*corev3.HeaderValueOption{
			{
				Header: &corev3.HeaderValue{
					Key:      ":path",
					RawValue: []byte(path),
				},
			},
		},
	}

	// If the request body is not empty, we set the content-length header and create a body mutation
	if len(reqBody) != 0 {
		// Set the "content-length" header
		headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
			Header: &corev3.HeaderValue{
				Key:      HTTPHeaderKeyContentLength,
				RawValue: []byte(strconv.Itoa(len(reqBody))),
			},
		})

		// Create body mutation
		bodyMutation = &ext_procv3.BodyMutation{
			Mutation: &ext_procv3.BodyMutation_Body{Body: reqBody},
		}

	}

	return headerMutation, bodyMutation
}
