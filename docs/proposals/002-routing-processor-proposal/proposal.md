# External Routing Processor Service for Auto Model Selection and Semantic Caching

## Introduction

The following design is for extending the AI Gateway to support model selection and semantic caching for LLM inference. Both will be key features in optimizing latency, cost, and scalability of LLM services by intelligent routing of the requests and semantic similarity caching.

## Goals

- Smart model selection: route requests based on characteristics and metrics from the backends
- Semantic caching to reduce latency and cost
- Compatibility with the current AI Gateway architecture
- Multi-provider and multi-model type support


Non-Goals:

- Non LLM inference types
- Deploying Models, Managing scaling

## Overview

The AI Gateway currently routes requests to the LLM service based on the model name provided in the request. The new service will provide the functionality to make routing decisions and to cache the information needed to optimize the routing, based on:

- Model Selection: Route requests accordingly to their level of complexity, taking semantic meaning and current metrics on the backend into account
- Semantic Caching: Stores and retrieves responses basing on the semantic similarity between prompts

## Design

### Protocol

Please find the proposed protocol in [routing-processor.proto](routing-processor.proto)

### Model Selection

Model selection operates by:

1. Scoring incoming requests for their complexity and needs
2. Optionally, tracking performance metrics of backends (latencies, rate limits, costs)
3. Making routing decisions based on configurable policies

A model routing PoC can be found [here](https://docs.google.com/document/d/1DVZJS1LC3O3CqokSWoguknLqD-qc467BOyUxTixqQ4c/edit?usp=sharing)

### Semantic Caching

The semantic cache:

- Given a request and response, generates embeddings
- Stores request and response along with its embeddings
- Performed similarity matching for cache lookups
- Handles eviction of cache both based on provided rention period and storage limit

A PoC is to be developed.

### Error Handling

System will handle the following types of failure:

- Cache failures: Will degrade to routing directly into the model.
- Model selection failures: Fallback to specified default model

## Development

- Basic model selection PoC
- Semantic caching PoC
- Advanced metrics and optimizations
