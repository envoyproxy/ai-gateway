package sotw

import (
	"context"
	"fmt"
	"time"

	discoveryv3 "github.com/envoyproxy/go-control-plane/envoy/service/discovery/v3"
	cachev3 "github.com/envoyproxy/go-control-plane/pkg/cache/v3"
	cplog "github.com/envoyproxy/go-control-plane/pkg/log"
	serverv3 "github.com/envoyproxy/go-control-plane/pkg/server/v3"
	"github.com/go-logr/logr"
	"google.golang.org/grpc"
	ctrl "sigs.k8s.io/controller-runtime"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/ratelimit/xds"
)

type Server struct {
	grpcServer *grpc.Server
	cache      cachev3.SnapshotCache

	logger logr.Logger
}

func NewServer(gServer *grpc.Server, logger logr.Logger) *Server {
	return &Server{
		grpcServer: gServer,
		cache:      cachev3.NewSnapshotCache(false, cachev3.IDHash{}, cplog.NewDefaultLogger()),
		logger:     logger.WithName("ratelimit-sotw-server"),
	}
}

func (s *Server) Start(ctx context.Context, rlChan chan *aigv1a1.LLMRouteList) {
	// Register xDS Config server.
	discoveryv3.RegisterAggregatedDiscoveryServiceServer(s.grpcServer, serverv3.NewServer(ctx, s.cache, serverv3.CallbackFuncs{}))

	go s.waitForRateLimitUpdate(ctx, rlChan)
}

func (s *Server) waitForRateLimitUpdate(ctx context.Context, ch chan *aigv1a1.LLMRouteList) {
	for {
		select {
		case <-ctx.Done():
			s.logger.Info("context done, stop waiting for ratelimit update")
			return
		case routeList := <-ch:
			ctrl.Log.Info("trigger ratelimit update")

			resources := xds.BuildRateLimitConfigResources(routeList)
			v := time.Now().Unix()
			snapshot, err := cachev3.NewSnapshot(fmt.Sprintf("%d", v), resources)
			if err != nil {
				increaseSnapshotFailures()
				ctrl.Log.Error(err, "failed to create snapshot")
				continue
			}
			if s.cache == nil {
				// this should not happen
				continue
			}

			recordSnapshotVersion(v)
			ctrl.Log.Info("set ratelimit snapshot", "version", v, "node", "llm-ratelimit")
			err = s.cache.SetSnapshot(ctx, "llm-ratelimit", snapshot)
			if err != nil {
				ctrl.Log.Error(err, "failed to set snapshot")
			}
		}
	}
}
