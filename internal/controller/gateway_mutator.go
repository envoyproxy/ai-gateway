// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// gatewayMutator implements [admission.CustomDefaulter].
type gatewayMutator struct {
	codec  serializer.CodecFactory
	c      client.Client
	kube   kubernetes.Interface
	logger logr.Logger

	extProcImage                   string
	extProcImagePullPolicy         corev1.PullPolicy
	extProcLogLevel                string
	udsPath                        string
	metricsRequestHeaderAttributes string
	spanRequestHeaderAttributes    string
	rootPrefix                     string
	endpointPrefixes               string
	extProcExtraEnvVars            []corev1.EnvVar
	extProcImagePullSecrets        []corev1.LocalObjectReference
	extProcMaxRecvMsgSize          int

	// mcpSessionEncryptionSeed is the seed used to derive the encryption key for MCP session data.
	mcpSessionEncryptionSeed string
	// mcpSessionEncryptionIterations is the number of iterations to use for PBKDF2 key derivation for MCP session data.
	mcpSessionEncryptionIterations int
	// mcpFallbackSessionEncryptionSeed is the optional fallback seed used for MCP session key rotation.
	mcpFallbackSessionEncryptionSeed string
	// mcpFallbackSessionEncryptionIterations is the number of iterations used in the fallback PBKDF2 key derivation for MCP session encryption.
	mcpFallbackSessionEncryptionIterations int

	// Whether to run the extProc container as a sidecar (true) as a normal container (false).
	// This is essentially a workaround for old k8s versions, and we can remove this in the future.
	extProcAsSideCar bool
}

func newGatewayMutator(c client.Client, kube kubernetes.Interface, logger logr.Logger,
	extProcImage string, extProcImagePullPolicy corev1.PullPolicy, extProcLogLevel,
	udsPath, metricsRequestHeaderAttributes, spanRequestHeaderAttributes, rootPrefix, endpointPrefixes, extProcExtraEnvVars, extProcImagePullSecrets string, extProcMaxRecvMsgSize int,
	extProcAsSideCar bool,
	mcpSessionEncryptionSeed string, mcpSessionEncryptionIterations int, mcpFallbackSessionEncryptionSeed string, mcpFallbackSessionEncryptionIterations int,
) *gatewayMutator {
	var parsedEnvVars []corev1.EnvVar
	if extProcExtraEnvVars != "" {
		var err error
		parsedEnvVars, err = ParseExtraEnvVars(extProcExtraEnvVars)
		if err != nil {
			logger.Error(err, "failed to parse extProc extra env vars, skipping",
				"envVars", extProcExtraEnvVars)
		}
	}

	var parsedImagePullSecrets []corev1.LocalObjectReference
	if extProcImagePullSecrets != "" {
		var err error
		parsedImagePullSecrets, err = ParseImagePullSecrets(extProcImagePullSecrets)
		if err != nil {
			logger.Error(err, "failed to parse extProc image pull secrets, skipping",
				"imagePullSecrets", extProcImagePullSecrets)
		}
	}

	return &gatewayMutator{
		c: c, codec: serializer.NewCodecFactory(Scheme),
		kube:                                   kube,
		extProcImage:                           extProcImage,
		extProcImagePullPolicy:                 extProcImagePullPolicy,
		extProcLogLevel:                        extProcLogLevel,
		logger:                                 logger,
		udsPath:                                udsPath,
		metricsRequestHeaderAttributes:         metricsRequestHeaderAttributes,
		spanRequestHeaderAttributes:            spanRequestHeaderAttributes,
		rootPrefix:                             rootPrefix,
		endpointPrefixes:                       endpointPrefixes,
		extProcExtraEnvVars:                    parsedEnvVars,
		extProcImagePullSecrets:                parsedImagePullSecrets,
		extProcMaxRecvMsgSize:                  extProcMaxRecvMsgSize,
		extProcAsSideCar:                       extProcAsSideCar,
		mcpSessionEncryptionSeed:               mcpSessionEncryptionSeed,
		mcpSessionEncryptionIterations:         mcpSessionEncryptionIterations,
		mcpFallbackSessionEncryptionSeed:       mcpFallbackSessionEncryptionSeed,
		mcpFallbackSessionEncryptionIterations: mcpFallbackSessionEncryptionIterations,
	}
}

