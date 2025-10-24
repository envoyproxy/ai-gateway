// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package e2elib

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer/yaml"
	yamlutil "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

var (
	// kubeClientset is the singleton Kubernetes clientset
	kubeClientset *kubernetes.Clientset
	// kubeConfig is the singleton Kubernetes rest config
	kubeConfig *rest.Config
	// dynamicClient is the singleton dynamic client
	dynamicClient dynamic.Interface
	// discoveryClient is the singleton discovery client
	discoveryClient discovery.DiscoveryInterface
)

// initKubeClient initializes the Kubernetes client from the kubeconfig
func initKubeClient() error {
	if kubeClientset != nil {
		return nil
	}

	// Get kubeconfig path from environment or default location
	kubeconfigPath := os.Getenv("KUBECONFIG")
	if kubeconfigPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		kubeconfigPath = homeDir + "/.kube/config"
	}

	// Build config from kubeconfig file
	config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("failed to build config from kubeconfig: %w", err)
	}
	kubeConfig = config

	// Create clientset
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create clientset: %w", err)
	}
	kubeClientset = clientset

	// Create dynamic client for unstructured resources
	dynClient, err := dynamic.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create dynamic client: %w", err)
	}
	dynamicClient = dynClient

	// Create discovery client
	discClient, err := discovery.NewDiscoveryClientForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create discovery client: %w", err)
	}
	discoveryClient = discClient

	return nil
}

// getKubeClientset returns the Kubernetes clientset, initializing it if necessary
func getKubeClientset() (*kubernetes.Clientset, error) {
	if err := initKubeClient(); err != nil {
		return nil, err
	}
	return kubeClientset, nil
}

// getKubeConfig returns the Kubernetes rest config, initializing it if necessary
func getKubeConfig() (*rest.Config, error) {
	if err := initKubeClient(); err != nil {
		return nil, err
	}
	return kubeConfig, nil
}

// getDynamicClient returns the dynamic client, initializing it if necessary
func getDynamicClient() (dynamic.Interface, error) {
	if err := initKubeClient(); err != nil {
		return nil, err
	}
	return dynamicClient, nil
}

// kubeApplyManifest applies a Kubernetes manifest from a URL or file path
func kubeApplyManifest(ctx context.Context, manifest string) error {
	var reader io.Reader

	// Check if manifest is a URL
	if strings.HasPrefix(manifest, "http://") || strings.HasPrefix(manifest, "https://") {
		resp, err := http.Get(manifest) // #nosec G107
		if err != nil {
			return fmt.Errorf("failed to fetch manifest from URL: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("failed to fetch manifest: HTTP %d", resp.StatusCode)
		}
		reader = resp.Body
	} else {
		// It's a file path
		file, err := os.Open(manifest)
		if err != nil {
			return fmt.Errorf("failed to open manifest file: %w", err)
		}
		defer file.Close()
		reader = file
	}

	return kubeApplyManifestReader(ctx, reader)
}

// kubeApplyManifestStdin applies a Kubernetes manifest from a string
func kubeApplyManifestStdin(ctx context.Context, manifest string) error {
	return kubeApplyManifestReader(ctx, strings.NewReader(manifest))
}

// kubeApplyManifestReader applies Kubernetes manifests from a reader
func kubeApplyManifestReader(ctx context.Context, reader io.Reader) error {
	clientset, err := getKubeClientset()
	if err != nil {
		return err
	}

	dynClient, err := getDynamicClient()
	if err != nil {
		return err
	}

	decoder := yamlutil.NewYAMLOrJSONDecoder(reader, 4096)
	for {
		var rawObj runtime.RawExtension
		if err := decoder.Decode(&rawObj); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to decode manifest: %w", err)
		}

		if len(rawObj.Raw) == 0 {
			continue
		}

		obj, gvk, err := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme).Decode(rawObj.Raw, nil, nil)
		if err != nil {
			return fmt.Errorf("failed to decode object: %w", err)
		}

		unstructuredObj, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return fmt.Errorf("failed to convert object to unstructured")
		}

		// Use server-side apply for better conflict handling
		if err := applyUnstructuredObject(ctx, clientset, dynClient, unstructuredObj, gvk); err != nil {
			return fmt.Errorf("failed to apply object %s/%s: %w", unstructuredObj.GetNamespace(), unstructuredObj.GetName(), err)
		}
	}

	return nil
}

