# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

# run like this: uv run --exact -q --env-file .env main.py
#
# Customizing the ".env" like:
#
# OPENAI_BASE_URL=http://localhost:1975/v1
# OPENAI_API_KEY=unused
# CHAT_MODEL=qwen3:4b
#
# MCP_URL=http://localhost:1975/mcp
#
# OTEL_EXPORTER_OTLP_ENDPOINT=http://localhost:4318
# OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
#
# /// script
# dependencies = [
#     "openai-agents",
#     "httpx",
#     "mcp",
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
    Tool,
)
from agents.mcp import MCPServerStreamableHttp, MCPUtil


async def run_agent(prompt: str, model_name: str, tools: list[Tool]):
    model = OpenAIProvider(use_responses=False).get_model(model_name)
    agent = Agent(name="Assistant", model=model, tools=tools)
    result = await Runner.run(
        starting_agent=agent,
        input=prompt,
        run_config=RunConfig(workflow_name="envoy-ai-gateway"),
    )
    print(result.final_output)


async def main(prompt: str, model_name: str, mcp_url: str):
    if not mcp_url:
        await run_agent(prompt, model_name, [])
        return

    async with MCPServerStreamableHttp({"url": mcp_url,"timeout": 300.0},cache_tools_list=True) as server:
        tools = await server.list_tools()
        util = MCPUtil()
        tools = [util.to_function_tool(tool, server, False) for tool in tools]
        await run_agent(prompt, model_name, tools)


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
