// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build !envoy

package sdk

import "fmt"

const (
	LogDebugEnabled = true
	LogInfoEnabled  = true
)

func Log(level LogLevel, format string, args ...interface{}) {
	fmt.Printf(fmt.Sprintf("[%s] %s\n", level, format), args...)
}