// applyUnstructuredObject applies an unstructured object using server-side apply
func applyUnstructuredObject(ctx context.Context, clientset *kubernetes.Clientset, dynClient dynamic.Interface, obj *unstructured.Unstructured, gvk *schema.GroupVersionKind) error {
	namespace := obj.GetNamespace()
	name := obj.GetName()

	// Get the REST mapping for the resource
	gv := gvk.GroupVersion()

	// Use dynamic client to apply the resource
	// For common resource types, we can handle them directly
	switch gvk.Kind {
	case "Secret":
		var secret corev1.Secret
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &secret); err != nil {
			return err
		}
		_, err := clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			// Update existing secret
			_, err = clientset.CoreV1().Secrets(namespace).Update(ctx, &secret, metav1.UpdateOptions{})
			return err
		}
		// Create new secret
		_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, &secret, metav1.CreateOptions{})
		return err

	case "Service":
		var svc corev1.Service
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &svc); err != nil {
			return err
		}
		_, err := clientset.CoreV1().Services(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			_, err = clientset.CoreV1().Services(namespace).Update(ctx, &svc, metav1.UpdateOptions{})
			return err
		}
		_, err = clientset.CoreV1().Services(namespace).Create(ctx, &svc, metav1.CreateOptions{})
		return err

	case "Namespace":
		var ns corev1.Namespace
		if err := runtime.DefaultUnstructuredConverter.FromUnstructured(obj.Object, &ns); err != nil {
			return err
		}
		_, err := clientset.CoreV1().Namespaces().Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			_, err = clientset.CoreV1().Namespaces().Update(ctx, &ns, metav1.UpdateOptions{})
			return err
		}
		_, err = clientset.CoreV1().Namespaces().Create(ctx, &ns, metav1.CreateOptions{})
		return err

	default:
		// For other resources, use the dynamic client
		// Determine the resource type
		resourceName := strings.ToLower(gvk.Kind) + "s"

		gvr := gv.WithResource(resourceName)
		var resourceClient dynamic.ResourceInterface
		if namespace != "" {
			resourceClient = dynClient.Resource(gvr).Namespace(namespace)
		} else {
			resourceClient = dynClient.Resource(gvr)
		}

		_, err := resourceClient.Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			// Update existing resource
			_, err = resourceClient.Update(ctx, obj, metav1.UpdateOptions{})
			return err
		}
		// Create new resource
		_, err = resourceClient.Create(ctx, obj, metav1.CreateOptions{})
		return err
	}
}

