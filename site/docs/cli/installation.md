---
id: aigwinstall
title: Installation
sidebar_position: 1
---

## Official CLI binaries

Each release includes the binaries for the `aigw` CLI build for different platforms.<br/>
They can be downloaded directly from the corresponding release in the
[GitHub releases page](https://github.com/envoyproxy/ai-gateway/releases).

## Using the Docker image

You can also use the official Docker images to run the CLI without installing it locally.
The CLI images are available as: `docker.io/envoyproxy/ai-gateway-cli:<version>`.

To run the CLI using Docker, you only need to expose the port where the standalone `aigw` listens to
and configure the environment variables for the credentials. If you want to use a custom configuration file,
you can mount it as a volume.

The following example runs the AI Gateway with the default configuration for the [OpenAI provider](../getting-started/connect-providers/openai.md):

```shell
$ docker run --rm -p 1975:1975 -e OPENAI_API_KEY=OPENAI_API_KEY envoyproxy/ai-gateway-cli run
looking up the latest Envoy version
downloading https://archive.tetratelabs.io/envoy/download/v1.35.0/envoy-v1.35.0-linux-arm64.tar.xz
starting: /tmp/envoy-gateway/versions/1.35.0/bin/envoy in run directory /tmp/envoy-gateway/runs/1756912322973222887
```

## Building the latest version

To use the latest version, you can use the following commands to clone the repo and build the CLI:

```shell
git clone https://github.com/envoyproxy/ai-gateway.git
cd ai-gateway
go install ./cmd/aigw
```

:::tip
`go install` command installs a binary in the `$(go env GOPATH)/bin` directory.
Make sure that the `$(go env GOPATH)/bin` directory is in your `PATH` environment variable.

For example, you can add the following line to your shell profile (e.g., `~/.bashrc`, `~/.zshrc`, etc.):
```sh
export PATH=$PATH:$(go env GOPATH)/bin
```
:::

Now, you can check if the installation was successful by running the following command:

```sh
aigw --help
```

This will display the help message for the `aigw` CLI like this:

```
Usage: aigw <command> [flags]

Envoy AI Gateway CLI

Flags:
  -h, --help    Show context-sensitive help.

Commands:
  version [flags]
    Show version.

  translate <path> ... [flags]
    Translate yaml files containing AI Gateway resources to Envoy Gateway and Kubernetes resources. The translated resources are written to stdout.

  run [<path>] [flags]
    Run the AI Gateway locally for given configuration.

Run "aigw <command> --help" for more information on a command.
```

## What's next?

The following sections provide more information about each of the CLI commands:

- [aigw run](./run.md): Run the AI Gateway locally for a given configuration.
- [aigw translate](./translate.md): Translate AI Gateway resources to Envoy Gateway and Kubernetes resources.
