package controller

import (
	"context"
	"fmt"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/client-go/kubernetes"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1b1 "sigs.k8s.io/gateway-api/apis/v1beta1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

func init() { MustInitializeScheme(scheme) }

// scheme contains the necessary schemas for the AI Gateway.
var scheme = runtime.NewScheme()

// MustInitializeScheme initializes the scheme with the necessary schemas for the AI Gateway.
// This is exported for the testing purposes.
func MustInitializeScheme(scheme *runtime.Scheme) {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(aigv1a1.AddToScheme(scheme))
	utilruntime.Must(apiextensionsv1.AddToScheme(scheme))
	utilruntime.Must(egv1a1.AddToScheme(scheme))
	utilruntime.Must(gwapiv1.Install(scheme))
	utilruntime.Must(gwapiv1b1.Install(scheme))
}

// Options defines the program configurable options that may be passed on the command line.
type Options struct {
	ExtProcImage         string
	EnableLeaderElection bool
	ZapOptions           zap.Options
}

// StartControllers starts the controllers for the AI Gateway.
// This blocks until the manager is stopped.
//
// Note: this is tested with envtest, hence the test exists outside of this package. See /tests/controller_test.go.
func StartControllers(ctx context.Context, config *rest.Config, logger logr.Logger, options Options) error {
	opt := ctrl.Options{
		Scheme:           scheme,
		LeaderElection:   options.EnableLeaderElection,
		LeaderElectionID: "envoy-ai-gateway-controller",
	}

	mgr, err := ctrl.NewManager(config, opt)
	if err != nil {
		return fmt.Errorf("failed to create new controller manager: %w", err)
	}

	c := mgr.GetClient()
	indexer := mgr.GetFieldIndexer()
	if err = applyIndexing(indexer); err != nil {
		return fmt.Errorf("failed to apply indexing: %w", err)
	}

	sinkChan := make(chan ConfigSinkEvent, 100)
	routeC := NewAIGatewayRouteController(c, kubernetes.NewForConfigOrDie(config), logger, sinkChan)
	if err = ctrl.NewControllerManagedBy(mgr).
		For(&aigv1a1.AIGatewayRoute{}).
		Complete(routeC); err != nil {
		return fmt.Errorf("failed to create controller for AIGatewayRoute: %w", err)
	}

	backendC := NewAIServiceBackendController(c, kubernetes.NewForConfigOrDie(config), logger, sinkChan)
	if err = ctrl.NewControllerManagedBy(mgr).
		For(&aigv1a1.AIServiceBackend{}).
		Complete(backendC); err != nil {
		return fmt.Errorf("failed to create controller for AIServiceBackend: %w", err)
	}

	sink := newConfigSink(c, kubernetes.NewForConfigOrDie(config), logger, sinkChan, options.ExtProcImage)

	// Before starting the manager, initialize the config sink to sync all AIServiceBackend and AIGatewayRoute objects in the cluster.
	logger.Info("Initializing config sink")
	if err = sink.init(ctx); err != nil {
		return fmt.Errorf("failed to initialize config sink: %w", err)
	}

	logger.Info("Starting controller manager")
	if err = mgr.Start(ctx); err != nil { // This blocks until the manager is stopped.
		return fmt.Errorf("failed to start controller manager: %w", err)
	}
	return nil
}

func applyIndexing(indexer client.FieldIndexer) error {
	err := indexer.IndexField(context.Background(), &aigv1a1.AIGatewayRoute{},
		k8sClientIndexBackendToReferencingAIGatewayRoute, aiGatewayRouteIndexFunc)
	if err != nil {
		return fmt.Errorf("failed to index field for AIGatewayRoute: %w", err)
	}
	return nil
}
