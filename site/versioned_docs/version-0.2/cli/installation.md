---
id: aigwinstall
title: Installation
sidebar_position: 1
---


To install the `aigw` CLI, run the following command (This may take a while):

```shell
go install github.com/envoyproxy/ai-gateway/cmd/aigw@main
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

