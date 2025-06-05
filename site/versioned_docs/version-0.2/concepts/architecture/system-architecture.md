---
id: system-architecture
title: System Architecture Overview
sidebar_position: 1
---

# System Architecture Overview

Envoy AI Gateway follows a modern cloud-native architecture pattern with distinct control and data planes. Let's explore how these components work together to manage AI traffic effectively.

## High-Level Architecture

The architecture is divided into two main planes:

1. **Control Plane**: Responsible for configuring and managing the system
   - Kubernetes API Server serves as the configuration interface
   - AI Gateway Controller manages AI-specific resources and configurations
   - Envoy Gateway Controller handles core proxy configuration and xDS

2. **Data Plane**: Handles the actual request traffic
   - Envoy Proxy and External Processor work together to handle AI traffic
   - Envoy Proxy routes and manages all incoming requests
   - External Processor handles AI-specific transformations and validations
   - Rate Limit Service manages token-based rate limiting

The Control Plane configures the Data Plane through several paths:
- AI Gateway Controller configures the External Processor as well as fine-tuning xDS configurations via Envoy Gateway extension server mechanism.
- Envoy Gateway Controller configures Envoy Proxy through xDS as well as Rate Limit Service.

```mermaid
graph TB
    subgraph "Control Plane"
        K8s[Kubernetes API Server]
        Controller[AI Gateway Controller]
        EGController[Envoy Gateway Controller]

        Controller --> |GWAPI/EG resources|K8s
        K8s -->|AI Gateway resources| Controller
        K8s -->|GWAPI/EG resources| EGController
        Controller <-->|xDS fine-tune| EGController

        style Controller fill:#f9f,stroke:#333,stroke-width:2px
        style EGController fill:#ddf,stroke:#333
    end

    subgraph "Data Plane"
        Envoy[Envoy Proxy]
        ExtProc[External Processor]
        RateLimit[Rate Limit Service]

        Envoy --> RateLimit
        Envoy --> ExtProc

        style Envoy fill:#f9f,stroke:#333,stroke-width:2px
        style ExtProc fill:#f9f,stroke:#333,stroke-width:2px
        style RateLimit fill:#dfd,stroke:#333
    end

    Controller -.->|Configures| ExtProc
    EGController -.->|Configures| RateLimit
    EGController -.->|Updates Config| Envoy
```

## Component Interactions

The system operates through a series of interactions between components:

1. **Configuration Flow**
   - Kubernetes API Server receives configuration changes
   - AI Gateway Controller processes these changes and:
     - Creates resources for Envoy Gateway to consume
     - Configures the External Processor for AI request handling, including token usage extraction.
   - Envoy Gateway Controller watches Gateway API and its Custom resources and:
     - Translates them into Envoy configuration
     - Talk to AI Gateway Controller to fine-tune the configuration.
     - Pushes updates to Envoy Proxy via xDS protocol
     - Configures the Rate Limit Service for token-based limiting

2. **Request Flow**
   - Clients send requests to Envoy Proxy
   - Envoy Proxy routes requests through the External Processor for:
     - Request transformation and validation
     - Provider authentication
     - Token usage tracking
   - Processed requests are sent to AI providers
   - Responses flow back through the same path
   - Rate Limit Service enforces token-based limits

3. **Rate Limiting**
   - External Processor extracts token usage from responses
   - Rate Limit Service receives token usage data
   - Rate limits are enforced based on configured policies
   - Helps manage resource utilization and costs

```mermaid
sequenceDiagram
    participant Client
    participant Envoy as Envoy Proxy
    participant Processor as External Processor
    participant Provider as AI Provider

    Client->>Envoy: Request
    Envoy->>Processor: Process Request
    Note over Processor: Transform & Validate
    Processor-->>Envoy: Add Provider Auth
    Envoy->>Provider: Forward Request
    Provider-->>Envoy: Response
    Envoy->>Processor: Process Response
    Note over Processor: Extract Token Usage
    Processor-->>Envoy: Add Usage Metadata
    Envoy->>Client: Response
```

## Summary

The Envoy AI Gateway architecture provides a robust system for managing AI traffic through:
- Clear separation of control and data plane responsibilities
- Flexible configuration management
- Efficient request processing and transformation
- Token-based rate limiting capabilities

For detailed information about specific components:
- Learn more about the [Control Plane](./control-plane.md) components
- Understand the [Data Plane](./data-plane.md) in detail
