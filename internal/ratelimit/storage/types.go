// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package storage

import "time"

// Counter represents a single rate limit bucket identified by a compound key.
type Counter struct {
	Domain        string // e.g. "ai-gateway-quota"
	DescriptorKey string // the full descriptor path, e.g. "backend_name_default/openai/model_name_override_1_gpt-4/rule-0-match-0"
}

// Key returns a deterministic string key for use in key-value stores.
func (c Counter) Key() string {
	return c.Domain + ":" + c.DescriptorKey
}

// Limit defines the rate limit threshold.
type Limit struct {
	RequestsPerUnit uint32
	Unit            RateLimitUnit
}

// RateLimitUnit mirrors envoy.config.ratelimit.v3.RateLimitUnit.
type RateLimitUnit int32

const (
	RateLimitUnitSecond RateLimitUnit = 0
	RateLimitUnitMinute RateLimitUnit = 1
	RateLimitUnitHour   RateLimitUnit = 2
	RateLimitUnitDay    RateLimitUnit = 3
)

// UnitDuration returns the time.Duration for the unit.
func (u RateLimitUnit) UnitDuration() time.Duration {
	switch u {
	case RateLimitUnitSecond:
		return time.Second
	case RateLimitUnitMinute:
		return time.Minute
	case RateLimitUnitHour:
		return time.Hour
	case RateLimitUnitDay:
		return 24 * time.Hour
	default:
		return time.Second
	}
}