// kubeDeleteManifest deletes resources from a manifest
func kubeDeleteManifest(ctx context.Context, manifest string) error {
	var reader io.Reader

	// Check if manifest is a URL
	if strings.HasPrefix(manifest, "http://") || strings.HasPrefix(manifest, "https://") {
		resp, err := http.Get(manifest) // #nosec G107
		if err != nil {
			return fmt.Errorf("failed to fetch manifest from URL: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("failed to fetch manifest: HTTP %d", resp.StatusCode)
		}
		reader = resp.Body
	} else {
		// It's a file path
		file, err := os.Open(manifest)
		if err != nil {
			return fmt.Errorf("failed to open manifest file: %w", err)
		}
		defer file.Close()
		reader = file
	}

	clientset, err := getKubeClientset()
	if err != nil {
		return err
	}

	dynClient, err := getDynamicClient()
	if err != nil {
		return err
	}

	decoder := yamlutil.NewYAMLOrJSONDecoder(reader, 4096)
	for {
		var rawObj runtime.RawExtension
		if err := decoder.Decode(&rawObj); err != nil {
			if err == io.EOF {
				break
			}
			return fmt.Errorf("failed to decode manifest: %w", err)
		}

		if len(rawObj.Raw) == 0 {
			continue
		}

		obj, gvk, err := yaml.NewDecodingSerializer(unstructured.UnstructuredJSONScheme).Decode(rawObj.Raw, nil, nil)
		if err != nil {
			return fmt.Errorf("failed to decode object: %w", err)
		}

		unstructuredObj, ok := obj.(*unstructured.Unstructured)
		if !ok {
			return fmt.Errorf("failed to convert object to unstructured")
		}

		namespace := unstructuredObj.GetNamespace()
		name := unstructuredObj.GetName()

		// Delete the object
		switch gvk.Kind {
		case "Secret":
			_ = clientset.CoreV1().Secrets(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		case "Service":
			_ = clientset.CoreV1().Services(namespace).Delete(ctx, name, metav1.DeleteOptions{})
		case "Namespace":
			_ = clientset.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{})
		default:
			gv := gvk.GroupVersion()
			resourceName := strings.ToLower(gvk.Kind) + "s"
			gvr := gv.WithResource(resourceName)
			var resourceClient dynamic.ResourceInterface
			if namespace != "" {
				resourceClient = dynClient.Resource(gvr).Namespace(namespace)
			} else {
				resourceClient = dynClient.Resource(gvr)
			}
			_ = resourceClient.Delete(ctx, name, metav1.DeleteOptions{})
		}
	}

	return nil
}

// kubeGetSecret gets a secret from the cluster
func kubeGetSecret(ctx context.Context, namespace, name string) (*corev1.Secret, error) {
	clientset, err := getKubeClientset()
	if err != nil {
		return nil, err
	}

	secret, err := clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return secret, nil
}

// kubeCreateSecret creates a secret in the cluster
func kubeCreateSecret(ctx context.Context, namespace, name string, data map[string][]byte) error {
	clientset, err := getKubeClientset()
	if err != nil {
		return err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: data,
	}

	_, err = clientset.CoreV1().Secrets(namespace).Create(ctx, secret, metav1.CreateOptions{})
	return err
}

// kubeRestartDeployment restarts a deployment by updating its restart annotation
func kubeRestartDeployment(ctx context.Context, namespace, deployment string) error {
	clientset, err := getKubeClientset()
	if err != nil {
		return err
	}

	// Get the deployment
	deploy, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deployment, metav1.GetOptions{})
	if err != nil {
		return err
	}

	// Add/update restart annotation
	if deploy.Spec.Template.Annotations == nil {
		deploy.Spec.Template.Annotations = make(map[string]string)
	}
	deploy.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"] = time.Now().Format(time.RFC3339)

	// Update the deployment
	_, err = clientset.AppsV1().Deployments(namespace).Update(ctx, deploy, metav1.UpdateOptions{})
	return err
}

// kubeWaitForDeploymentReady waits for a deployment to be ready
func kubeWaitForDeploymentReady(ctx context.Context, namespace, deployment string, timeout time.Duration) error {
	clientset, err := getKubeClientset()
	if err != nil {
		return err
	}

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Wait for deployment to exist first
	for {
		_, err := clientset.AppsV1().Deployments(namespace).Get(ctx, deployment, metav1.GetOptions{})
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for deployment %s/%s to be created: %w", namespace, deployment, ctx.Err())
		case <-time.After(1 * time.Second):
		}
	}

	// Watch for deployment to become ready
	watcher, err := clientset.AppsV1().Deployments(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", deployment),
	})
	if err != nil {
		return fmt.Errorf("failed to watch deployment: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for deployment %s/%s to be ready: %w", namespace, deployment, ctx.Err())
		case event := <-watcher.ResultChan():
			if event.Type == watch.Added || event.Type == watch.Modified {
				deploy, ok := event.Object.(*appsv1.Deployment)
				if !ok {
					continue
				}

				// Check if deployment is available
				for _, cond := range deploy.Status.Conditions {
					if cond.Type == appsv1.DeploymentAvailable && cond.Status == corev1.ConditionTrue {
						return nil
					}
				}
			}
		}
	}
}

