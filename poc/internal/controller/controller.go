package controller

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"
	gwapiv1a2 "sigs.k8s.io/gateway-api/apis/v1alpha2"

	aigv1a1 "github.com/tetratelabs/ai-gateway/api/v1alpha1"
	"github.com/tetratelabs/ai-gateway/internal/scheme"
)

const (
	owningLabel = "ai-gateway.envoyproxy.io/owning-llm-route-name"
	managedBy   = "app.kubernetes.io/managed-by"
)

// controller implements reconcile.Reconciler.
type controller struct {
	client        client.Client
	scheme        *runtime.Scheme
	kube          kubernetes.Interface
	extprocImage  string
	rateLimitAddr string
	logLevel      string

	rlChan chan *aigv1a1.LLMRouteList
}

// StartController starts the controller that manages the EnvoyGatewayLLMPolicy.
// TODO: use Options Pattern here?
func StartController(ctx context.Context, logger logr.Logger, logLevel string, rlChan chan *aigv1a1.LLMRouteList,
	monitoringAddr string, config *rest.Config, extprocImage string, rateLimitAddr string,
) (err error) {
	ctrl.SetLogger(logger)

	mgr, err := ctrl.NewManager(config, ctrl.Options{
		Scheme:           scheme.GetScheme(),
		LeaderElection:   true,
		LeaderElectionID: "ai-gateway-controller",
		Metrics: metricsserver.Options{
			BindAddress: monitoringAddr,
		},
	})
	if err != nil {
		return fmt.Errorf("failed to create new manager: %w", err)
	}

	err = aigv1a1.SchemeBuilder.AddToScheme(mgr.GetScheme())
	if err != nil {
		return fmt.Errorf("failed to add scheme to manager: %w", err)
	}

	c := &controller{
		client:        mgr.GetClient(),
		scheme:        mgr.GetScheme(),
		logLevel:      logLevel,
		extprocImage:  extprocImage,
		rlChan:        rlChan,
		rateLimitAddr: rateLimitAddr,
	}

	c.kube, err = kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Set up the controller.
	if err = ctrl.NewControllerManagedBy(mgr).
		For(&aigv1a1.LLMRoute{}).
		Complete(c); err != nil {
		return fmt.Errorf("failed to set up controller: %w", err)
	}
	go func() {
		if err = mgr.Start(ctx); err != nil {
			fmt.Printf("failed to start manager: %v", err)
		}
	}()
	return nil
}