// Default implements [admission.CustomDefaulter].
func (g *gatewayMutator) Default(ctx context.Context, obj runtime.Object) error {
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		panic(fmt.Sprintf("BUG: unexpected object type %T, expected *corev1.Pod", obj))
	}
	gatewayName := pod.Labels[egOwningGatewayNameLabel]
	gatewayNamespace := pod.Labels[egOwningGatewayNamespaceLabel]
	g.logger.Info("mutating gateway pod",
		"pod_name", pod.Name, "pod_namespace", pod.Namespace,
		"gateway_name", gatewayName, "gateway_namespace", gatewayNamespace,
	)
	if err := g.mutatePod(ctx, pod, gatewayName, gatewayNamespace); err != nil {
		g.logger.Error(err, "failed to mutate deployment", "name", pod.Name, "namespace", pod.Namespace)
		return err
	}
	return nil
}

// buildExtProcArgs builds all command line arguments for the extproc container.
func (g *gatewayMutator) buildExtProcArgs(filterConfigFullPath string, extProcAdminPort int, needMCP bool, logLevel string) []string {
	args := []string{
		"-configPath", filterConfigFullPath,
		"-logLevel", logLevel,
		"-extProcAddr", "unix://" + g.udsPath,
		"-adminPort", fmt.Sprintf("%d", extProcAdminPort),
		"-rootPrefix", g.rootPrefix,
		"-maxRecvMsgSize", fmt.Sprintf("%d", g.extProcMaxRecvMsgSize),
	}
	if needMCP {
		args = append(args,
			"-mcpAddr", ":"+strconv.Itoa(internalapi.MCPProxyPort),
			"-mcpSessionEncryptionSeed", g.mcpSessionEncryptionSeed,
			"-mcpSessionEncryptionIterations", strconv.Itoa(g.mcpSessionEncryptionIterations),
		)
		if g.mcpFallbackSessionEncryptionSeed != "" {
			args = append(args,
				"-mcpFallbackSessionEncryptionSeed", g.mcpFallbackSessionEncryptionSeed,
				"-mcpFallbackSessionEncryptionIterations", strconv.Itoa(g.mcpFallbackSessionEncryptionIterations),
			)
		}
	}

	// Add metrics header label mapping if configured.
	if g.metricsRequestHeaderAttributes != "" {
		args = append(args, "-metricsRequestHeaderAttributes", g.metricsRequestHeaderAttributes)
	}

	// Add tracing header attribute mapping if configured.
	if g.spanRequestHeaderAttributes != "" {
		args = append(args, "-spanRequestHeaderAttributes", g.spanRequestHeaderAttributes)
	}

	// Add endpoint prefixes mapping if configured.
	if g.endpointPrefixes != "" {
		args = append(args, "-endpointPrefixes", g.endpointPrefixes)
	}

	return args
}

const (
	mutationNamePrefix   = "ai-gateway-"
	extProcContainerName = mutationNamePrefix + "extproc"
)

// ParseExtraEnvVars parses semicolon-separated key=value pairs into a list of
// environment variables. The input delimiter is a semicolon (';') to allow
// values to contain commas without escaping.
// Example: "OTEL_SERVICE_NAME=ai-gateway;OTEL_TRACES_EXPORTER=otlp".
func ParseExtraEnvVars(s string) ([]corev1.EnvVar, error) {
	if s == "" {
		return nil, nil
	}

	pairs := strings.Split(s, ";")
	result := make([]corev1.EnvVar, 0, len(pairs))
	for i, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue // Skip empty pairs from trailing semicolons.
		}

		key, value, found := strings.Cut(pair, "=")
		if !found {
			return nil, fmt.Errorf("invalid env var pair at position %d: %q (expected format: KEY=value)", i+1, pair)
		}

		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("empty env var name at position %d: %q", i+1, pair)
		}
		result = append(result, corev1.EnvVar{Name: key, Value: value})
	}

	if len(result) == 0 {
		return nil, nil
	}

	return result, nil
}

