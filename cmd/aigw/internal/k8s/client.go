// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package k8s

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// Client wraps Kubernetes client functionality.
type Client struct {
	clientset *kubernetes.Clientset
	config    *rest.Config
}

// NewClient creates a new Kubernetes client.
func NewClient() (*Client, error) {
	// Try to load kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
	if err != nil {
		// Fallback to in-cluster config
		config, err = rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to create kubernetes config: %w", err)
		}
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	return &Client{
		clientset: clientset,
		config:    config,
	}, nil
}

// Config returns the Kubernetes REST config.
func (c *Client) Config() *rest.Config {
	return c.config
}

// CheckConnection verifies the connection to the Kubernetes cluster.
func (c *Client) CheckConnection(ctx context.Context) error {
	_, err := c.clientset.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("failed to connect to kubernetes cluster: %w", err)
	}
	return nil
}

// GetServerVersion returns the Kubernetes server version.
func (c *Client) GetServerVersion(ctx context.Context) (string, error) {
	version, err := c.clientset.Discovery().ServerVersion()
	if err != nil {
		return "", fmt.Errorf("failed to get server version: %w", err)
	}
	return version.String(), nil
}

// CheckMinimumVersion checks if the cluster meets the minimum version requirement.
func (c *Client) CheckMinimumVersion(ctx context.Context, minVersion string) error {
	version, err := c.clientset.Discovery().ServerVersion()
	if err != nil {
		return fmt.Errorf("failed to get server version: %w", err)
	}

	// Parse version numbers
	serverMajor, err := strconv.Atoi(version.Major)
	if err != nil {
		return fmt.Errorf("failed to parse server major version: %w", err)
	}

	serverMinor, err := strconv.Atoi(strings.TrimSuffix(version.Minor, "+"))
	if err != nil {
		return fmt.Errorf("failed to parse server minor version: %w", err)
	}

	// Parse minimum version (expected format: "1.29")
	parts := strings.Split(minVersion, ".")
	if len(parts) != 2 {
		return fmt.Errorf("invalid minimum version format: %s", minVersion)
	}

	minMajor, err := strconv.Atoi(parts[0])
	if err != nil {
		return fmt.Errorf("failed to parse minimum major version: %w", err)
	}

	minMinor, err := strconv.Atoi(parts[1])
	if err != nil {
		return fmt.Errorf("failed to parse minimum minor version: %w", err)
	}

	// Compare versions
	if serverMajor < minMajor || (serverMajor == minMajor && serverMinor < minMinor) {
		return fmt.Errorf("kubernetes version %s.%s is below minimum required version %s",
			version.Major, version.Minor, minVersion)
	}

	return nil
}

// CreateNamespace creates a namespace if it doesn't exist.
func (c *Client) CreateNamespace(ctx context.Context, name string) error {
	_, err := c.clientset.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
	if err == nil {
		// Namespace already exists
		return nil
	}

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	_, err = c.clientset.CoreV1().Namespaces().Create(ctx, namespace, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("failed to create namespace %s: %w", name, err)
	}

	return nil
}

// DeleteNamespace deletes a namespace.
func (c *Client) DeleteNamespace(ctx context.Context, name string) error {
	err := c.clientset.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete namespace %s: %w", name, err)
	}
	return nil
}

// WaitForDeployment waits for a deployment to be ready.
func (c *Client) WaitForDeployment(ctx context.Context, namespace, name string, timeout time.Duration) error {
	return wait.PollUntilContextTimeout(ctx, 5*time.Second, timeout, true, func(ctx context.Context) (bool, error) {
		deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		return isDeploymentReady(deployment), nil
	})
}

// RestartDeployment restarts a deployment by updating its restart annotation.
func (c *Client) RestartDeployment(ctx context.Context, namespace, name string) error {
	deployment, err := c.clientset.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to get deployment %s/%s: %w", namespace, name, err)
	}

	if deployment.Spec.Template.Annotations == nil {
		deployment.Spec.Template.Annotations = make(map[string]string)
	}
	deployment.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	_, err = c.clientset.AppsV1().Deployments(namespace).Update(ctx, deployment, metav1.UpdateOptions{})
	if err != nil {
		return fmt.Errorf("failed to restart deployment %s/%s: %w", namespace, name, err)
	}

	return nil
}

// GetPods returns pods in a namespace with optional label selector.
func (c *Client) GetPods(ctx context.Context, namespace string, labelSelector string) (*corev1.PodList, error) {
	pods, err := c.clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get pods in namespace %s: %w", namespace, err)
	}
	return pods, nil
}

// isDeploymentReady checks if a deployment is ready.
func isDeploymentReady(deployment *appsv1.Deployment) bool {
	if deployment.Status.Replicas == 0 {
		return false
	}
	return deployment.Status.ReadyReplicas == deployment.Status.Replicas
}