// Reconcile implements reconcile.Reconciler.
//
// This creates or updates the following resources:
//
// # For each LLMRoute:
//   - gateway.networking.k8s.io/v1.HTTPRoute
//   - gateway.envoyproxy.io/v1alpha1.EnvoyExtensionPolicy
//   - apps/v1.Deployment (for the external processor)
//   - core/v1.Service    (for the external processor)
//   - core/v1.Service    (for the external processor)
//
// # For each LLMRoute.TargetRefs:
//   - gateway.envoyproxy.io/v1alpha1.EnvoyFilter
//
// At the end, we need send ratelimit configuration to the server with xds-sotw-config-server
func (c *controller) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var llmRoute aigv1a1.LLMRoute
	if err := c.client.Get(ctx, req.NamespacedName, &llmRoute); err != nil {
		if client.IgnoreNotFound(err) == nil {
			ctrl.Log.Info("Deleting LLMRoute",
				"namespace", req.Namespace, "name", req.Name)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if err := validateLLMRoute(&llmRoute); err != nil {
		return ctrl.Result{}, err
	}

	ctrl.Log.Info("Updating LLMRoute",
		"namespace", req.Namespace, "name", req.Name)

	ownerRef := []metav1.OwnerReference{{
		APIVersion: llmRoute.APIVersion,
		Kind:       llmRoute.Kind,
		Name:       llmRoute.Name,
		UID:        llmRoute.UID,
	}}

	if err := c.reconcileExtprocDeployment(ctx, &llmRoute, ownerRef); err != nil {
		incrementReconcileFailures("Deployment")
		return ctrl.Result{}, fmt.Errorf("failed to reconcile external processor deployment: %w", err)
	}

	if err := c.reconcileEnvoyProxyResources(ctx, &llmRoute, ownerRef); err != nil {
		incrementReconcileFailures("EnvoyProxy")
		return ctrl.Result{}, fmt.Errorf("failed to reconcile EnvoyProxy resource: %w", err)
	}

	ns := llmRoute.Namespace
	if err := c.reconcileHTTPRoute(ctx, llmRoute.Spec.TargetRefs, llmRoute.Name, ns, llmRoute.Spec.Backends, ownerRef); err != nil {
		incrementReconcileFailures("HTTPRoute")
		return ctrl.Result{}, fmt.Errorf("failed to reconcile HTTPRoute: %w", err)
	}

	// Finally creates EnvoyExtensionPolicy to talk to this external processor following the user-provided ExtProc.
	pm := egv1a1.BufferedExtProcBodyProcessingMode
	port := gwapiv1.PortNumber(1063)
	objNs := gwapiv1.Namespace(llmRoute.Namespace)
	extPolicy := &egv1a1.EnvoyExtensionPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:            llmRoute.Name,
			Namespace:       ns,
			OwnerReferences: ownerRef,
			Labels:          map[string]string{owningLabel: llmRoute.Name},
		},
		Spec: egv1a1.EnvoyExtensionPolicySpec{
			PolicyTargetReferences: egv1a1.PolicyTargetReferences{TargetRefs: llmRoute.Spec.TargetRefs},
			ExtProc: append(llmRoute.Spec.ExtProc, egv1a1.ExtProc{
				ProcessingMode: &egv1a1.ExtProcProcessingMode{
					Request:  &egv1a1.ProcessingModeOptions{Body: &pm},
					Response: &egv1a1.ProcessingModeOptions{Body: &pm},
				},
				BackendCluster: egv1a1.BackendCluster{BackendRefs: []egv1a1.BackendRef{{
					BackendObjectReference: gwapiv1.BackendObjectReference{
						Name:      gwapiv1.ObjectName(extProcName(&llmRoute)),
						Namespace: &objNs,
						Port:      &port,
					},
				}}},
			}),
		},
	}

	if err := c.client.Get(ctx, client.ObjectKey{Name: extPolicy.Name, Namespace: ns}, extPolicy); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Resource not found, create it.
			if err := c.client.Create(ctx, extPolicy); err != nil {
				incrementReconcileFailures("EnvoyExtensionPolicy")
				ctrl.Log.Error(err, "failed to create EnvoyExtensionPolicy", "name", extPolicy.Name)
				return ctrl.Result{}, fmt.Errorf("failed to create EnvoyExtensionPolicy %s: %w", extPolicy.Name, err)
			}
		}
	}

	// notify update to the rate limit server configurations
	go func() {
		routeList := &aigv1a1.LLMRouteList{}
		if err := c.client.List(ctx, routeList); err != nil {
			ctrl.Log.Error(err, "failed to list LLMRoute")
			return
		}
		c.rlChan <- routeList
	}()
	return ctrl.Result{}, nil
}

// reconcileExtprocDeployment reconciles the external processor Deployment and Service for a given [aigv1a1.LLMRoute].
func (c *controller) reconcileExtprocDeployment(
	ctx context.Context, route *aigv1a1.LLMRoute, ownerRef []metav1.OwnerReference,
) error {
	name := extProcName(route)
	ns := route.Namespace
	labels := map[string]string{
		"app":       name,
		owningLabel: route.Name,
		managedBy:   "ai-gateway",
	}

	jsoned, err := json.Marshal(route)
	if err != nil {
		return fmt.Errorf("failed to marshal LLMRoute: %w", err)
	}

	base64LLMRoute := base64.StdEncoding.EncodeToString(jsoned)

	var resources corev1.ResourceRequirements
	var replicas *int32
	if extProcConfig := route.Spec.ExtProcConfig; extProcConfig != nil {
		if extProcConfig.Resources != nil {
			resources = *extProcConfig.Resources
		}
		replicas = &extProcConfig.Replicas
	}
	// Create apps/v1.Deployment.
	deployment, err := c.kube.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		if client.IgnoreNotFound(err) == nil {
			deployment = &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{
					Name:            name,
					Namespace:       ns,
					OwnerReferences: ownerRef,
					Labels:          labels,
					Annotations: map[string]string{
						"prometheus.io/scrape": "true",
						"prometheus.io/port":   "9090",
						"prometheus.io/path":   "/metrics",
					},
				},
				Spec: appsv1.DeploymentSpec{
					Replicas: replicas,
					Selector: &metav1.LabelSelector{
						MatchLabels: labels,
					},
					Template: corev1.PodTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels: labels,
						},
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Resources:       resources,
									Name:            name,
									Image:           c.extprocImage,
									ImagePullPolicy: corev1.PullIfNotPresent,
									Ports:           []corev1.ContainerPort{{Name: "grpc", ContainerPort: 1063}},
									ReadinessProbe: &corev1.Probe{
										InitialDelaySeconds: 5,
										PeriodSeconds:       5,
										ProbeHandler: corev1.ProbeHandler{
											GRPC: &corev1.GRPCAction{Port: 1063},
										},
									},
									Args: []string{
										"--configuration", base64LLMRoute,
										"--rateLimitAddr", c.rateLimitAddr,
										"--logLevel", c.logLevel,
									},
								},
							},
						},
					},
				},
			}
			ctrl.Log.Info("Creating Deployment", "name", name, "namespace", ns)
			if _, err = c.kube.AppsV1().Deployments(ns).Create(ctx, deployment, metav1.CreateOptions{}); client.IgnoreAlreadyExists(err) != nil {
				return fmt.Errorf("failed to create Deployment %s.%s: %w", name, ns, err)
			}
		} else {
			return fmt.Errorf("failed to get Deployment %s.%s: %w", name, ns, err)
		}
	} else {
		deployment.Spec.Template.Spec.Containers[0].Args = []string{
			"--configuration", base64LLMRoute,
			"--rateLimitAddr", c.rateLimitAddr,
			"--logLevel", c.logLevel,
		}
		deployment.Spec.Replicas = replicas
		deployment.Spec.Template.Spec.Containers[0].Resources = resources
		ctrl.Log.Info("Updating Deployment", "name", name, "namespace", ns)
		if _, err = c.kube.AppsV1().Deployments(ns).Update(ctx, deployment, metav1.UpdateOptions{}); err != nil {
			return fmt.Errorf("failed to update Deployment %s.%s: %w", name, ns, err)
		}
	}

	// Wait for the Deployment to be ready.
	ctxWait, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
