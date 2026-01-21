// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build darwin && cgo

package sdk

// #cgo LDFLAGS: -Wl,-undefined,dynamic_lookup
import "C"
