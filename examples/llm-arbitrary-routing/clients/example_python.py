#!/usr/bin/env python3
"""Example: Setting routing plan header with OpenAI Python SDK.

This example demonstrates how to use the x-ai-eg-routing-plan header
to control backend selection across multiple providers.
"""

import base64
import json
from openai import OpenAI

# Define routing plan: try Azure first, fall back to GCP
plan = {
    "backends": ["azure-primary", "gcp-ptu"],
    "fallbackEnabled": True
}

# Encode as base64 JSON
encoded_plan = base64.b64encode(json.dumps(plan).encode()).decode()

# Create client pointing to AI Gateway
client = OpenAI(
    api_key="your-api-key",
    base_url="http://localhost:8080/v1"
)

# Send request with routing plan header
response = client.chat.completions.create(
    model="gpt-4",
    messages=[
        {"role": "user", "content": "What is the capital of France?"}
    ],
    extra_headers={"x-ai-eg-routing-plan": encoded_plan}
)

print(response.choices[0].message.content)

# Example output on fallback:
# Attempt 1: x-ai-eg-routing-plan directs to azure-primary
#   -> DFP sets :authority=snc-oai-llmproxy-dev-eastus2.openai.azure.com
#   -> Azure responds 503
#   -> Envoy retries
# Attempt 2: AI Gateway picks backends[1] (gcp-ptu)
#   -> DFP sets :authority=us-central1-aiplatform.googleapis.com
#   -> GCP returns 200 ✓