// kubeWaitForDaemonSetReady waits for a daemonset to be ready
func kubeWaitForDaemonSetReady(ctx context.Context, namespace, daemonset string, timeout time.Duration) error {
	clientset, err := getKubeClientset()
	if err != nil {
		return err
	}

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Wait for daemonset to exist first
	for {
		_, err := clientset.AppsV1().DaemonSets(namespace).Get(ctx, daemonset, metav1.GetOptions{})
		if err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for daemonset %s/%s to be created: %w", namespace, daemonset, ctx.Err())
		case <-time.After(1 * time.Second):
		}
	}

	// Watch for daemonset to become ready
	watcher, err := clientset.AppsV1().DaemonSets(namespace).Watch(ctx, metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", daemonset),
	})
	if err != nil {
		return fmt.Errorf("failed to watch daemonset: %w", err)
	}
	defer watcher.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for daemonset %s/%s to be ready: %w", namespace, daemonset, ctx.Err())
		case event := <-watcher.ResultChan():
			if event.Type == watch.Added || event.Type == watch.Modified {
				ds, ok := event.Object.(*appsv1.DaemonSet)
				if !ok {
					continue
				}

				// Check if at least one pod is ready
				if ds.Status.NumberReady > 0 {
					return nil
				}
			}
		}
	}
}

// kubeGetServiceBySelector gets the first service matching a selector
func kubeGetServiceBySelector(ctx context.Context, namespace, selector string) (*corev1.Service, error) {
	clientset, err := getKubeClientset()
	if err != nil {
		return nil, err
	}

	services, err := clientset.CoreV1().Services(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, err
	}

	if len(services.Items) == 0 {
		return nil, fmt.Errorf("no service found with selector %s", selector)
	}

	return &services.Items[0], nil
}

// kubeGetPodsBySelector gets pods matching a selector
func kubeGetPodsBySelector(ctx context.Context, namespace, selector string) ([]corev1.Pod, error) {
	clientset, err := getKubeClientset()
	if err != nil {
		return nil, err
	}

	pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: selector,
	})
	if err != nil {
		return nil, err
	}

	return pods.Items, nil
}

// kubeGetPod gets a pod by name
func kubeGetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	clientset, err := getKubeClientset()
	if err != nil {
		return nil, err
	}

	pod, err := clientset.CoreV1().Pods(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return pod, nil
}

// kubeWaitForPodReady waits for a pod with the given selector to be ready
func kubeWaitForPodReady(ctx context.Context, namespace, selector string, timeout time.Duration) error {
	clientset, err := getKubeClientset()
	if err != nil {
		return err
	}

	// Create a context with timeout
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	for {
		pods, err := clientset.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
			LabelSelector: selector,
		})
		if err != nil {
			select {
			case <-ctx.Done():
				return fmt.Errorf("timeout waiting for pod with selector %s to be ready: %w", selector, ctx.Err())
			case <-time.After(1 * time.Second):
				continue
			}
		}

		if len(pods.Items) == 0 {
			select {
			case <-ctx.Done():
				return fmt.Errorf("timeout waiting for pod with selector %s to exist: %w", selector, ctx.Err())
			case <-time.After(1 * time.Second):
				continue
			}
		}

		// Check if any pod is ready
		for _, pod := range pods.Items {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == corev1.PodReady && cond.Status == corev1.ConditionTrue {
					return nil
				}
			}
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for pod with selector %s to be ready: %w", selector, ctx.Err())
		case <-time.After(1 * time.Second):
		}
	}
}

// kubeDeleteNamespace deletes a namespace
func kubeDeleteNamespace(ctx context.Context, name string) error {
	clientset, err := getKubeClientset()
	if err != nil {
		return err
	}

	return clientset.CoreV1().Namespaces().Delete(ctx, name, metav1.DeleteOptions{})
}

// kubePortForward implements port forwarding to a service
type kubePortForward struct {
	namespace   string
	selector    string
	localPort   int
	servicePort int
	stopChan    chan struct{}
	readyChan   chan struct{}
}

