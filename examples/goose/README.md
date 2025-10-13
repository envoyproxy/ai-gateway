# Goose MCP Recipe Example

## Overview

This example demonstrates how to use [Goose][goose], an AI agent framework by
Block, with the Envoy AI Gateway (aigw). It shows how to create a single agent
that handles both LLM (Large Language Model) calls and MCP (Model Context
Protocol) tool calls through the same gateway.

Goose is an AI agent framework for tools and LLMs. MCP connects AI models to
external tools. The [example recipe](kiwi_recipe.yaml) searches for flights
from New York to Los Angeles on a specified date using the Kiwi flight search.

This demonstrates single-agent origin, where all AI interactions, LLM and MCP,
flow through the Envoy AI Gateway for observability, security, and routing.

## Prerequisites

- Ollama installed and running.
- Goose installed.
- aigw binary installed.

## Manual Steps

1. **Start Ollama** on your host machine:

   Run Ollama on all interfaces with a large context size to support access from Docker and handle complex tasks.

   ```bash
   OLLAMA_CONTEXT_LENGTH=131072 OLLAMA_HOST=0.0.0.0 ollama serve
   ```

2. **Start aigw with MCP configuration**:

   Launch aigw in standalone mode to route LLM requests to Ollama and MCP requests to the Kiwi flight search server.

   ```bash
   OPENAI_BASE_URL=http://localhost:11434/v1 OPENAI_API_KEY=unused aigw run --mcp-json '{"mcpServers":{"kiwi":{"type":"http","url":"https://mcp.kiwi.com"}}}'
   ```

3. **Run the Goose recipe**:

   Execute the flight search recipe with Goose, directing it to the Envoy AI Gateway (port 1975) for both LLM and MCP access.

   ```bash
   OPENAI_HOST=http://127.0.0.1:1975 OPENAI_API_KEY=test-key \
     goose run --provider openai --model qwen3:1.7b --recipe kiwi_recipe.yaml --params flight_date=31/12/2025
   ```

   - Replace `qwen3:1.7b` with your preferred Ollama model.
   - Set `flight_date` to a future date in DD/MM/YYYY format.

4. **Verify the output**:

   The recipe outputs a JSON structure with the top 3 flight options from New
   York to Los Angeles, including airline, flight number, and price.

   Example output:

   ```json
   {
     "contents": [
       {
         "airline": "Example Airlines",
         "flight_number": "EA123",
         "price": "$299"
       },
       ...
     ]
   }
   ```

---

[goose]: https://block.github.io/goose/
