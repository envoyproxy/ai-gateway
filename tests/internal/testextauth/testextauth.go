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

	// ExtAuthDynamicMetadataKeyEnvVar and ExtAuthDynamicMetadataValueEnvVar configure a single
	// dynamic-metadata field the server returns on an allowed response. When both are set, the
	// server emits CheckResponse.dynamic_metadata with that key/value, which the ext_authz filter
	// exposes under the envoy.filters.http.ext_authz namespace. If either is unset, no dynamic
	// metadata is emitted (the default).
	ExtAuthDynamicMetadataKeyEnvVar   = "EXT_AUTH_DYNAMIC_METADATA_KEY"
	ExtAuthDynamicMetadataValueEnvVar = "EXT_AUTH_DYNAMIC_METADATA_VALUE"
)