// ParseImagePullSecrets parses semicolon-separated secret names into a list of
// LocalObjectReference objects for image pull secrets.
// Example: "my-registry-secret;another-secret".
func ParseImagePullSecrets(s string) ([]corev1.LocalObjectReference, error) {
	if s == "" {
		return nil, nil
	}

	names := strings.Split(s, ";")
	result := make([]corev1.LocalObjectReference, 0, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" {
			continue // Skip empty names from trailing semicolons.
		}
		result = append(result, corev1.LocalObjectReference{Name: name})
	}

	if len(result) == 0 {
		return nil, nil
	}

	return result, nil
}

func (g *gatewayMutator) mutatePod(ctx context.Context, pod *corev1.Pod, gatewayName, gatewayNamespace string) error {
	var routes aigv1a1.AIGatewayRouteList
	err := g.c.List(ctx, &routes, client.MatchingFields{
		k8sClientIndexAIGatewayRouteToAttachedGateway: fmt.Sprintf("%s.%s", gatewayName, gatewayNamespace),
	})
	if err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}

	var mcpRoutes aigv1a1.MCPRouteList
	err = g.c.List(ctx, &mcpRoutes, client.MatchingFields{
		k8sClientIndexMCPRouteToAttachedGateway: fmt.Sprintf("%s.%s", gatewayName, gatewayNamespace),
	})
	if err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}
	if len(routes.Items) == 0 && len(mcpRoutes.Items) == 0 {
		g.logger.Info("no AIGatewayRoutes or MCPRoutes found for gateway", "name", gatewayName, "namespace", gatewayNamespace)
		return nil
	}
	g.logger.Info("found routes for gateway", "aigatewayroute_count", len(routes.Items), "mcpgatewayroute_count", len(mcpRoutes.Items))

	podspec := &pod.Spec

	// Check if the config secret is already created. If not, let's skip the mutation for this pod to avoid blocking the Envoy pod creation.
	// The config secret will be eventually created by the controller, and that will trigger the mutation for new pods since the Gateway controller
	// will update the pod annotation in the deployment/daemonset template once it creates the config secret.
	_, err = g.kube.CoreV1().Secrets(pod.Namespace).Get(ctx,
		FilterConfigSecretPerGatewayName(gatewayName, gatewayNamespace), metav1.GetOptions{})
	if err != nil && apierrors.IsNotFound(err) {
		g.logger.Info("filter config secret not found, skipping mutation",
			"gateway_name", gatewayName, "gateway_namespace", gatewayNamespace)
		return nil
	} else if err != nil {
		return fmt.Errorf("failed to get filter config secret: %w", err)
	}

	gatewayConfig := g.fetchGatewayConfig(ctx, gatewayName, gatewayNamespace)
	var (
		extProcSpec       *aigv1a1.GatewayConfigExtProc
		kubernetesExtProc *egv1a1.KubernetesContainerSpec
		extProcLogLevel   *string
	)
	if gatewayConfig != nil {
		extProcSpec = gatewayConfig.Spec.ExtProc
		if extProcSpec != nil {
			extProcLogLevel = extProcSpec.LogLevel
			kubernetesExtProc = extProcSpec.Kubernetes
		}
	}

	// Now we construct the AI Gateway managed containers and volumes.
	filterConfigSecretName := FilterConfigSecretPerGatewayName(gatewayName, gatewayNamespace)
	filterConfigVolumeName := mutationNamePrefix + filterConfigSecretName
	const extProcUDSVolumeName = mutationNamePrefix + "extproc-uds"
	podspec.Volumes = append(podspec.Volumes,
		corev1.Volume{
			Name: filterConfigVolumeName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: filterConfigSecretName},
			},
		},
		corev1.Volume{
			Name: extProcUDSVolumeName,
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
	)

	// Add imagePullSecrets for extProc if configured
	if len(g.extProcImagePullSecrets) > 0 {
		podspec.ImagePullSecrets = append(podspec.ImagePullSecrets, g.extProcImagePullSecrets...)
	}

	// TODO: remove after the next release v0.5.
	var resources corev1.ResourceRequirements
	for i := range routes.Items {
		fc := routes.Items[i].Spec.FilterConfig
		if fc != nil && fc.ExternalProcessor != nil && fc.ExternalProcessor.Resources != nil {
			resources = *fc.ExternalProcessor.Resources
		}
	}

	// GatewayConfig resources override route-scoped values when present.
	if kubernetesExtProc != nil && kubernetesExtProc.Resources != nil {
		resources = *kubernetesExtProc.Resources
		g.logger.Info("using resources from GatewayConfig",
			"gateway_name", gatewayName, "gatewayconfig_name", gatewayConfig.Name)
	}

	// Merge env vars with GatewayConfig overriding global.
	envVars := g.mergeEnvVars(gatewayConfig)
	image := g.resolveExtProcImage(extProcSpec)
	logLevel := g.resolveExtProcLogLevel(extProcLogLevel)

	const (
		extProcAdminPort      = 1064
		filterConfigMountPath = "/etc/filter-config"
		filterConfigFullPath  = filterConfigMountPath + "/" + FilterConfigKeyInSecret
	)
	udsMountPath := filepath.Dir(g.udsPath)
	securityContext := &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
		Privileged:   ptr.To(false),
		RunAsGroup:   ptr.To(int64(65532)),
		RunAsNonRoot: ptr.To(true),
		RunAsUser:    ptr.To(int64(65532)),
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
	if kubernetesExtProc != nil && kubernetesExtProc.SecurityContext != nil {
		securityContext = kubernetesExtProc.SecurityContext
	}

	container := corev1.Container{
		Name:            extProcContainerName,
		Image:           image,
		ImagePullPolicy: g.extProcImagePullPolicy,
		Ports: []corev1.ContainerPort{
			{Name: "aigw-admin", ContainerPort: extProcAdminPort},
		},
		Args: g.buildExtProcArgs(filterConfigFullPath, extProcAdminPort, len(mcpRoutes.Items) > 0, logLevel),
		Env:  envVars,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      extProcUDSVolumeName,
				MountPath: udsMountPath,
				ReadOnly:  false,
			},
			{
				Name:      filterConfigVolumeName,
				MountPath: filterConfigMountPath,
				ReadOnly:  true,
			},
		},
		SecurityContext: securityContext,
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Port:   intstr.FromInt32(extProcAdminPort),
					Path:   "/health",
					Scheme: corev1.URISchemeHTTP,
				},
			},
			InitialDelaySeconds: 2,
			TimeoutSeconds:      5,
			PeriodSeconds:       10,
			SuccessThreshold:    1,
			FailureThreshold:    1,
		},
		Resources: resources,
	}

	if kubernetesExtProc != nil && len(kubernetesExtProc.VolumeMounts) > 0 {
		container.VolumeMounts = append(container.VolumeMounts, kubernetesExtProc.VolumeMounts...)
	}

	if g.extProcAsSideCar {
		// When running as a sidecar, we want to ensure the extProc container is shutdown last after Envoy is shutdown.
		container.RestartPolicy = ptr.To(corev1.ContainerRestartPolicyAlways)
		podspec.InitContainers = append(podspec.InitContainers, container)
	} else {
		podspec.Containers = append(podspec.Containers, container)
	}

	// Lastly, we need to mount the Envoy container with the extproc socket.
	for i := range podspec.Containers {
		c := &podspec.Containers[i]
		if c.Name == "envoy" {
			c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
				Name:      extProcUDSVolumeName,
				MountPath: udsMountPath,
				ReadOnly:  false,
			})
		}
	}
	return nil
}

