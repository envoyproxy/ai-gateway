# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

# run like this: uv run --exact -q --env-file .env agent.py
#
# Customizing the ".env" like:
#
# OPENAI_BASE_URL=http://localhost:1975/v1
# OPENAI_API_KEY=unused
# CHAT_MODEL=qwen3:4b
#
# MCP_URL=http://localhost:1975/mcp
#
# OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4317
# OTEL_EXPORTER_OTLP_PROTOCOL=grpc
#
# /// script
# dependencies = [
#     "openai-agents",
#     "httpx",
#     "mcp",
#     "elastic-opentelemetry",
#     "openinference-instrumentation-openai-agents",
#     "opentelemetry-instrumentation-httpx",
#     "openinference-instrumentation-mcp",
# ]
# ///

from opentelemetry.instrumentation import auto_instrumentation

# This must precede any other imports you want to instrument!
auto_instrumentation.initialize()

import argparse
import asyncio
import os
import sys

from agents import (
    Agent,
    OpenAIProvider,
    RunConfig,
    Runner,
)
from agents.mcp import MCPServer, MCPServerStreamableHttp, MCPUtil

# Uncomment the following lines to enable agent verbose logging
# from agents import enable_verbose_stdout_logging
# enable_verbose_stdout_logging()


async def main(prompt: str, model_name: str, mcp_url: str):
    async with MCPServerStreamableHttp(
        name="Envoy AI Gateway MCP",
        params={"url": mcp_url, "timeout": 30.0},
        cache_tools_list=True,
        client_session_timeout_seconds=30.0,
    ) as server:
        model = OpenAIProvider(use_responses=False).get_model(model_name)
        agent = Agent(name="Assistant", model=model, mcp_servers=[server])
        result = await Runner.run(
            starting_agent=agent,
            input=prompt,
            run_config=RunConfig(workflow_name="Envoy AI Gateway Example"),
        )
        print(result.final_output)


if __name__ == "__main__":
    parser = argparse.ArgumentParser("Example Agent with Tools")
    parser.add_argument("prompt", help="Prompt to be evaluated.", default=sys.stdin, type=argparse.FileType('r'), nargs='?')
    parser.add_argument("--model", help="Model to use.", default=os.getenv("CHAT_MODEL"), type=str)
    parser.add_argument("--mcp-url", help="MCP Server to connect to.", default=os.getenv("MCP_URL"), type=str)
    args = parser.parse_args()
    prompt = args.prompt.read()

    print(f"Prompt: {prompt}")
    print(f"Using model: {args.model}")
    print(f"Using MCP URL: {args.mcp_url}")

    asyncio.run(main(prompt, args.model, args.mcp_url))
