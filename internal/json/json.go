// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package json

import (
	"testing"

	sonicjson "github.com/bytedance/sonic" // nolint: depguard
)

var (
	Unmarshal     = sonicjson.ConfigDefault.Unmarshal
	Marshal       = sonicjson.ConfigDefault.Marshal
	NewEncoder    = sonicjson.ConfigDefault.NewEncoder
	NewDecoder    = sonicjson.ConfigDefault.NewDecoder
	Valid         = sonicjson.ConfigDefault.Valid
	MarshalIndent = sonicjson.ConfigDefault.MarshalIndent
)

type RawMessage = sonicjson.NoCopyRawMessage

func init() {
	if testing.Testing() {
		config := sonicjson.ConfigStd
		Unmarshal = config.Unmarshal
		Marshal = config.Marshal
		NewEncoder = config.NewEncoder
		NewDecoder = config.NewDecoder
	}
}
