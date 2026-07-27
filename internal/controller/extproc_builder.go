// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"crypto/sha256"
	"encoding/hex"
	stdjson "encoding/json" //nolint: depguard // determinism is required: the hash must be byte-identical across the webhook and reconciler, and sonic's Marshal does not guarantee stable field order, which would risk an infinite rollout loop.
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	egv1a1 "github.com/envoyproxy/gateway/api/v1alpha1"
	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	aigv1b1 "github.com/envoyproxy/ai-gateway/api/v1beta1"
	"github.com/envoyproxy/ai-gateway/internal/internalapi"
)

// extProcConfigHashAnnotationKey is stamped on each gateway pod by the mutating
// webhook with a hash of the injected extproc container config. The gateway
// reconciler recomputes the desired hash with the same builder and compares it
// against this annotation to detect config drift (e.g. a changed
// --extProcExtraEnvVars flag) and trigger a rollout. The controller only reads
// this annotation; it never writes it, so a stale hash can never be persisted.
const extProcConfigHashAnnotationKey = "aigateway.envoyproxy.io/extproc-config-hash"

// extProcAdminPort is the admin port of the extproc container.
const extProcAdminPort = 1064

// newExtProcBuilder constructs the shared extproc builder from the controller
// options plus the runtime-resolved extProcAsSideCar flag. It is called once in
// StartControllers; the resulting builder is shared by the mutating webhook (to
// inject the container and stamp the config hash) and the gateway reconciler (to
// detect drift), guaranteeing the two sides compute identical hashes.
func newExtProcBuilder(options *Options, extProcAsSideCar bool, logger logr.Logger) *extProcBuilder {
	var parsedEnvVars []corev1.EnvVar
	if options.ExtProcExtraEnvVars != "" {
		var err error
		parsedEnvVars, err = ParseExtraEnvVars(options.ExtProcExtraEnvVars)
		if err != nil {
			logger.Error(err, "failed to parse extProc extra env vars, skipping",
				"envVars", options.ExtProcExtraEnvVars)
		}
	}

	var parsedImagePullSecrets []corev1.LocalObjectReference
	if options.ExtProcImagePullSecrets != "" {
		// ParseImagePullSecrets only splits on ';' and trims; it never returns
		// an error, so there is no defensive log branch here (unlike env vars).
		parsedImagePullSecrets, _ = ParseImagePullSecrets(options.ExtProcImagePullSecrets)
	}

	return &extProcBuilder{
		image:                                  options.ExtProcImage,
		imagePullPolicy:                        options.ExtProcImagePullPolicy,
		logLevel:                               options.ExtProcLogLevel,
		enableRedaction:                        options.ExtProcEnableRedaction,
		udsPath:                                options.UDSPath,
		requestHeaderAttributes:                options.RequestHeaderAttributes,
		spanRequestHeaderAttributes:            options.TracingRequestHeaderAttributes,
		metricsRequestHeaderAttributes:         options.MetricsRequestHeaderAttributes,
		logRequestHeaderAttributes:             options.LogRequestHeaderAttributes,
		rootPrefix:                             options.RootPrefix,
		endpointPrefixes:                       options.EndpointPrefixes,
		extraEnvVars:                           parsedEnvVars,
		imagePullSecrets:                       parsedImagePullSecrets,
		maxRecvMsgSize:                         options.ExtProcMaxRecvMsgSize,
		extProcAsSideCar:                       extProcAsSideCar,
		mcpSessionEncryptionSeed:               options.MCPSessionEncryptionSeed,
		mcpSessionEncryptionIterations:         options.MCPSessionEncryptionIterations,
		mcpFallbackSessionEncryptionSeed:       options.MCPFallbackSessionEncryptionSeed,
		mcpFallbackSessionEncryptionIterations: options.MCPFallbackSessionEncryptionIterations,
	}
}