// fetchGatewayConfig returns the referenced GatewayConfig (if present).
func (g *gatewayMutator) fetchGatewayConfig(ctx context.Context, gatewayName, gatewayNamespace string) *aigv1a1.GatewayConfig {
	// Fetch the Gateway object.
	var gateway gwapiv1.Gateway
	if err := g.c.Get(ctx, client.ObjectKey{Name: gatewayName, Namespace: gatewayNamespace}, &gateway); err != nil {
		if apierrors.IsNotFound(err) {
			g.logger.Info("Gateway not found, using global default configuration",
				"gateway_name", gatewayName, "gateway_namespace", gatewayNamespace)
		} else {
			g.logger.Error(err, "failed to get Gateway, using global default configuration",
				"gateway_name", gatewayName, "gateway_namespace", gatewayNamespace)
		}
		return nil
	}

	configName, ok := gateway.Annotations[GatewayConfigAnnotationKey]
	if !ok || configName == "" {
		return nil
	}

	// Fetch the GatewayConfig (must be in same namespace as Gateway).
	var gatewayConfig aigv1a1.GatewayConfig
	if err := g.c.Get(ctx, client.ObjectKey{Name: configName, Namespace: gatewayNamespace}, &gatewayConfig); err != nil {
		if apierrors.IsNotFound(err) {
			g.logger.Info("GatewayConfig referenced by Gateway not found, using global defaults",
				"gateway_name", gatewayName, "gatewayconfig_name", configName)
		} else {
			g.logger.Error(err, "failed to get GatewayConfig, using global defaults",
				"gateway_name", gatewayName, "gatewayconfig_name", configName)
		}
		return nil
	}

	g.logger.Info("found GatewayConfig for Gateway",
		"gateway_name", gatewayName, "gatewayconfig_name", configName)
	return &gatewayConfig
}

