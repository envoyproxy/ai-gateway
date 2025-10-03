# E2E MCP Tests

This directory contains end-to-end tests for the AI Gateway CLI's MCP (Model Context Protocol) functionality.

## Prerequisites

1. **Ollama**: Install and start Ollama locally
   ```bash
   # Start Ollama with external access
   OLLAMA_HOST=0.0.0.0 ollama serve

   # Pull the test model
   ollama pull qwen3:0.6b
   ```

2. **Goose**: Install the Goose AI framework
   ```bash
   # Install goose according to the official documentation
   # https://block.github.io/goose/docs/installation/
   ```
