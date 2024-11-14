package e2e

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"golang.org/x/sync/errgroup"
)

// PrepareImages builds the llm-controller image and pulls the images required for the tests.
func prepareImages(ctx context.Context, controllerImage, extProcImage, testupstreamImage string, miscImages ...string) {
	if err := pullImages(ctx, miscImages...); err != nil {
		fmt.Printf("    error pulling images: %v\n", err)
		os.Exit(1)
	}

	if err := buildAIGatewayImage(ctx, controllerImage); err != nil {
		fmt.Printf("    error building image: %v\n", err)
		os.Exit(1)
	}

	if err := buildAIGatewayImage(ctx, extProcImage); err != nil {
		fmt.Printf("    error building image: %v\n", err)
		os.Exit(1)
	}

	if err := buildAIGatewayImage(ctx, testupstreamImage); err != nil {
		fmt.Printf("    error building image: %v\n", err)
		os.Exit(1)
	}
}

// pullImages pulls the images required for the tests.
func pullImages(ctx context.Context, images ...string) (err error) {
	var provider testcontainers.GenericProvider
	if provider, err = testcontainers.ProviderDocker.GetProvider(); err != nil {
		return
	}

	fmt.Printf("=== INIT: pulling images %v\n", images)
	start := time.Now()
	defer func() {
		fmt.Printf("    --- done (took %.2fs in total)\n", time.Since(start).Seconds())
	}()

	// Copy each image to the container in parallel
	var g errgroup.Group
	for _, image := range images {
		image := image
		g.Go(func() (err error) {
			now := time.Now()
			err = provider.PullImage(ctx, image)
			if err != nil {
				return
			}
			fmt.Printf("    --- pulled image: %s (%.2fs)\n", image, time.Since(now).Seconds())
			return
		})
	}
	return g.Wait()
}

// buildAIGatewayControllerImage builds the llm-controller image.
func buildAIGatewayImage(ctx context.Context, image string) (err error) {
	fmt.Printf("=== INIT: building image: %v\n", image)
	start := time.Now()
	defer func() {
		fmt.Printf("    --- done (took %.2fs in total)\n", time.Since(start).Seconds())
	}()

	dockerfile := "../../Dockerfile"
	if _, err := os.Stat(dockerfile); os.IsNotExist(err) {
		return fmt.Errorf("dockerfile not found: %s", dockerfile)
	}

	// Change to the context root and revert after we're done
	originalDir, wdErr := os.Getwd()
	if wdErr != nil {
		err = wdErr
		return
	}
	if err = os.Chdir(path.Dir(dockerfile)); err != nil {
		return err
	}
	defer func() {
		_ = os.Chdir(originalDir)
	}()

	// We can't use testcontainers to build our image as it relies on buildkit
	// https://github.com/docker/for-linux/issues/1136
	args := []string{"buildx", "build", "--load", "-t", image, "."}
	if strings.Contains(image, "extproc") { // nolint: gocritic
		args = append(args, "--build-arg", "NAME=extproc")
	} else if strings.Contains(image, "testupstream") {
		args = append(args, "--build-arg", "NAME=testupstream")
	} else {
		args = append(args, "--build-arg", "NAME=controller")
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Run the command and capture the output
	if err = cmd.Run(); err != nil {
		err = fmt.Errorf("failed to build our image: %w", err)
	}
	return
}
