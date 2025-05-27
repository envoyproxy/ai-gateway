// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package controller

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/go-logr/logr"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigv1a1 "github.com/envoyproxy/ai-gateway/api/v1alpha1"
)

// gatewayMutator implements [admission.CustomDefaulter].
type gatewayMutator struct {
	codec  serializer.CodecFactory
	c      client.Client
	kube   kubernetes.Interface
	logger logr.Logger

	extProcImage           string
	extProcImagePullPolicy corev1.PullPolicy
	extProcLogLevel        string
	envoyGatewayNamespace  string
	udsPath                string
}

func newGatewayMutator(c client.Client, kube kubernetes.Interface, logger logr.Logger,
	extProcImage, extProcLogLevel, envoyGatewayNamespace string,
	udsPath string,
) *gatewayMutator {
	return &gatewayMutator{
		c: c, codec: serializer.NewCodecFactory(Scheme),
		kube:                   kube,
		extProcImage:           extProcImage,
		extProcImagePullPolicy: corev1.PullIfNotPresent,
		extProcLogLevel:        extProcLogLevel,
		logger:                 logger,
		envoyGatewayNamespace:  envoyGatewayNamespace,
		udsPath:                udsPath,
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

const mutationNamePrefix = "ai-gateway-"

func (g *gatewayMutator) mutatePod(ctx context.Context, pod *corev1.Pod, gatewayName, gatewayNamespace string) error {
	var routes aigv1a1.AIGatewayRouteList
	err := g.c.List(ctx, &routes, client.MatchingFields{
		k8sClientIndexAIGatewayRouteToAttachedGateway: fmt.Sprintf("%s.%s", gatewayName, gatewayNamespace),
	})
	if err != nil {
		return fmt.Errorf("failed to list routes: %w", err)
	}
	if len(routes.Items) == 0 {
		g.logger.Info("no AIGatewayRoutes found for gateway", "name", gatewayName, "namespace", gatewayNamespace)
		return nil
	}

	podspec := &pod.Spec

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

	// Currently, we have to set the resources for the extproc container at route level.
	// We choose one of the routes to set the resources for the extproc container.
	var resources corev1.ResourceRequirements
	for i := range routes.Items {
		fc := routes.Items[i].Spec.FilterConfig
		if fc != nil && fc.ExternalProcessor != nil && fc.ExternalProcessor.Resources != nil {
			resources = *fc.ExternalProcessor.Resources
		}
	}
	const (
		extProcMetricsPort    = 1064
		extProcHealthPort     = 1065
		filterConfigMountPath = "/etc/filter-config"
		filterConfigFullPath  = filterConfigMountPath + "/" + FilterConfigKeyInSecret
	)
	udsMountPath := filepath.Dir(g.udsPath)
	podspec.Containers = append(podspec.Containers, corev1.Container{
		Name:            mutationNamePrefix + "extproc",
		Image:           g.extProcImage,
		ImagePullPolicy: g.extProcImagePullPolicy,
		Ports: []corev1.ContainerPort{
			{Name: "aigw-metrics", ContainerPort: extProcMetricsPort},
		},
		Args: []string{
			"-configPath", filterConfigFullPath,
			"-logLevel", g.extProcLogLevel,
			"-extProcAddr", "unix://" + g.udsPath,
			"-metricsPort", fmt.Sprintf("%d", extProcMetricsPort),
			"-healthPort", fmt.Sprintf("%d", extProcHealthPort),
		},
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
		SecurityContext: &corev1.SecurityContext{
			AllowPrivilegeEscalation: ptr.To(false),
			Capabilities: &corev1.Capabilities{
				Drop: []corev1.Capability{"ALL"},
			},
			Privileged: ptr.To(false),
			// To allow the UDS to be reachable by the Envoy container, we need the group (not the user) to be the same.
			// This group ID 65532 needs to be updated if the one of Envoy proxy has changed.
			RunAsGroup:   ptr.To(int64(65532)),
			RunAsNonRoot: ptr.To(true),
			RunAsUser:    ptr.To(int64(55532)),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Port:   intstr.FromInt32(extProcHealthPort),
					Path:   "/",
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
	})

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