// mergeEnvVars merges env vars; GatewayConfig overrides global while preserving order.
func (g *gatewayMutator) mergeEnvVars(gatewayConfig *aigv1a1.GatewayConfig) []corev1.EnvVar {
	result := make([]corev1.EnvVar, 0, len(g.extProcExtraEnvVars))
	index := make(map[string]int, len(g.extProcExtraEnvVars))

	// Add global env vars first (lowest precedence) preserving input order.
	for _, env := range g.extProcExtraEnvVars {
		result = append(result, env)
		index[env.Name] = len(result) - 1
	}

	// Add GatewayConfig env vars (highest precedence) overriding in-place when names collide,
	// otherwise append in the order they are defined.
	if gatewayConfig != nil && gatewayConfig.Spec.ExtProc != nil && gatewayConfig.Spec.ExtProc.Kubernetes != nil {
		for _, env := range gatewayConfig.Spec.ExtProc.Kubernetes.Env {
			if i, ok := index[env.Name]; ok {
				result[i] = env
			} else {
				result = append(result, env)
				index[env.Name] = len(result) - 1
			}
		}
	}

	return result
}

// resolveExtProcImage chooses the extProc image honoring GatewayConfig overrides.
func (g *gatewayMutator) resolveExtProcImage(extProc *aigv1a1.GatewayConfigExtProc) string {
	if extProc == nil || extProc.Kubernetes == nil {
		return g.extProcImage
	}

	kubernetesExtProc := extProc.Kubernetes
	switch {
	case kubernetesExtProc.Image != nil:
		return *kubernetesExtProc.Image
	case kubernetesExtProc.ImageRepository != nil:
		return mergeImageWithRepository(g.extProcImage, *kubernetesExtProc.ImageRepository)
	default:
		return g.extProcImage
	}
}

// resolveExtProcLogLevel chooses the log level honoring GatewayConfig overrides.
func (g *gatewayMutator) resolveExtProcLogLevel(logLevel *string) string {
	if logLevel == nil {
		return g.extProcLogLevel
	}
	return *logLevel
}

// mergeImageWithRepository reuses the tag or digest from baseImage when a repository override is provided.
func mergeImageWithRepository(baseImage, repository string) string {
	if repository == "" {
		return baseImage
	}

	suffix := imageTagOrDigest(baseImage)
	if suffix == "" {
		return repository
	}
	return repository + suffix
}

// imageTagOrDigest extracts the tag (":vX") or digest ("@sha256:...") from an image reference.
func imageTagOrDigest(image string) string {
	if image == "" {
		return ""
	}
	if idx := strings.Index(image, "@"); idx != -1 {
		return image[idx:]
	}
	lastSlash := strings.LastIndex(image, "/")
	lastColon := strings.LastIndex(image, ":")
	if lastColon != -1 && lastColon > lastSlash {
		return image[lastColon:]
	}
	return ""
}
