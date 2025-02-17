---
id: architecture
title: Architecture
sidebar_position: 2
---

# Architecture

This section provides a detailed look at the architectural components of Envoy AI Gateway. Understanding the architecture will help you better deploy, configure, and maintain your gateway installation.

## Overview

Envoy AI Gateway follows a modern cloud-native architecture with distinct control and data planes. This separation of concerns allows for better scalability, maintainability, and flexibility in deployment options.

Envoy AI Gateway integrates with Envoy Gateway for the control plane and Envoy Proxy for the data plane.

## Key Concepts

### Control Plane
A control plane is a component that manages the configuration of the data plane. We utilize Envoy Gateway as a central control plane, and Envoy AI Gateway works in conjunction with it to manage the data plane configuration.

### Data Plane
The data plane is the component that sits in the request path and processes the requests. In the context of Envoy AI Gateway, the data plane consists of Envoy Proxy and the AI Gateway external processor that processes the AI requests.

### Token Rate Limiting
The major AI model endpoints return usage metrics called "tokens" per HTTP request. These tokens represent the computational resources consumed by the request. One of the major features of Envoy AI Gateway is rate limiting based on token usage instead of standard "requests per second" style rate limiting.

We call such rate limiting "Token Rate Limiting" in our context, and the metrics that represent the token usage are called "Token Usage" or "Used Tokens".

## In This Section

1. [System Architecture Overview](./system-architecture.md)
   - High-level architecture overview
   - Control and data plane separation
   - Component interactions

2. [Control Plane](./control-plane.md)
   - AI Gateway Controller
   - Envoy Gateway Controller
   - Configuration management
   - Resource orchestration

3. [Data Plane](./data-plane.md)
   - External Processor functionality
   - Request processing flow
   - Provider integration

## What's Next

After understanding the architecture:
- Check out our [Getting Started](../../getting-started/index.md) guide for hands-on experience
