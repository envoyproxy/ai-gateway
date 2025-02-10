---
id: architecture
title: Architecture Overview
sidebar_position: 2
---

# Architecture Overview

This page provides an overview of the architecture of Envoy AI Gateway, and how it integrates with Envoy Gateway
as well as the AI providers. Note that this documents the current state of the project and may change in the future.

## Terminology

### Control Plane

A control plan is a component that manages the configuration of the data plane.
We utilize the Envoy Gateway as a central control plane for the Envoy AI Gateway and
Envoy AI Gateway works in conjunction with the Envoy Gateway to manage the data plane configuration.

### Data Plane

The data plane is the component that sits in the request path and processes the requests.
In the context of Envoy AI Gateway, the data plane consists of Envoy Proxy and the AI Gateway external
processor that processes the AI requests.

### AI Provider / Service / Backend / Platform

AI Provider is a service that servers AI models via a REST API, such as OpenAI, AWS Bedrock, etc.
Not only the commercial AI providers, but also the self-hosted AI services can be considered as AI providers
in our context. We may sometimes refer to AI providers as AI Backend, AI Service, or AI Platform in some contexts. They
are all the same thing in the sense that they host AI model serving REST APIs.

### Token Rate Limiting

The major AI model endpoints, such as `/v1/chat/completions` of OpenAI, return usage metrics called "tokens"
per HTTP request. It represents the amount of "tokens" consumed by the request. In other words, it can be used
to measure how expensive a request is. One of the major features of Envoy AI Gateway is to do a rate limit
based on the token usage instead of the standard "requests per second" style rate limiting.

We call such rate limiting as "Token Rate Limiting" in our context and the metrics that represents the token usage
is called "Token Usage" or "Used Tokens".

## How it works: Control Plane

On the control plane side, Envoy AI Gateway has a k8s controller that watches the AI Gateway CRD and updates the
Envoy Gateway configuration accordingly. In other words, the data plane itself is managed by Envoy Gateway so
that the Envoy AI Gateway controller only needs to update the Envoy Gateway configuration.

![Control Plane](../static/img/control_plane.png)

The above diagram shows how the control plane works. The Envoy AI Gateway controller watches the AI Gateway CRD
and updates the Envoy Gateway configuration. These configuration include the settings for the AI Gateway external
processor so that it can intercept the incoming requests.

In response to the configuration update by Envoy AI Gateway,
the Envoy Gateway updates the Envoy Proxy configuration so that the Envoy Proxy
can process the AI traffic based on the configuration.

## How it works: Data Plane

On the data plane side, the Envoy AI Gateway external processor intercepts all the incoming requests.

There are several steps that the external processor does to process the HTTP requests on request paths:
1. Routing: It calculates the destination AI provider based on the request contents, such as the path, headers and most notably
the model name extracted from the request path.
2. Request Transformation: After it determines the destination AI provider, it does a necessary transformation to the request so that
the AI provider can understand the request. This includes the transformation of the request body as
well as the request path.
3. Upstream Authorization: Optionally, it does the upstream authorization to the AI provider. For example, it can
append the API key to the request headers, etc.

After the external processor does the above steps, the request goes through the Envoy filter chain such
as the rate limiting filter, etc. and then it goes to the AI provider. When rate limiting is enabled, the rate limiting
filter will only check if the rate limit budget is left based on the consumed tokens.

On the response path, the external processor does the following:
1. Response Transformation: It transforms the response from the AI provider so that the client can understand the response.
2. Token Usage Extraction/Calculation: When configured, it extracts the token usage from the response and
calculates the token usage based on the configuration. The calculated number is stored in the per-request dynamic config of
Envoy filter chain so that the rate limiting filter can reduce the rate limit budget based on that token usage when
the HTTP request completes.

![Data Plane](../static/img/data_plane.png)

The above diagram illustrates how the data plane works as described above.

## Summary

In summary, the Envoy AI Gateway is a control plane component that works in coordination with the Envoy Gateway
to manage the data plane configuration for AI traffic. The Envoy AI Gateway external processor intercepts all the incoming
requests and processes them based on the configuration. It does the necessary routing, request transformation,
response transformation, and token usage calculation, etc.

If you are more interested in the implementation details, feel free to dive into the source code of
[the project](https://github.com/envoyproxy/ai-gateway).
