---
id: overview
title: Overview
---

# Envoy AI Gateway Overview

Welcome to the **Envoy AI Gateway** documentation! This open-source project, built on **Envoy Proxy**, aims to simplify how application clients interact with **Generative AI (GenAI)** services. It provides a secure, scalable, and efficient way to manage LLM/AI traffic, with backend rate limiting and policy control.

## **Project Overview**

The **Envoy AI Gateway** was created to address the complexity of connecting applications to GenAI services by leveraging Envoy's flexibility and Kubernetes-native features. The project has evolved through contributions from the Envoy community, fostering a collaborative approach to solving real-world challenges.

### **Key Objectives**
- Provide a unified layer for routing and managing LLM/AI traffic.  
- Support automatic failover mechanisms to ensure service reliability.  
- Ensure end-to-end security, including upstream authorization for LLM/AI traffic.  
- Implement a policy framework to support usage limiting use cases.  
- Foster an open-source community to address GenAI-specific routing and quality of service needs.  


## **Release Goals**

The initial release focuses on key foundational features to provide LLM/AI traffic management:

- **Request Routing and Failover**: Directs API requests to appropriate GenAI services and switches providers based on availability and performance.
- **Load Balancing**: Distributes incoming requests to maintain optimal system performance.
- **Authentication and Authorization**: Implement API key validation to secure communication.
- **Backend Security Policy**: Introduces fine-grained access control for backend services.
This also controls LLM/AI backend usage using token-per-second (TPS) policies to prevent overuse.
- **Multi-Upstream Provider Support for LLM/AI Services**: The ability to receive requests in the format of one LLM provider and route them to different upstream providers, ensuring compatibility with their expected formats. This is made possible through built-in transformation capabilities that adapt requests and responses accordingly. 
- **AWS Request Signing**: Supports external processing for secure communication with AWS-hosted LLM/AI services.

Documentation for installation, setup, and contribution guidelines is included to help new users and contributors get started easily.


## **Community Collaboration**

Weekly community meetings are held every Thursday to discuss updates, address issues, and review contributions. 

## **Architecture Overview**


## **Get Involved**

We welcome community contributions! Here's how you can participate:
- Attend the weekly community meetings to stay updated and share ideas.
- Submit feature requests and pull requests via the GitHub repository.
- Join discussions in the #envoy-ai-gateway Slack channel.

Refer to the contribution guide in the GitHub repository for detailed instructions on setting up your environment and contributing.

---

The **Envoy AI Gateway** addresses the growing demand for secure, scalable, and efficient AI/LLM traffic management. Your contributions and feedback are key to its success and to advancing the future of AI service integration.



