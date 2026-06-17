// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package service

import (
	"context"
	"sync"
	"time"

	commonrlv3 "github.com/envoyproxy/go-control-plane/envoy/extensions/common/ratelimit/v3"
	ratelimitv3 "github.com/envoyproxy/go-control-plane/envoy/service/ratelimit/v3"
	rlsconfv3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
	"github.com/go-logr/logr"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/envoyproxy/ai-gateway/internal/ratelimit/storage"
)

// Service implements the Envoy rate limit gRPC service.
// It receives ShouldRateLimit requests from Envoy data-plane proxies,
// evaluates descriptor trees against the storage backend, and returns
// rate limit decisions.
type Service struct {
	ratelimitv3.UnimplementedRateLimitServiceServer
	store  storage.Store
	logger logr.Logger
	// configMu protects concurrent access to config.
	configMu sync.RWMutex
	// config is the merged rate limit config tree used for decision-making.
	config *rlsconfv3.RateLimitConfig
}

// New creates a new rate limit gRPC service.
func New(store storage.Store, logger logr.Logger) *Service {
	return &Service{store: store, logger: logger}
}

// UpdateConfig updates the rate limit config tree used for decision-making.
func (s *Service) UpdateConfig(config *rlsconfv3.RateLimitConfig) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.config = config
}

// ShouldRateLimit implements the Envoy rate limit service gRPC method.
func (s *Service) ShouldRateLimit(ctx context.Context, req *ratelimitv3.RateLimitRequest) (*ratelimitv3.RateLimitResponse, error) {
	const maxDescriptors = 1000
	if len(req.Descriptors) > maxDescriptors {
		return nil, status.Errorf(codes.InvalidArgument, "too many descriptors: %d (max %d)", len(req.Descriptors), maxDescriptors)
	}

	s.configMu.RLock()
	cfg := s.config
	s.configMu.RUnlock()

	if cfg == nil {
		return &ratelimitv3.RateLimitResponse{
			OverallCode: ratelimitv3.RateLimitResponse_OK,
		}, nil
	}

	var statuses []*ratelimitv3.RateLimitResponse_DescriptorStatus
	overallCode := ratelimitv3.RateLimitResponse_OK

	for _, desc := range req.Descriptors {
		limit := s.findLimit(cfg.Descriptors, desc.Entries)
		if limit == nil {
			statuses = append(statuses, &ratelimitv3.RateLimitResponse_DescriptorStatus{
				Code: ratelimitv3.RateLimitResponse_OK,
			})
			continue
		}

		counter := s.buildCounter(req.Domain, desc.Entries)
		storeLimit := storage.Limit{
			RequestsPerUnit: limit.RequestsPerUnit,
			Unit:            unitFromResponseProto(limit.Unit),
		}

		delta := uint32(1)
		if desc.HitsAddend != nil {
			delta = uint32(desc.HitsAddend.Value) //nolint:gosec // token counts fit in uint32
		}

		newCount, resetAt, err := s.store.Increment(ctx, counter, storeLimit, delta)
		if err != nil {
			s.logger.Error(err, "storage increment failed")
			return nil, status.Error(codes.Internal, "storage unavailable")
		}

		code := ratelimitv3.RateLimitResponse_OK
		if newCount > limit.RequestsPerUnit {
			code = ratelimitv3.RateLimitResponse_OVER_LIMIT
			overallCode = ratelimitv3.RateLimitResponse_OVER_LIMIT
		}

		dur := time.Until(resetAt)
		if dur < 0 {
			dur = 0
		}

		remaining := uint32(0)
		if newCount < limit.RequestsPerUnit {
			remaining = limit.RequestsPerUnit - newCount
		}

		statuses = append(statuses, &ratelimitv3.RateLimitResponse_DescriptorStatus{
			Code:               code,
			CurrentLimit:       limit,
			LimitRemaining:     remaining,
			DurationUntilReset: durationpb.New(dur),
		})
	}

	return &ratelimitv3.RateLimitResponse{
		OverallCode: overallCode,
		Statuses:    statuses,
	}, nil
}

