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

func newClients(config *rest.Config) (kubeClient client.Client, kube kubernetes.Interface, err error) {
	// TODO: cache options, especially HTTPRoutes managed by the AI Gateway.
	kubeClient, err = client.New(config, client.Options{Scheme: scheme})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create new client: %w", err)
	}

	kube, err = kubernetes.NewForConfig(config)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}
	return kubeClient, kube, nil
}

// StartControllers starts the controllers for the AI Gateway.
// This blocks until the manager is stopped.
//
// Note: this is tested with envtest, hence the test exists outside of this package. See /tests/controller_test.go.
func StartControllers(ctx context.Context, config *rest.Config, logger logr.Logger, logLevel string, extProcImage string, leaderElection bool) error {
	opt := ctrl.Options{
		Scheme:           scheme,
		LeaderElection:   leaderElection,
		LeaderElectionID: "envoy-ai-gateway-controller",
	}

	mgr, err := ctrl.NewManager(config, opt)
	if err != nil {
		return fmt.Errorf("failed to create new controller manager: %w", err)
	}

	clientForRouteC, kubeForRouteC, err := newClients(config)
	if err != nil {
		return fmt.Errorf("failed to create new clients: %w", err)
	}

	sinkChan := make(chan ConfigSinkEvent, 100)
	routeC := NewLLMRouteController(clientForRouteC, kubeForRouteC, logger, logLevel, extProcImage, sinkChan)
	if err = ctrl.NewControllerManagedBy(mgr).
		For(&aigv1a1.LLMRoute{}).
		Complete(routeC); err != nil {
		return fmt.Errorf("failed to create controller for LLMRoute: %w", err)
	}

	clientForBackendC, kubeForBackendC, err := newClients(config)
	if err != nil {
		return fmt.Errorf("failed to create new clients: %w", err)
	}

	backendC := NewLLMBackendController(clientForBackendC, kubeForBackendC, logger, sinkChan)
	if err = ctrl.NewControllerManagedBy(mgr).
		For(&aigv1a1.LLMBackend{}).
		Complete(backendC); err != nil {
		return fmt.Errorf("failed to create controller for LLMBackend: %w", err)
	}

	clientForConfigSink, kubeForConfigSink, err := newClients(config)
	if err != nil {
		return fmt.Errorf("failed to create new clients: %w", err)
	}

	sink := newConfigSink(clientForConfigSink, kubeForConfigSink, logger, sinkChan)

	if leaderElection {
		logger.Info("Waiting to become the leader")
		<-mgr.Elected()
		logger.Info("Became the leader")
	}

	// Before starting the manager, initialize the config sink to sync all LLMBackend and LLMRoute objects in the cluster.
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
