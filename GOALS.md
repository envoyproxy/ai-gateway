# Envoy AI Gateway GOALS.md

## Envoy AI Gateway Goals

The high-level goal of the Envoy AI Gateway project is to facilitate seamless communication between application clients and multiple Generative AI (GenAI) services by leveraging Envoy Gateway.

This open-source project aims to reduce integration complexity for developers and provide a secure, scalable solution for handling GenAI-specific traffic routing.

Envoy AI Gateway will offer a flexible and simple API for configuring GenAI traffic handling with Envoy, leveraging Envoy Gateway.

## Objectives

### Enable GenAI traffic handling with Envoy

Envoy AI Gateway leverages Envoy Gateway and Envoy Proxy to handle GenAI traffic handling. The Envoy AI Gateway will provide control plane extensions, where appropriate, to the Envoy Gateway API to define routing rules for handling traffic to Generative AI services.

### Easy Setup

Envoy AI Gateway will simplify the process of setting up an AI Gateway to manage traffic to and from GenAI services.

Envoy AI Gateway enables Platform Engineers to provide a Gateway solution that enables application developers to focus on leveraging GenAI for feature development.

- **Preset Envoy Gateway Configurations:** Default configurations that simplify setup of routing to GenAI Services, making it accessible to application developers.
- **Leveraging Envoy:** The project aims to leverage the functionality of the Envoy Gateway control plane and the Envoy Proxy data plane.

## Non-Objectives

- **Disruption of Existing Envoy Patterns:** This project is an additive layer designed to expand use cases for Envoy Proxy and Envoy Gateway without changing existing deployment or control patterns.

## Personas

### Application Developer

Focuses on implementing Generative AI services in applications. This user requires a simple and effective way to manage traffic and authentication with AI services.

### Infrastructure Administrator

Responsible for provisioning and maintaining Envoy AI Gateway infrastructure. They need straightforward tools and API support to configure and monitor AI traffic flows securely and at scale.