outer:
	for {
		select {
		case <-ctxWait.Done():
			return fmt.Errorf("timed out waiting for Deployment %s.%s to be ready", name, ns)
		case <-ticker.C:
			d, err := c.kube.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
			if err != nil {
				if client.IgnoreNotFound(err) != nil {
					return fmt.Errorf("failed to get Deployment %s.%s while waiting for it to be ready: %w", name, ns, err)
				} else {
					continue
				}
			}
			ctrl.Log.Info("Deployment status", "name", name, "namespace", ns, "ready", d.Status.ReadyReplicas, "replicas", *d.Spec.Replicas)
			// TODO: is this the right condition?
			if d.Status.ReadyReplicas == *d.Spec.Replicas {
				break outer
			}
		}
	}

	// Create core/v1.Service. This is static, so we don't need to update it.
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:            name,
			Namespace:       ns,
			OwnerReferences: ownerRef,
			Labels:          labels,
		},
		Spec: corev1.ServiceSpec{
			Selector: labels,
			Ports: []corev1.ServicePort{
				{
					Name:        "grpc",
					Protocol:    corev1.ProtocolTCP,
					Port:        1063,
					AppProtocol: func(s string) *string { return &s }("grpc"),
				},
			},
		},
	}
	if _, err = c.kube.CoreV1().Services(ns).Create(ctx, service, metav1.CreateOptions{}); client.IgnoreAlreadyExists(err) != nil {
		return fmt.Errorf("failed to create Service %s.%s: %w", name, ns, err)
	}
	return nil
}

