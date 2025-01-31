---
id: glossary
title: Glossary
sidebar_position: 6
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# AI Gateway Glossary

This glossary provides definitions for key terms and concepts used in AI Gateway and GenAI traffic handling.

<Tabs>
<TabItem value="alphabetical" label="Alphabetical" default>

## C-F
### Content Filtering
A mechanism to screen and moderate AI-generated content to ensure compliance with ethical standards, company policies, or regulatory requirements.

<div style={{marginBottom: '1rem'}}><small>Related: [Model Routing](#model-routing) · [GenAI Gateway](#genai-gateway)</small></div>

### Context Window
The maximum amount of text (in tokens) that a model can process in a single request.

<div style={{marginBottom: '1rem'}}><small>Related: [Token](#token) · [Token Cost](#token-cost) · [Token Usage Limiting](#llm-token-usage-limiting)</small></div>

### Fine-Tuned Model
A version of a base Generative AI model that has been customized for specific tasks or domains using additional training data.

<div style={{marginBottom: '1rem'}}><small>Related: [Foundation Model](#foundation-model) · [Model Provider](#model-provider) · [Model Endpoint](#model-endpoint)</small></div>

### Foundation Model
Foundation models are large-scale, pre-trained AI models designed to handle a broad range of tasks. They are trained on extensive datasets and can be fine-tuned or adapted to specific use cases.

<div style={{marginBottom: '1rem'}}><small>Related: [Fine-Tuned Model](#fine-tuned-model) · [Model Provider](#model-provider) · [Model Endpoint](#model-endpoint)</small></div>

## G-I
### GenAI Gateway
A specialized gateway solution designed to manage, monitor, and route traffic to Generative AI models. It provides capabilities such as load balancing, authorization, token usage monitoring, and integration with multiple model providers.

<div style={{marginBottom: '1rem'}}><small>Related: [Hybrid GenAI Gateway](#hybrid-genai-gateway) · [Inference Instance Gateway](#inference-instance-gateway) · [Model Routing](#model-routing)</small></div>

### GenAI Usage Analytics
The collection and analysis of data regarding how users interact with AI models via the GenAI Gateway, including token usage, request patterns, and latency metrics.

<div style={{marginBottom: '1rem'}}><small>Related: [GenAI Usage Monitoring](#genai-usage-monitoring) · [Token Usage Limiting](#llm-token-usage-limiting)</small></div>

### GenAI Usage Monitoring
The tracking of resource consumption across different types of models, including token-based monitoring for LLMs, image resolution and compute resources for LVMs, and combined metrics for multimodal models.

<div style={{marginBottom: '1rem'}}><small>Related: [Token Usage Limiting](#llm-token-usage-limiting) · [GenAI Usage Analytics](#genai-usage-analytics) · [Token Cost](#token-cost)</small></div>

### Hybrid GenAI Gateway
A GenAI Gateway configuration that supports both local inference instances and external cloud-based AI models, providing flexibility in deployment and cost management.

<div style={{marginBottom: '1rem'}}><small>Related: [GenAI Gateway](#genai-gateway) · [Inference Instance Gateway](#inference-instance-gateway) · [Model Provider](#model-provider)</small></div>

### Inference Instance
An individual compute resource or container used to run a machine learning model for generating AI outputs (inference).

<div style={{marginBottom: '1rem'}}><small>Related: [Inference Service](#inference-service) · [Inference Instance Gateway](#inference-instance-gateway) · [Model Endpoint](#model-endpoint)</small></div>

### Inference Instance Gateway
A gateway specifically configured to route requests to one or more inference instances, handling load balancing, scaling, and traffic management at the level of the instances.

<div style={{marginBottom: '1rem'}}><small>Related: [GenAI Gateway](#genai-gateway) · [Inference Instance](#inference-instance)</small></div>

### Inference Service
A service that provides model inference capabilities, including model loading, input processing, inference execution, and output formatting.

<div style={{marginBottom: '1rem'}}><small>Related: [Inference Instance](#inference-instance) · [Model Endpoint](#model-endpoint) · [Model Provider](#model-provider)</small></div>

## L-P
### LLM Token Usage Limiting
A mechanism to monitor and control the number of tokens processed by an LLM GenAI model, including input, output, and total token limits.

<div style={{marginBottom: '1rem'}}><small>Related: [Token](#token) · [Token Cost](#token-cost) · [GenAI Usage Monitoring](#genai-usage-monitoring)</small></div>

### Model Endpoint
The API endpoint provided by a specific AI model, whether hosted by a cloud provider, open-source solution, or private deployment.

<div style={{marginBottom: '1rem'}}><small>Related: [Model Provider](#model-provider) · [Foundation Model](#foundation-model) · [Fine-Tuned Model](#fine-tuned-model)</small></div>

### Model Provider
External service providing AI model capabilities through APIs, such as OpenAI, AWS Bedrock, Azure OpenAI Service, or Anthropic.

<div style={{marginBottom: '1rem'}}><small>Related: [Model Endpoint](#model-endpoint) · [Foundation Model](#foundation-model) · [Fine-Tuned Model](#fine-tuned-model)</small></div>

### Model Routing
A feature in GenAI Gateways that dynamically routes requests to specific models or model versions based on client configuration, use case requirements, or service level agreements.

<div style={{marginBottom: '1rem'}}><small>Related: [Model Endpoint](#model-endpoint) · [GenAI Gateway](#genai-gateway)</small></div>

### Prompt
The input text that guides the AI model's response, including instructions, context, and specific queries.

<div style={{marginBottom: '1rem'}}><small>Related: [Token](#token) · [Context Window](#context-window) · [Temperature](#temperature)</small></div>

## R-T
### Rate of LLM Token Consumption
The speed at which tokens are consumed by an AI model during processing. This metric is crucial for cost estimation and performance optimization.

<div style={{marginBottom: '1rem'}}><small>Related: [Token](#token) · [Token Cost](#token-cost) · [Token Usage Limiting](#llm-token-usage-limiting)</small></div>

### Temperature
A parameter that controls the randomness/creativity of model outputs, typically ranging from 0 (deterministic) to 1 (more creative).

<div style={{marginBottom: '1rem'}}><small>Related: [Foundation Model](#foundation-model) · [Model Provider](#model-provider) · [Prompt](#prompt)</small></div>

### Token
The basic unit of text processing in LLMs, representing parts of words or characters.

<div style={{marginBottom: '1rem'}}><small>Related: [Context Window](#context-window) · [Token Cost](#token-cost) · [Token Usage Limiting](#llm-token-usage-limiting)</small></div>

### Token Cost
The financial or resource cost associated with token usage in model requests.

<div style={{marginBottom: '1rem'}}><small>Related: [Token](#token) · [Rate of LLM Token Consumption](#rate-of-llm-token-consumption) · [Token Usage Limiting](#llm-token-usage-limiting)</small></div>

</TabItem>

<TabItem value="by-category" label="By Category">

## AI/ML Fundamentals
- [Token](#token)
- [Prompt](#prompt)
- [Context Window](#context-window)
- [Temperature](#temperature)
- [Token Cost](#token-cost)

## Inference Infrastructure
- [Inference Instance](#inference-instance)
- [Inference Service](#inference-service)
- [Model Provider](#model-provider)

## Gateway Components
- [GenAI Gateway](#genai-gateway)
- [Inference Instance Gateway](#inference-instance-gateway)
- [Hybrid GenAI Gateway](#hybrid-genai-gateway)

## Usage & Analytics
- [GenAI Usage Monitoring](#genai-usage-monitoring)
- [LLM Token Usage Limiting](#llm-token-usage-limiting)
- [Rate of LLM Token Consumption](#rate-of-llm-token-consumption)
- [GenAI Usage Analytics](#genai-usage-analytics)

## Model Types & Management
- [Foundation Model](#foundation-model)
- [Fine-Tuned Model](#fine-tuned-model)
- [Model Routing](#model-routing)
- [Model Endpoint](#model-endpoint)

## Content & Safety
- [Content Filtering](#content-filtering)

</TabItem>

<TabItem value="quick-reference" label="Quick Reference">

## Common Concepts
Quick explanations of the most frequently used terms:

| Term | Quick Definition |
|------|-----------------|
| GenAI Gateway | Gateway for managing AI model traffic |
| Foundation Model | Base pre-trained AI model |
| Token | Basic unit of text in LLM processing |
| Token Usage | Monitoring and limiting model resource consumption |
| Model Routing | Directing requests to appropriate models |
| Prompt | Input text guiding AI model response |
| Temperature | Control for model output randomness |
| Content Filtering | AI content moderation and safety |

</TabItem>
</Tabs>

:::note
This glossary is continuously evolving as the field of GenAI traffic handling develops. If you'd like to contribute or suggest changes, please visit our [GitHub repository](https://github.com/envoyproxy/ai-gateway).
:::

:::tip See Also
- Check our [Getting Started](./getting-started/index.md) guide for practical examples
- Visit our [Capabilities](./capabilities/index.md) section for detailed feature documentation
- Join our [Community Slack](https://envoyproxy.slack.com/archives/C07Q4N24VAA) for discussions
::: 