---
id: home
title: Home
sidebar_position: 1
---

# Envoy AI Gateway Overview

Welcome to the **Envoy AI Gateway** documentation! This open-source project, built on **Envoy
Proxy**, aims to simplify how application clients interact with **Generative AI (GenAI)** services.
It provides a secure, scalable, and efficient way to manage LLM/AI traffic, with backend rate
limiting and policy control.

## **Project Overview**

The **Envoy AI Gateway** was created to address the complexity of connecting applications to GenAI services by leveraging Envoy's flexibility and Kubernetes-native features. The project has evolved through contributions from the Envoy community, fostering a collaborative approach to solving real-world challenges.

### **Key Objectives**

- Provide a unified layer for routing and managing LLM/AI traffic.
- Support automatic failover mechanisms to ensure service reliability.
- Ensure end-to-end security, including upstream authorization for LLM/AI traffic.
- Implement a policy framework to support usage limiting use cases.
- Foster an open-source community to address GenAI-specific routing and quality of service needs.

## **Project Goals**

The Envoy AI Gateway project is designed to address the critical challenges of AI/LLM integration in enterprise environments through the following core goals:

- **Resilient Connectivity Across Providers and Self-Hosted Models**: Create robust, fault-tolerant connections that integrate with LLM providers (such as OpenAI, Anthropic, AWS Bedrock, etc.) and self-hosted models, ensuring high availability through intelligent routing and automatic failover.

- **Comprehensive Observability for Performance and Cost Management**: Provide visibility into traffic performance, usage patterns, and cost analytics, enabling organizations to optimize their GenAI usage and monitor service quality.

- **Enterprise-Grade Security Features**: Implement security controls including fine-grained access control, authorization policies, rate limiting, to access services, as well as ensure secure egress connection to external providers from the gateway via [Upstream Authentication](capabilities/upstream-auth.mdx).

- **Extensible Architecture**: Leverage Envoy's proven extensibility framework to enable rapid development of custom features, allowing organizations to adapt the gateway to their specific AI infrastructure needs.

- **Reliable Foundation with Envoy Proxy**: Build upon Envoy's battle-tested proxy technology to provide a stable, performant, and production-ready foundation that enterprises can rely on for their traffic handling needs.

These goals guide the development of features and capabilities that make AI/LLM integration secure, scalable, and operationally excellent for enterprise environments.

Documentation for installation, setup, and contribution guidelines is included to help new users and contributors get started easily.

## **Community Collaboration**

[Weekly community meetings][meeting-notes] are held every Thursday to discuss updates, address issues, and review contributions.

## **Architecture Overview**

## **Get Involved**

We welcome community contributions! Here's how you can participate:

- Attend the [weekly community meetings][meeting-notes] to stay updated and share ideas.
- Submit feature requests and pull requests via the GitHub repository.
- Join discussions in the [#envoy-ai-gateway] Slack channel.

Refer to [this contributing guide][contributing.md] for detailed instructions on setting up your
environment and contributing.

---

The **Envoy AI Gateway** addresses the growing demand for secure, scalable, and efficient AI/LLM
traffic management. Your contributions and feedback are key to its success and to advancing the
future of AI service integration.

[meeting-notes]: https://docs.google.com/document/d/10e1sfsF-3G3Du5nBHGmLjXw5GVMqqCvFDqp_O65B0_w
[#envoy-ai-gateway]: https://envoyproxy.slack.com/archives/C07Q4N24VAA
[contributing.md]: https://github.com/envoyproxy/ai-gateway/blob/main/CONTRIBUTING.md