// extProcBuilder holds the controller-global extproc configuration and is the
// single source of truth for the injected extproc container. It is constructed
// once in StartControllers from Options and shared by the mutating webhook
// (to inject the container and stamp the config hash) and the gateway reconciler
// (to detect drift and trigger a rollout).
//
// Sharing one builder plus one build function is what prevents rollout loops:
// both sides hash the output of the *same* function rather than maintaining two
// parallel representations of the drift signal. If the webhook and reconciler
// ever computed different hashes for identical state, the deployment would roll
// forever — this structure makes that impossible by construction.
type extProcBuilder struct {
	image           string
	imagePullPolicy corev1.PullPolicy
	logLevel        string
	enableRedaction bool
	udsPath         string

	requestHeaderAttributes        *string
	spanRequestHeaderAttributes    *string
	metricsRequestHeaderAttributes *string
	logRequestHeaderAttributes     *string

	rootPrefix       string
	endpointPrefixes string

	extraEnvVars     []corev1.EnvVar
	imagePullSecrets []corev1.LocalObjectReference
	maxRecvMsgSize   int
	extProcAsSideCar bool

	mcpSessionEncryptionSeed               string
	mcpSessionEncryptionIterations         int
	mcpFallbackSessionEncryptionSeed       string
	mcpFallbackSessionEncryptionIterations int
}

// extProcContainerInput is the per-gateway input the shared builder needs to
// produce the extproc container. Everything not in this struct is global and
// lives on the builder itself.
type extProcContainerInput struct {
	// gatewayConfig is the GatewayConfig referenced by the Gateway (nil if none).
	gatewayConfig *aigv1b1.GatewayConfig
	// needMCP is true when at least one MCPRoute is attached to the Gateway,
	// i.e. the extproc must run the MCP proxy.
	needMCP bool
}

