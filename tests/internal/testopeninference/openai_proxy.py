#!/usr/bin/env python3
# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

"""OpenAI proxy server for generating OpenTelemetry spans in OpenInference format."""

import json
import logging
from fastapi import FastAPI, Request, Response
from fastapi.responses import StreamingResponse
from openai import AsyncOpenAI
from opentelemetry.instrumentation import auto_instrumentation

# Set up logging
logging.basicConfig(level=logging.INFO)
logger = logging.getLogger(__name__)

# Initialize OpenTelemetry auto instrumentation
auto_instrumentation.initialize()

client = AsyncOpenAI()
app = FastAPI()

@app.get("/health")
async def health() -> str:
    return "ok"

@app.post("/v1/chat/completions")
async def chat_completions(request: Request) -> Response:
    return await handle_openai_request(
        request,
        client.chat.completions.create,
        is_streaming=request_data.get('stream', False)
    )

@app.post("/v1/embeddings")
async def embeddings(request: Request) -> Response:
    return await handle_openai_request(
        request,
        client.embeddings.create
    )

async def handle_openai_request(
    request: Request,
    client_method,
    is_streaming: bool = False
) -> Response:
    try:
        request_data = await request.json()
        logger.info(f"Received request: {json.dumps(request_data)}")

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
        error_response = json.dumps({"error": str(e)})
        return Response(content=error_response, status_code=500, media_type="application/json")

if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="0.0.0.0", port=8080)