// newKubePortForward creates a new port forwarder using client-go
func newKubePortForward(namespace, selector string, localPort, servicePort int) portForward {
	return &kubePortForward{
		namespace:   namespace,
		selector:    selector,
		localPort:   localPort,
		servicePort: servicePort,
		stopChan:    make(chan struct{}, 1),
		readyChan:   make(chan struct{}, 1),
	}
}

func (k *kubePortForward) start(ctx context.Context) error {
	config, err := getKubeConfig()
	if err != nil {
		return err
	}

	clientset, err := getKubeClientset()
	if err != nil {
		return err
	}

	// Get pods matching the selector
	pods, err := clientset.CoreV1().Pods(k.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: k.selector,
	})
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	if len(pods.Items) == 0 {
		return fmt.Errorf("no pods found in namespace %s with selector %s", k.namespace, k.selector)
	}

	podName := pods.Items[0].Name

	// Create the port forward request
	req := clientset.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(k.namespace).
		Name(podName).
		SubResource("portforward")

	transport, upgrader, err := spdy.RoundTripperFor(config)
	if err != nil {
		return fmt.Errorf("failed to create round tripper: %w", err)
	}

	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: transport}, "POST", req.URL())

	ports := []string{fmt.Sprintf("%d:%d", k.localPort, k.servicePort)}

	readyOut := &bytes.Buffer{}
	errOut := &bytes.Buffer{}

	forwarder, err := portforward.New(dialer, ports, k.stopChan, k.readyChan, readyOut, errOut)
	if err != nil {
		return fmt.Errorf("failed to create port forwarder: %w", err)
	}

	// Start port forwarding in a goroutine
	go func() {
		if err := forwarder.ForwardPorts(); err != nil {
			fmt.Fprintf(os.Stderr, "port forward error: %v\n", err)
		}
	}()

	// Wait for ready signal or timeout
	select {
	case <-k.readyChan:
		return nil
	case <-time.After(30 * time.Second):
		return fmt.Errorf("timeout waiting for port forward to be ready")
	}
}

func (k *kubePortForward) kill() {
	close(k.stopChan)
}

// kubeGetUnstructuredResource gets an unstructured resource by GVR
func kubeGetUnstructuredResource(ctx context.Context, namespace, name, group, version, resource string) (*unstructured.Unstructured, error) {
	dynClient, err := getDynamicClient()
	if err != nil {
		return nil, err
	}

	gvr := schema.GroupVersionResource{
		Group:    group,
		Version:  version,
		Resource: resource,
	}

	var obj *unstructured.Unstructured
	if namespace != "" {
		obj, err = dynClient.Resource(gvr).Namespace(namespace).Get(ctx, name, metav1.GetOptions{})
	} else {
		obj, err = dynClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
	}

	return obj, err
}

// kubeGetGatewayAddress gets the address of a Gateway resource
func kubeGetGatewayAddress(ctx context.Context, namespace, name string) (string, error) {
	// Gateway is in gateway.networking.k8s.io/v1
	obj, err := kubeGetUnstructuredResource(ctx, namespace, name, "gateway.networking.k8s.io", "v1", "gateways")
	if err != nil {
		return "", err
	}

	// Extract the address from status.addresses[0].value
	addresses, found, err := unstructured.NestedSlice(obj.Object, "status", "addresses")
	if err != nil || !found || len(addresses) == 0 {
		return "", fmt.Errorf("no addresses found in gateway status")
	}

	firstAddr, ok := addresses[0].(map[string]interface{})
	if !ok {
		return "", fmt.Errorf("invalid address format")
	}

	value, found, err := unstructured.NestedString(firstAddr, "value")
	if err != nil || !found {
		return "", fmt.Errorf("no value found in address")
	}

	return value, nil
}

// kubeDeletePod deletes a pod
func kubeDeletePod(ctx context.Context, namespace, name string) error {
	clientset, err := getKubeClientset()
	if err != nil {
		return err
	}

	return clientset.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}