// buildExtProcContainer builds the extproc container *minus* the secret-presence-
// driven parts: the -configPath / -configBundlePath args and the legacy/bundle
// volumeMounts. Those depend on which filter-config secrets happen to exist at
// pod-creation time, not on controller config, so they are excluded from the
// drift signal (the UUID-annotation fast path handles filter-config content
// updates). The mutating webhook adds them after this function returns; the
// reconciler never adds them — it only hashes this base container.
func (b *extProcBuilder) buildExtProcContainer(input extProcContainerInput) corev1.Container {
	var (
		extProcSpec       *aigv1b1.GatewayConfigExtProc
		kubernetesExtProc *egv1a1.KubernetesContainerSpec
	)
	if input.gatewayConfig != nil && input.gatewayConfig.Spec.ExtProc != nil {
		extProcSpec = input.gatewayConfig.Spec.ExtProc
		if extProcSpec.Kubernetes != nil {
			kubernetesExtProc = extProcSpec.Kubernetes
		}
	}

	// Use resources from GatewayConfig if present.
	var resources corev1.ResourceRequirements
	if kubernetesExtProc != nil && kubernetesExtProc.Resources != nil {
		resources = *kubernetesExtProc.Resources
	}

	envVars := b.mergeEnvVars(input.gatewayConfig)
	image := b.resolveExtProcImage(extProcSpec)

	udsMountPath := filepath.Dir(b.udsPath)
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
		ImagePullPolicy: b.imagePullPolicy,
		Ports: []corev1.ContainerPort{
			{Name: "aigw-admin", ContainerPort: extProcAdminPort},
		},
		Args: b.buildExtProcBaseArgs(input.needMCP),
		Env:  envVars,
		VolumeMounts: []corev1.VolumeMount{
			{
				Name:      extProcUDSVolumeName,
				MountPath: udsMountPath,
				ReadOnly:  false,
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

	// Mounts contributed by GatewayConfig are part of the drift signal.
	if kubernetesExtProc != nil && len(kubernetesExtProc.VolumeMounts) > 0 {
		container.VolumeMounts = append(container.VolumeMounts, kubernetesExtProc.VolumeMounts...)
	}

	if b.extProcAsSideCar {
		// When running as a sidecar, we want to ensure the extProc container is shutdown last after Envoy is shutdown.
		container.RestartPolicy = ptr.To(corev1.ContainerRestartPolicyAlways)
	}
	return container
}

// extProcConfigDigest is the canonical representation of everything the webhook
// injects that is drift-relevant: the extproc container (minus the secret-
// presence-driven config-routing args/mounts, which buildExtProcContainer already
// omits) plus the pod-spec-level imagePullSecrets, which are not part of the
// container but are injected by the webhook and must also drift-trigger a rollout.
// Hashing this struct — built by one shared function — is the drift signal both
// the webhook and reconciler use.
type extProcConfigDigest struct {
	Container        corev1.Container
	ImagePullSecrets []corev1.LocalObjectReference
}

// extProcContainerHash returns a stable hex hash of the extproc config the
// webhook injects for the given input. Both the webhook and the reconciler call
// this with the same builder + input, so identical state yields identical
// hashes. It is over the SAME object the builder injects (the container from
// buildExtProcContainer plus pod-spec-level imagePullSecrets), so the drift
// signal can never diverge from the injected config.
func (b *extProcBuilder) extProcContainerHash(input extProcContainerInput) string {
	digest := extProcConfigDigest{
		Container:        b.buildExtProcContainer(input),
		ImagePullSecrets: b.imagePullSecrets,
	}
	// stdjson.Marshal of a struct is deterministic (fields in declaration
	// order; map keys sorted). Since the builder always produces the same
	// object for the same input, the bytes — and therefore the hash — are
	// stable. stdlib json is used (not internal/json/sonic) because sonic
	// does not guarantee stable field order, which would risk rollout loops.
	// corev1.Container is a plain serializable struct, so Marshal cannot
	// fail here; the error is ignored accordingly.
	marshaled, _ := stdjson.Marshal(digest) // corev1.Container is serializable; see comment above
	sum := sha256.Sum256(marshaled)
	return hex.EncodeToString(sum[:])
}

// buildExtProcBaseArgs builds the command line arguments for the extproc
// container excluding the secret-presence-driven -configPath / -configBundlePath
// flags. The mutating webhook prepends those based on which filter-config
// secrets exist; they are intentionally kept out of the drift hash.
func (b *extProcBuilder) buildExtProcBaseArgs(needMCP bool) []string {
	args := []string{
		"-logLevel", b.logLevel,
		"-extProcAddr", "unix://" + b.udsPath,
		"-adminPort", fmt.Sprintf("%d", extProcAdminPort),
		"-rootPrefix", b.rootPrefix,
		"-maxRecvMsgSize", fmt.Sprintf("%d", b.maxRecvMsgSize),
	}
	if needMCP {
		args = append(args,
			"-mcpAddr", ":"+strconv.Itoa(internalapi.MCPProxyPort),
			"-mcpSessionEncryptionSeed", b.mcpSessionEncryptionSeed,
			"-mcpSessionEncryptionIterations", strconv.Itoa(b.mcpSessionEncryptionIterations),
		)
		if b.mcpFallbackSessionEncryptionSeed != "" {
			args = append(args,
				"-mcpFallbackSessionEncryptionSeed", b.mcpFallbackSessionEncryptionSeed,
				"-mcpFallbackSessionEncryptionIterations", strconv.Itoa(b.mcpFallbackSessionEncryptionIterations),
			)
		}
	}

	if b.requestHeaderAttributes != nil {
		args = append(args, "-requestHeaderAttributes", *b.requestHeaderAttributes)
	}
	if b.spanRequestHeaderAttributes != nil {
		args = append(args, "-spanRequestHeaderAttributes", *b.spanRequestHeaderAttributes)
	}
	if b.metricsRequestHeaderAttributes != nil {
		args = append(args, "-metricsRequestHeaderAttributes", *b.metricsRequestHeaderAttributes)
	}
	if b.logRequestHeaderAttributes != nil {
		args = append(args, "-logRequestHeaderAttributes", *b.logRequestHeaderAttributes)
	}
	if b.endpointPrefixes != "" {
		args = append(args, "-endpointPrefixes", b.endpointPrefixes)
	}
	if b.enableRedaction {
		args = append(args, "-enableRedaction")
	}
	return args
}

// extProcUDSVolumeName is the name of the volume backing the extproc UDS socket,
// shared between the extproc container and the envoy container.
const extProcUDSVolumeName = mutationNamePrefix + "extproc-uds"

// mergeEnvVars merges env vars; GatewayConfig overrides global while preserving order.
func (b *extProcBuilder) mergeEnvVars(gatewayConfig *aigv1b1.GatewayConfig) []corev1.EnvVar {
	result := make([]corev1.EnvVar, 0, len(b.extraEnvVars))
	index := make(map[string]int, len(b.extraEnvVars))

	// Add global env vars first (lowest precedence) preserving input order.
	for _, env := range b.extraEnvVars {
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
func (b *extProcBuilder) resolveExtProcImage(extProc *aigv1b1.GatewayConfigExtProc) string {
	if extProc == nil || extProc.Kubernetes == nil {
		return b.image
	}

	kubernetesExtProc := extProc.Kubernetes
	switch {
	case kubernetesExtProc.Image != nil:
		return *kubernetesExtProc.Image
	case kubernetesExtProc.ImageRepository != nil:
		return mergeImageWithRepository(b.image, *kubernetesExtProc.ImageRepository)
	default:
		return b.image
	}
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
