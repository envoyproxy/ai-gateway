// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package runner

import (
	"context"
	"fmt"
	"math"
	"net"
	"strconv"
	"sync"

	discoveryv3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	cachetype "github.com/envoyproxy/go-control-plane/pkg/cache/types"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	resourcev3 "github.com/envoyproxy/go-control-plane/pkg/resource/v3"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	rlsconfv3 "github.com/envoyproxy/go-control-plane/ratelimit/config/ratelimit/v3"
	"github.com/go-logr/logr"
	"google.golang.org/grpc"
)

const (
	// NodeID is the xDS node identifier that the rate limit service uses
	// when connecting to this xDS server.
	NodeID = "envoy-ai-gateway-ratelimit"

	// DefaultPort is the default listening port for the rate limit xDS config server.
	DefaultPort = 18002
)

// ConfigObserver is called with the merged rate limit config whenever configs are updated.
type ConfigObserver func(config *rlsconfv3.RateLimitConfig)

// Runner manages the xDS gRPC server that serves RateLimitConfig resources
// to the rate limit service. It is modeled after Envoy Gateway's
// internal/globalratelimit/runner/runner.go.
type Runner struct {
	logger          logr.Logger
	grpcServer      *grpc.Server
	cache           cachev3.SnapshotCache
	snapshotVersion int64
	port            int
	mu              sync.Mutex
	configObserver  ConfigObserver
}

// New creates a new rate limit xDS config server runner.
func New(logger logr.Logger, port int) *Runner {
	if port == 0 {
		port = DefaultPort
	}
	return &Runner{
		logger: logger.WithName("ratelimit-xds-runner"),
		port:   port,
	}
}

// SetConfigObserver sets a callback that receives the merged rate limit config
// whenever UpdateConfigs is called. This is used to keep the in-process rate
// limit service in sync with the xDS config served to external clients.
func (r *Runner) SetConfigObserver(observer ConfigObserver) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.configObserver = observer
}

// Start starts the xDS gRPC server. It blocks until ctx is cancelled.
func (r *Runner) Start(ctx context.Context) error {
	r.mu.Lock()
	r.cache = cachev3.NewSnapshotCache(false, cachev3.IDHash{}, nil)
	r.grpcServer = grpc.NewServer()
	r.mu.Unlock()

	xdsServer := serverv3.NewServer(ctx, r.cache, serverv3.CallbackFuncs{})
	discoveryv3.RegisterAggregatedDiscoveryServiceServer(r.grpcServer, xdsServer)

	addr := net.JoinHostPort("0.0.0.0", strconv.Itoa(r.port))
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	go func() {
		<-ctx.Done()
		r.logger.Info("shutting down rate limit xDS config server")
		r.grpcServer.GracefulStop()
	}()

	r.logger.Info("starting rate limit xDS config server", "address", addr)
	if err := r.grpcServer.Serve(lis); err != nil {
		return fmt.Errorf("failed to serve rate limit xDS config: %w", err)
	}
	return nil
}

// UpdateConfigs updates the xDS snapshot cache with the provided rate limit
// configurations. This is called by the QuotaPolicy controller whenever
// a QuotaPolicy is reconciled. The merged config is also pushed to the
// in-process rate limit service via the ConfigObserver.
func (r *Runner) UpdateConfigs(ctx context.Context, configs []*rlsconfv3.RateLimitConfig) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.cache == nil {
		return fmt.Errorf("snapshot cache not initialized")
	}

	var resources []cachetype.Resource
	for _, cfg := range configs {
		if cfg != nil {
			resources = append(resources, cfg)
		}
	}

	xdsResources := map[resourcev3.Type][]cachetype.Resource{
		resourcev3.RateLimitConfigType: resources,
	}

	// Increment snapshot version.
	if r.snapshotVersion == math.MaxInt64 {
		r.snapshotVersion = 0
	}
	r.snapshotVersion++

	snapshot, err := cachev3.NewSnapshot(fmt.Sprintf("%d", r.snapshotVersion), xdsResources)
	if err != nil {
		return fmt.Errorf("failed to create xDS snapshot: %w", err)
	}

	if err := r.cache.SetSnapshot(ctx, NodeID, snapshot); err != nil {
		return fmt.Errorf("failed to set xDS snapshot: %w", err)
	}

	r.logger.Info("updated rate limit xDS snapshot",
		"version", r.snapshotVersion,
		"numConfigs", len(resources),
	)

	// Notify the in-process rate limit service of the merged config.
	if r.configObserver != nil {
		merged := mergeConfigs(configs)
		r.configObserver(merged)
	}

	return nil
}

// mergeConfigs merges multiple RateLimitConfigs into a single one by
// concatenating all descriptors. The merged config uses the first config's
// domain and name.
func mergeConfigs(configs []*rlsconfv3.RateLimitConfig) *rlsconfv3.RateLimitConfig {
	var result *rlsconfv3.RateLimitConfig
	for _, cfg := range configs {
		if cfg == nil {
			continue
		}
		if result == nil {
			result = &rlsconfv3.RateLimitConfig{
				Name:   cfg.Name,
				Domain: cfg.Domain,
			}
		}
		result.Descriptors = append(result.Descriptors, cfg.Descriptors...)
	}
	return result
}