// findLimit walks the descriptor tree from the config to find the first
// RateLimitPolicy that matches the given entries. Entries are matched in
// order against nested descriptors. If a leaf descriptor has RateLimit set,
// that limit is returned. If no exact match is found, nil is returned.
func (s *Service) findLimit(
	descriptors []*rlsconfv3.RateLimitDescriptor,
	entries []*commonrlv3.RateLimitDescriptor_Entry,
) *ratelimitv3.RateLimitResponse_RateLimit {
	if len(entries) == 0 {
		return nil
	}

	entry := entries[0]
	rest := entries[1:]

	for _, desc := range descriptors {
		if desc.Key != entry.Key || desc.Value != entry.Value {
			continue
		}

		if len(rest) == 0 {
			// This descriptor is the last one in the request.
			// Return its RateLimit if set, otherwise check children.
			if desc.RateLimit != nil {
				return convertRateLimitPolicy(desc.RateLimit)
			}
			return nil
		}

		// Continue walking into nested descriptors.
		if limit := s.findLimit(desc.Descriptors, rest); limit != nil {
			return limit
		}
	}
	return nil
}

// buildCounter constructs a storage.Counter from the request domain and descriptor entries.
func (s *Service) buildCounter(domain string, entries []*commonrlv3.RateLimitDescriptor_Entry) storage.Counter {
	descKey := ""
	for i, e := range entries {
		if i > 0 {
			descKey += "/"
		}
		descKey += e.Key + "_" + e.Value
	}
	return storage.Counter{Domain: domain, DescriptorKey: descKey}
}

// convertRateLimitPolicy converts from the config proto's RateLimitPolicy
// to the response proto's RateLimit struct.
func convertRateLimitPolicy(policy *rlsconfv3.RateLimitPolicy) *ratelimitv3.RateLimitResponse_RateLimit {
	if policy == nil {
		return nil
	}
	return &ratelimitv3.RateLimitResponse_RateLimit{
		Name:            policy.Name,
		RequestsPerUnit: policy.RequestsPerUnit,
		Unit:            unitToResponseProto(policy.Unit),
	}
}

// unitFromResponseProto converts from the response proto unit to our storage unit.
func unitFromResponseProto(unit ratelimitv3.RateLimitResponse_RateLimit_Unit) storage.RateLimitUnit {
	switch unit {
	case ratelimitv3.RateLimitResponse_RateLimit_SECOND:
		return storage.RateLimitUnitSecond
	case ratelimitv3.RateLimitResponse_RateLimit_MINUTE:
		return storage.RateLimitUnitMinute
	case ratelimitv3.RateLimitResponse_RateLimit_HOUR:
		return storage.RateLimitUnitHour
	case ratelimitv3.RateLimitResponse_RateLimit_DAY:
		return storage.RateLimitUnitDay
	default:
		return storage.RateLimitUnitSecond
	}
}

// unitToResponseProto converts from the config proto unit to the response proto unit.
func unitToResponseProto(unit rlsconfv3.RateLimitUnit) ratelimitv3.RateLimitResponse_RateLimit_Unit {
	switch unit {
	case rlsconfv3.RateLimitUnit_SECOND:
		return ratelimitv3.RateLimitResponse_RateLimit_SECOND
	case rlsconfv3.RateLimitUnit_MINUTE:
		return ratelimitv3.RateLimitResponse_RateLimit_MINUTE
	case rlsconfv3.RateLimitUnit_HOUR:
		return ratelimitv3.RateLimitResponse_RateLimit_HOUR
	case rlsconfv3.RateLimitUnit_DAY:
		return ratelimitv3.RateLimitResponse_RateLimit_DAY
	default:
		return ratelimitv3.RateLimitResponse_RateLimit_SECOND
	}
}
