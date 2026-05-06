// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package main

import (
	_ "github.com/envoyproxy/envoy/source/extensions/dynamic_modules/sdk/go/abi"
	_ "github.com/tetratelabs/built-on-envoy/extensions/composer/token-exchange/embedded"
)

func main() {} // main is required to build as a C shared library.
