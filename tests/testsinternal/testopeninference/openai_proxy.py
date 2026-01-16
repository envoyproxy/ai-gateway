#!/usr/bin/env python3
# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

"""OpenAI proxy server for generating OpenTelemetry spans in OpenInference format."""

import json
import logging
import os
from fastapi import FastAPI, Request, Response
from fastapi.responses import StreamingResponse
from openai import AsyncOpenAI, AsyncAzureOpenAI
from opentelemetry.instrumentation import auto_instrumentation

# Set up logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# Initialize OpenTelemetry auto instrumentation
auto_instrumentation.initialize()

if "AZURE_OPENAI_API_KEY" in os.environ:
    client = AsyncAzureOpenAI()
else:
    client = AsyncOpenAI()

app = FastAPI()

@app.get("/health")
async def health() -> str:
    return "ok"

@app.post("/v1/chat/completions")
async def chat_completions(request: Request) -> Response:
    request_data = await request.json()
    return await handle_openai_request(
        request,
        client.chat.completions.create,
        request_data=request_data,
        is_streaming=request_data.get('stream', False)
    )

@app.post("/openai/deployments/{deployment}/chat/completions")
async def azure_chat_completions(deployment: str, request: Request) -> Response:
    request_data = await request.json()
    return await handle_openai_request(
        request,
        client.chat.completions.create,
        request_data=request_data,
        is_streaming=request_data.get('stream', False)
    )

@app.post("/v1/completions")
async def completions(request: Request) -> Response:
    request_data = await request.json()
    return await handle_openai_request(
        request,
        client.completions.create,
        request_data=request_data,
        is_streaming=request_data.get('stream', False)
    )

@app.post("/openai/deployments/{deployment}/completions")
async def azure_chat_completions(deployment: str, request: Request) -> Response:
    request_data = await request.json()
    return await handle_openai_request(
        request,
        client.completions.create,
        request_data=request_data,
        is_streaming=request_data.get('stream', False)
    )

@app.post("/v1/embeddings")
async def embeddings(request: Request) -> Response:
    return await handle_openai_request(
        request,
        client.embeddings.create
    )

@app.post("/openai/deployments/{deployment}/embeddings")
async def azure_embeddings(deployment: str, request: Request) -> Response:
    return await handle_openai_request(
        request,
        client.embeddings.create
    )

@app.post("/v1/images/generations")
async def images_generations(request: Request) -> Response:
    return await handle_openai_request(
        request,
        client.images.generate
    )

@app.post("/openai/deployments/{deployment}/images/generations")
async def azure_images_generations(deployment: str, request: Request) -> Response:
    return await handle_openai_request(
        request,
        client.images.generate
    )

async def handle_openai_request(
    request: Request,
    client_method,
    request_data: dict = None,
    is_streaming: bool = False
) -> Response:
    try:
        if request_data is None:
            request_data = await request.json()
        logger.info(f"Received request: {json.dumps(request_data)[:600]}")

        cassette_name = request.headers.get('X-Cassette-Name')
        extra_headers = {"X-Cassette-Name": cassette_name} if cassette_name else {}

        if is_streaming:
            async def stream_response():
                stream = await client_method(**request_data, extra_headers=extra_headers)
                async for chunk in stream:
                    chunk_json = chunk.model_dump_json()
                    yield f"data: {chunk_json}\n\n"
                yield "data: [DONE]\n\n"

            return StreamingResponse(stream_response(), media_type="text/event-stream")
        else:
            response = await client_method(**request_data, extra_headers=extra_headers)
            response_json = response.model_dump_json()
            return Response(content=response_json, media_type="application/json")

    except Exception as e:
        logger.exception("Error processing request")
        # Check if this is an OpenAI API error with a specific status code
        if hasattr(e, 'response') and hasattr(e.response, 'status_code'):
            # Pass through the original error status and response
            return Response(
                content=e.response.content,
                status_code=e.response.status_code,
                headers=dict(e.response.headers)
            )
        # For other exceptions, return 500
        error_response = json.dumps({"error": str(e)})
        return Response(content=error_response, status_code=500, media_type="application/json")

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8080)
