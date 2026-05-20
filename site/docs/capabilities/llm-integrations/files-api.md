---
id: files-api
title: OpenAI Files API
sidebar_position: 12
---

# OpenAI Files API

Envoy AI Gateway supports the [OpenAI Files API](https://platform.openai.com/docs/api-reference/files), enabling you to upload, list, retrieve, and delete files across one or more AI provider backends — with **transparent sticky routing** to ensure every follow-up operation reaches the same backend that received the original upload.

## Overview

The Files API is a prerequisite for batch processing workflows. Uploaded files are referenced in batch jobs and fine-tuning operations by their file IDs.

### Supported Endpoints

| Endpoint                  | Method   | Path                          | Description                             |
| ------------------------- | -------- | ----------------------------- | --------------------------------------- |
| **Upload file**           | `POST`   | `/v1/files`                   | Upload a file via `multipart/form-data` |
| **List files**            | `GET`    | `/v1/files`                   | List files stored in a backend          |
| **Retrieve file**         | `GET`    | `/v1/files/{file_id}`         | Fetch file metadata by ID               |
| **Retrieve file content** | `GET`    | `/v1/files/{file_id}/content` | Download raw file content               |
| **Delete file**           | `DELETE` | `/v1/files/{file_id}`         | Delete a file by ID                     |

### Supported Providers

The Files API is currently supported for **OpenAI-compatible backends**.

## How Routing Works

### Routing Inputs by Operation

| Endpoint                                                               | Routing input                                                                                                                                                                                         |
| ---------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `POST /v1/files`                                                       | `multipart/form-data` only. `model` is required as a multipart field. `backend` is optional as a multipart field and can pin the upload to a specific backend.                                        |
| `GET /v1/files`                                                        | Query parameter `backend` is required (for example: `?backend=default.openai-backend`)                                                                                                                |
| `GET/DELETE /v1/files/{file_id}` and `GET /v1/files/{file_id}/content` | Prefer the gateway-encoded file ID returned by upload. Raw upstream file IDs are also supported, but they must include `?backend=<namespace.backend>` so the gateway can route the request correctly. |

#### Backend value format

When you pass `backend` (for example in `GET /v1/files?backend=...` or as multipart field `backend` in upload), use the namespace-qualified `AIServiceBackend` name in this format:

```
<namespace>.<aiservicebackend-name>
```

Example:

```
default.openai-backend
```

This value should match the `metadata.namespace` and `metadata.name` of your `AIServiceBackend` resource.

### Sticky Routing via Encoded File IDs

A core challenge of file APIs in a gateway context is **routing stickiness**: once a file is uploaded to a specific backend, all subsequent operations on that file (retrieve, retrieve content, delete) **must** be routed to the same backend. Otherwise, the backend returns a "file not found" error.

Envoy AI Gateway solves this transparently without requiring any extra client configuration:

1. On **upload** responses, the gateway rewrites the `id` field in the returned JSON with an encoded file ID that embeds the routing metadata (model name and backend name).
2. On **retrieve**, **content**, or **delete** requests, the gateway decodes the file ID, recovers the original backend, sets internal routing headers, and forwards the request to the correct backend — while forwarding the original provider file ID (not the encoded one).

**This is fully transparent to clients.** Clients use the encoded file ID exactly as returned; no extra headers or configuration are needed for follow-up requests.

#### Encoded ID Format

```
Original:  file-abc123
Encoded:   file-<base64url(aigw:file-abc123;model:gpt-4.1;backend:default.openai-backend)>
```

- The prefix (`file-`) is preserved for OpenAI SDK compatibility.
- The `aigw:` payload prefix identifies an AI Gateway-encoded ID.
- The backend value uses the namespace-qualified form `namespace.name` of the `AIServiceBackend` resource.

#### Fallback behavior for uploads

If provider fallback is configured for the route and the primary backend fails during `POST /v1/files`, Envoy AI Gateway can automatically retry the upload against a fallback backend.

When that retry succeeds:

- the encoded file ID returned to the client contains the backend that actually handled the successful upload, and
- subsequent `retrieve`, `retrieve content`, and `delete` requests are automatically routed to that same fallback backend.

In other words, sticky routing follows the backend that ultimately accepted the upload, not necessarily the primary backend that was tried first.

## Prerequisites

Before using the Files API, set up an `AIGatewayRoute` that references the target `AIServiceBackend`. See [Connecting to AI Providers](./connect-providers.md) for provider setup.

The examples below assume you already have:

- A running gateway with an `AIGatewayRoute` named `my-gateway-route`
- An `AIServiceBackend` for OpenAI with a valid `BackendSecurityPolicy`
- `GATEWAY_URL` environment variable set to your gateway address

See [Basic Usage](../../getting-started/basic-usage.md) for environment setup.

## Example: Files API with OpenAI

### Step 1: Upload a File

`model` is required. `backend` is optional.

- **`model`** determines which backend(s) the request can route to, based on your gateway routing rules.

- **`backend`** (optional) explicitly pins the upload to a specific backend. Use this if you have multiple backends configured and want to upload to a particular one instead of relying on the default primary backend for that model. The value should be a namespace-qualified backend name.

For uploads, provide routing inputs as multipart form fields:

```bash
curl -X POST "$GATEWAY_URL/v1/files" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -F "purpose=fine-tune" \
  -F "model=gpt-4.1" \
  -F "backend=default.openai-backend" \
  -F "file=@training_data.jsonl"
```

The gateway routes the upload to the backend mapped to `gpt-4.1` and returns an encoded file ID:

```json
{
  "id": "file-YWlndzpmaWxlLWFiYzEyMzttb2RlbDpncHQtNC4xO2JhY2tlbmQ6ZGVmYXVsdC5vcGVuYWk",
  "object": "file",
  "bytes": 140,
  "created_at": 1677610602,
  "filename": "training_data.jsonl",
  "purpose": "fine-tune"
}
```

Store the returned ID for subsequent requests:

```bash
export FILE_ID="file-YWlndzpmaWxlLWFiYzEyMzttb2RlbDpncHQtNC4xO2JhY2tlbmQ6ZGVmYXVsdC5vcGVuYWk"
```

If the primary backend fails and the upload succeeds on a configured fallback backend, the returned encoded file ID will embed that fallback backend instead. Subsequent `GET /v1/files/{file_id}`, `GET /v1/files/{file_id}/content`, and `DELETE /v1/files/{file_id}` requests will then be pinned to the fallback backend automatically.

### Step 2: List Files

List files for a specific backend:

```bash
# List files for a specific backend
curl "$GATEWAY_URL/v1/files?backend=default.openai-backend" \
  -H "Authorization: Bearer $OPENAI_API_KEY"
```

List responses are returned as-is from the upstream provider. The gateway does not re-encode `data[].id` values in list responses, so file IDs returned from list are raw provider IDs.

```json
{
  "object": "list",
  "data": [
    {
      "id": "file-abc123",
      "object": "file",
      "bytes": 140,
      "created_at": 1677610602,
      "filename": "training_data.jsonl",
      "purpose": "fine-tune"
    }
  ]
}
```

If you use a raw file ID obtained from a list response, include the backend query parameter on follow-up requests:

```bash
curl "$GATEWAY_URL/v1/files/file-abc123?backend=default.openai-backend" \
  -H "Authorization: Bearer $OPENAI_API_KEY"

curl "$GATEWAY_URL/v1/files/file-abc123/content?backend=default.openai-backend" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -o downloaded_file.jsonl

curl -X DELETE "$GATEWAY_URL/v1/files/file-abc123?backend=default.openai-backend" \
  -H "Authorization: Bearer $OPENAI_API_KEY"
```

If the file was originally uploaded via a fallback backend, use that fallback backend value in the query parameter when working with raw provider file IDs.

### Step 3: Retrieve File Metadata

Use the encoded file ID returned by the gateway directly — no additional routing inputs needed:

```bash
curl "$GATEWAY_URL/v1/files/$FILE_ID" \
  -H "Authorization: Bearer $OPENAI_API_KEY"
```

The gateway decodes the file ID, sets the appropriate internal routing headers, and forwards the request to the correct backend.

### Working with Raw Provider File IDs

If you already have a raw upstream file ID such as `file-abc123`—for example, from a list response or from a pre-existing file created outside the gateway—you can still retrieve, download, or delete it through the gateway.

In that case, append `?backend=<namespace.backend>` so the gateway can route the request to the correct backend:

```bash
curl "$GATEWAY_URL/v1/files/file-abc123?backend=default.openai-backend" \
  -H "Authorization: Bearer $OPENAI_API_KEY"
```

Without the `backend` query parameter, raw file IDs are rejected with `400 Bad Request` because the gateway cannot recover routing information from the ID alone.

### Step 4: Download File Content

```bash
curl "$GATEWAY_URL/v1/files/$FILE_ID/content" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -o downloaded_file.jsonl
```

### Step 5: Delete a File

```bash
curl -X DELETE "$GATEWAY_URL/v1/files/$FILE_ID" \
  -H "Authorization: Bearer $OPENAI_API_KEY"
```

Response:

```json
{
  "id": "file-YWlndzpmaWxlLWFiYzEyMzttb2RlbDpncHQtNC4xO2JhY2tlbmQ6ZGVmYXVsdC5vcGVuYWk",
  "object": "file",
  "deleted": true
}
```

## Example: Multi-Backend File Routing

When you have multiple OpenAI backends, the encoded file ID ensures that each follow-up operation goes to the right backend automatically.

```bash
# Upload to one model/backend mapping
curl -X POST "$GATEWAY_URL/v1/files" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -F "purpose=fine-tune" \
  -F "model=gpt-4.1" \
  -F "file=@training_data_a.jsonl"
# Returns an encoded ID embedding routing metadata

# Upload to another model/backend mapping
curl -X POST "$GATEWAY_URL/v1/files" \
  -H "Authorization: Bearer $OPENAI_API_KEY" \
  -F "purpose=fine-tune" \
  -F "model=gpt-4o-mini" \
  -F "file=@training_data_b.jsonl"
# Returns an encoded ID embedding routing metadata
```

Subsequent retrieve, content, and delete requests for each file automatically target the correct backend from the encoded file ID — no extra client configuration needed.

## Kubernetes Configuration

Below is an example `AIGatewayRoute` configuration for a setup that enables the Files API alongside chat completions. No special configuration is needed specifically for Files API support — the standard `AIGatewayRoute` automatically enables all supported endpoints.

```yaml
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: AIGatewayRoute
metadata:
  name: my-ai-gateway-route
  namespace: default
spec:
  parentRefs:
    - name: my-gateway
      kind: Gateway
      group: gateway.networking.k8s.io
  rules:
    - matches:
        - headers:
            - type: Exact
              name: x-ai-eg-model
              value: gpt-4.1
      backendRefs:
        - name: openai-backend
---
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: AIServiceBackend
metadata:
  name: openai-backend
  namespace: default
spec:
  schema:
    name: OpenAI
  backendRef:
    name: openai-backend
    kind: Backend
    group: gateway.envoyproxy.io
---
apiVersion: aigateway.envoyproxy.io/v1beta1
kind: BackendSecurityPolicy
metadata:
  name: openai-apikey
  namespace: default
spec:
  targetRefs:
    - group: aigateway.envoyproxy.io
      kind: AIServiceBackend
      name: openai-backend
  type: APIKey
  apiKey:
    secretRef:
      name: openai-apikey-secret
      namespace: default
```

:::tip
The `AIGatewayRoute` controller automatically generates **sticky per-backend HTTPRoutes** for each declared backend. These sticky routes match on internal `x-ai-eg-backend` and `x-ai-eg-model` headers, ensuring that decoded file IDs pin requests to the correct backend without affecting regular routing.
:::

The `x-ai-eg-backend` value used internally for sticky routing is the namespace-qualified `AIServiceBackend` name in the form `namespace.name`.

## Limitations

- Files API is supported only for **OpenAI-compatible backends**. Translation for AWS Bedrock, GCP Vertex AI, and other native-format backends is not yet implemented.
- `POST /v1/files` requires `model`; `backend` is optional.
- `GET /v1/files` (list) requires the `backend` query parameter; requests without it return `400 Bad Request`.
- `GET /v1/files` rejects the `model` query parameter.
- Upload routing currently reads multipart form fields (`model`, optional `backend`); routing via request headers, query params, or `metadata` JSON is not implemented for `POST /v1/files`.
- List responses are passthrough; `data[].id` values are not re-encoded.
- Raw upstream file IDs are supported for retrieve/content/delete only when `backend` is supplied as a query parameter.

## What's Next

- **[Supported Endpoints](./supported-endpoints.md)** — Full list of all supported API endpoints
- **[Connecting to AI Providers](./connect-providers.md)** — Configure backends for OpenAI and more
- **[Provider Fallback](../traffic/provider-fallback.md)** — Set up automatic failover between providers