// reconcileHTTPRoute creates or updates the gateway.networking.k8s.io/v1.HTTPRoute.
func (c *controller) reconcileHTTPRoute(ctx context.Context,
	targetRefs []gwapiv1a2.LocalPolicyTargetReferenceWithSectionName,
	llmRouteName, llmRouteNamespace string,
	backends []aigv1a1.LLMBackend,
	ownerRef []metav1.OwnerReference,
) error {
	objMeta := metav1.ObjectMeta{
		Name:            fmt.Sprintf("llmroute-%s", llmRouteName),
		Namespace:       llmRouteNamespace,
		OwnerReferences: ownerRef,
		Labels:          map[string]string{owningLabel: llmRouteName},
	}
	// Create gateway.networking.k8s.io/v1.HTTPRoute that references the Backend.
	var httpRouteRules []gwapiv1.HTTPRouteRule
	for i := range backends {
		backend := &backends[i]
		rule := gwapiv1.HTTPRouteRule{
			BackendRefs: []gwapiv1.HTTPBackendRef{
				{
					BackendRef: gwapiv1.BackendRef{BackendObjectReference: backend.BackendRef.BackendObjectReference},
				},
			},
			Matches: []gwapiv1.HTTPRouteMatch{
				{
					Headers: []gwapiv1.HTTPHeaderMatch{{Name: aigv1a1.LLMRoutingHeaderKey, Value: backend.Name()}},
				},
			},
			Filters: []gwapiv1.HTTPRouteFilter{
				{
					Type: gwapiv1.HTTPRouteFilterExtensionRef,
					ExtensionRef: &gwapiv1.LocalObjectReference{
						Name:  gwapiv1.ObjectName(llmRouteName),
						Group: aigv1a1.GroupName,
						Kind:  "LLMRoute",
					},
				},
			},
		}

		if pp := backend.ProviderPolicy; pp != nil && pp.Type == aigv1a1.LLMProviderTypeAPIKey {
			apiKey, err := c.extractAPIKey(ctx, llmRouteNamespace, pp.APIKey)
			if err != nil {
				return fmt.Errorf("failed to extract the API key: %w", err)
			}
			rule.Filters = append(rule.Filters, gwapiv1.HTTPRouteFilter{
				Type: gwapiv1.HTTPRouteFilterRequestHeaderModifier,
				RequestHeaderModifier: &gwapiv1.HTTPHeaderFilter{
					Set: []gwapiv1.HTTPHeader{
						{Name: "Authorization", Value: fmt.Sprintf("Bearer %s", apiKey)},
					},
				},
			})
		}

		httpRouteRules = append(httpRouteRules, rule)
	}

	egNs := gwapiv1.Namespace(llmRouteNamespace)
	parentRefs := make([]gwapiv1.ParentReference, len(targetRefs))
	for i, egRef := range targetRefs {
		egName := egRef.Name
		parentRefs[i] = gwapiv1.ParentReference{
			Name:      egName,
			Namespace: &egNs,
		}
	}
	httpRoute := &gwapiv1.HTTPRoute{ObjectMeta: objMeta, Spec: gwapiv1.HTTPRouteSpec{}}
	if err := c.client.Get(ctx, client.ObjectKey{Name: objMeta.Name, Namespace: llmRouteNamespace}, httpRoute); err != nil {
		if client.IgnoreNotFound(err) == nil {
			// Resource not found, create it.
			httpRoute.Spec.CommonRouteSpec.ParentRefs = parentRefs
			httpRoute.Spec.Rules = httpRouteRules
			if err := c.client.Create(ctx, httpRoute); err != nil {
				ctrl.Log.Error(err, "failed to create HTTPRoute", "name", objMeta.Name)
				return fmt.Errorf("failed to create HTTPRoute %s: %w", objMeta.Name, err)
			}
		} else {
			return fmt.Errorf("failed to get HTTPRoute %s: %w", objMeta.Name, err)
		}
	} else {
		// Update the existing resource.
		httpRoute.Spec.CommonRouteSpec.ParentRefs = parentRefs
		httpRoute.Spec.Rules = httpRouteRules
		if err := c.client.Update(ctx, httpRoute); err != nil {
			ctrl.Log.Error(err, "failed to update HTTPRoute", "name", objMeta.Name)
			return fmt.Errorf("failed to update HTTPRoute %s: %w", objMeta.Name, err)
		}
	}
	return nil
}

func extProcName(route *aigv1a1.LLMRoute) string {
	return fmt.Sprintf("ai-gateway-extproc-%s", route.Name)
}

// validateLLMRoute validates the given LLMRoute. Currently, only validates the JQ expressions.
func validateLLMRoute(route *aigv1a1.LLMRoute) error {
	var errs []string
	backendNames := make(map[string]struct{})
	for i := range route.Spec.Backends {
		be := &route.Spec.Backends[i]

		if ns := be.BackendRef.Namespace; ns != nil && string(*ns) != route.Namespace {
			errs = append(errs, fmt.Sprintf("the referenced Backend's namespace \"%s\" does not match the LLMRoute's namespace \"%s\"",
				string(*ns), route.Namespace))
		}

		name := be.Name()

		// Check the backend name duplication.
		if _, ok := backendNames[name]; ok {
			errs = append(errs, fmt.Sprintf("backend name %q is duplicated", name))
		} else {
			backendNames[name] = struct{}{}
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid LLM Route:\n * %s", strings.Join(errs, "\n * "))
	}
	return nil
}

// extractAPIKey extracts the API key from the [gwapiv1.SecretObjectReference].
func (c *controller) extractAPIKey(ctx context.Context, routeNs string, key *aigv1a1.LLMProviderAPIKey) (string, error) {
	switch key.Type {
	case aigv1a1.LLMProviderAPIKeyTypeSecretRef:
		ref := key.SecretRef

		ns := routeNs
		if ref.Namespace != nil {
			ns = string(*ref.Namespace)
		}

		secret, err := c.kube.CoreV1().Secrets(ns).Get(ctx, string(ref.Name), metav1.GetOptions{})
		if err != nil {
			return "", fmt.Errorf("failed to get secret %s.%s: %w", ref.Name, ns, err)
		}

		apiKey, ok := secret.Data["apiKey"]
		if !ok {
			return "", fmt.Errorf("missing 'apiKey' in secret %s.%s", secret.Name, secret.Namespace)
		}
		return string(apiKey), nil
	case aigv1a1.LLMProviderAPIKeyTypeInline:
		return *key.Inline, nil
	default:
		return "", fmt.Errorf("unsupported API key type %q", key.Type)
	}
}
