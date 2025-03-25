// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

// Package infext provides the shared constructs needed to coordinate between the control plane and the data plane
// in order to implement the Inference Extension API.
package infext

const (
	// OriginalDstHeaderName is the header name that will be used to pass the original destination endpoint in the form of "ip:port".
	OriginalDstHeaderName = "x-ai-eg-original-dst"
	// OriginalDstEnablingHeaderName is the header name that will be used to enable the original destination cluster when set to "true".
	OriginalDstEnablingHeaderName = "x-ai-eg-use-original-dst"
)
