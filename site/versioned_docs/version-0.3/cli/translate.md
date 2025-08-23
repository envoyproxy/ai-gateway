---
id: aigtranslate
title: aigw translate
sidebar_position: 3
---

# `aigw translate`

## Overview

This command translates the AI Gateway resources defined in a YAML file to Envoy Gateway and Kubernetes resources.
This can be useful when:
* You want to understand how the AI Gateway resources are translated to Envoy Gateway and Kubernetes resources.
* Deploying the AI Gateway resources to a Kubernetes cluster without running the Envoy AI Gateway.
  * Note that not all functionality can be functional without the Envoy AI Gateway control plane. For example, OIDC credential rotation is not working without the control plane.

You can check the help message via `aigw translate --help`:

```
Usage: aigw translate <path> ... [flags]

Translate yaml files containing AI Gateway resources to Envoy Gateway and Kubernetes resources. The translated resources are written to stdout.

Arguments:
  <path> ...    Paths to yaml files to translate.

Flags:
  -h, --help     Show context-sensitive help.

      --debug    Enable debug logging emitted to stderr.
```

## Usage

To translate the AI Gateway resources defined in a YAML file, say `config.yaml`, to Envoy Gateway and Kubernetes resources, run the following command:

```shell
aigw translate config.yaml
```

This will output the translated resources to the standard output. You can redirect the output to a file if needed:

```shell
aigw translate config.yaml > translated.yaml
```
