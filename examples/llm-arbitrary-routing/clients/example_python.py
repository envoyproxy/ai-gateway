#!/usr/bin/env python3
"""Example: Setting routing plan header with OpenAI Python SDK."""

import base64
import json
from openai import OpenAI

# Define routing plan
plan = {
    "backends": ["azure-primary", "gcp-ptu"],
    "fallbackEnabled": True
}

# Encode as base64 JSON
encoded_plan = base64.b64encode(json.dumps(plan).encode()).decode()

# Create client
client = OpenAI(
    api_key="your-api-key",
    base_url="http://localhost:8080/v1"
)

# Send request with routing plan header
response = client.chat.completions.create(
    model="gpt-4",
    messages=[{"role": "user", "content": "Hello"}],
    extra_headers={"x-ai-eg-routing-plan": encoded_plan}
)

print(response.choices[0].message.content)
