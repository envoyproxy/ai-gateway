package e2e

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"time"

	"github.com/go-logr/stdr"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/k3s"
	"golang.org/x/sync/errgroup"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrllog "sigs.k8s.io/controller-runtime/pkg/log"
)

// k8sConfig is the configuration used in K8S clients.
type k8sConfig struct {
	// Config is the k8s client configuration.
	config *rest.Config
	// YamlPath is the path to the temporary kubeconfig file.
	yamlPath string
}

func (k k8sConfig) kubectl(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "kubectl", args...)
	cmd.Env = append(os.Environ(), fmt.Sprintf("KUBECONFIG=%s", k.yamlPath))
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

func (k k8sConfig) helm(ctx context.Context, args ...string) *exec.Cmd {
	args = append([]string{"--kubeconfig", k.yamlPath}, args...)
	cmd := exec.CommandContext(ctx, "helm", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd
}

const k3sImage = "docker.io/rancher/k3s:v1.29.3-k3s1"

// initK3s initializes docker and resources that use it. This logs and
// returns instead of erring, so that normal unit tests can run regardless.
func initK3s(ctx context.Context, images ...string) (config k8sConfig, cleanup func(), err error) {
	fmt.Println("=== INIT: k3s")

	start := time.Now()
	defer func() {
		elapsed := time.Since(start)
		fmt.Printf("    --- done (took %.2fs in total)\n", elapsed.Seconds())
	}()

	// Avoid: log.SetLogger(...) was never called; logs will not be displayed.
	ctrllog.SetLogger(stdr.New(log.New(os.Stderr, "", log.LstdFlags|log.Lshortfile)))

	return newK3sConfig(ctx, images...)
}

// newK3sConfig returns a docker backed Kubernetes configuration. This allows
// E2E style tests, without needing to set up infrastructure out-of-band, or
// accidentally leave test resources in it.
func newK3sConfig(ctx context.Context, images ...string) (config k8sConfig, cleanup func(), err error) {
	var container *k3s.K3sContainer
	if container, err = k3s.Run(ctx,
		k3sImage,
		testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
			ContainerRequest: testcontainers.ContainerRequest{
				// Trim defaults which normally only disables traefik
				Cmd: []string{
					"--disable=local-storage,servicelb,traefik,metrics-server@server:*",
				},
			},
		}),
	); err != nil {
		return
	}

	// If we have any images we pull them locally and push them into the k3s node.
	if len(images) > 0 {
		if err = loadImages(ctx, container, images); err != nil {
			_ = container.Terminate(ctx)
			return
		}
	}

	var yaml []byte
	if yaml, err = container.GetKubeConfig(ctx); err != nil {
		_ = container.Terminate(ctx)
		return
	}

	if config.config, err = clientcmd.RESTConfigFromKubeConfig(yaml); err != nil {
		_ = container.Terminate(ctx)
		return
	}

	// Write the kubeconfig to a temporary file
	tmp, err := os.CreateTemp("", "kubeconfig-*.yaml")
	if err != nil {
		log.Fatalf("error creating temporary file: %v\n", err)
	}
	_, err = tmp.Write(yaml)
	if err != nil {
		log.Fatalf("error writing kubeconfig to temporary file: %v\n", err)
	}
	fmt.Printf("To access k8s cluster: export KUBECONFIG=%s\n", tmp.Name())
	config.yamlPath = tmp.Name()
	cleanup = func() {
		_ = container.Terminate(ctx)
		os.Remove(tmp.Name())
	}
	return
}

// loadImages copies images to kubernetes. Avoid very big images as it slows tests!
func loadImages(ctx context.Context, container *k3s.K3sContainer, images []string) (err error) {
	fmt.Printf("    --- loading images into k3s: %v\n", images)

	// Copy each image to the container in parallel
	var g errgroup.Group
	for _, image := range images {
		image := image
		g.Go(func() (err error) {
			now := time.Now()
			defer func() {
				elapsed := time.Since(now)
				fmt.Printf("        --- loaded image: %s (%.2fs)\n", image, elapsed.Seconds())
			}()
			if err == nil {
				err = container.LoadImages(ctx, image)
			}
			return
		})
	}
	return g.Wait()
}
