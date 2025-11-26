// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package testextauth

const (
	// ExtAuthAccessControlHeader is the header used to send the access control value to
	// configure the response that will be returned by the ext-authz filter.
	ExtAuthAccessControlHeader = "x-access-control"

	// ExtAuthAllowedValueEnvVar is the name of the environment variable that will configure
	// the allowed value for the access control header. If not set, all requests are allowed.
	ExtAuthAllowedValueEnvVar = "EXT_AUTH_ALLOWED_VALUE"
)
