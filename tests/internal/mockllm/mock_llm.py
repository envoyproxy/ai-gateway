# Copyright Envoy AI Gateway Authors
# SPDX-License-Identifier: Apache-2.0
# The full text of the Apache license is available in the LICENSE file at
# the root of the repo.

"""Deterministic mock LLM server for quota budget E2E testing.

Returns a fixed chat completion response with predictable token usage
so that rate limit budget consumption can be verified in E2E tests.
"""

import json
import os

from fastapi import FastAPI, Request
from fastapi.responses import JSONResponse

app = FastAPI()

_BODY = {
    "choices": [{"message": {"content": "Hello, world!", "role": "assistant"}}],
    "usage": {"prompt_tokens": 10, "completion_tokens": 5, "total_tokens": 15},
}


@app.get("/health")
async def health() -> str:
    return "ok"


@app.post("/v1/chat/completions")
async def chat_completions(request: Request) -> JSONResponse:
    body = await request.json()
    resp = dict(_BODY)
    resp["model"] = body.get("model", "mock-model")
    return JSONResponse(content=resp)


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=int(os.getenv("LISTENER_PORT", "8080")))
