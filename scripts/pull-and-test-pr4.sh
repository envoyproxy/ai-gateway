#!/usr/bin/env bash
# Pull and test https://github.com/groundsada/ai-gateway/pull/4 (Anthropic support for OpenAI).
# Run from ai-gateway repo root. Requires: git, go. Does not run e2e (no kind/Docker/Ollama).
set -e
cd "$(dirname "$0")/.."
echo "==> Fetching origin and PR #4..."
git fetch origin
git fetch origin pull/4/head:pr-4 2>/dev/null || true
echo "==> Checking out pr-4..."
git checkout pr-4
echo "==> Running tests (no e2e)..."
make test-no-e2e
echo "==> Done. All tests passed."
