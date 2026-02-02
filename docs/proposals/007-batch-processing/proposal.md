# Batch Processing API Support

## Table of Contents

<!-- toc -->

- [Summary](#summary)
- [Goals](#goals)
- [Background](#background)
- [Provider Batch API Comparison](#provider-batch-api-comparison)
  - [OpenAI and Azure OpenAI](#openai-and-azure-openai)
  - [AWS Bedrock](#aws-bedrock)
  - [GCP Vertex AI](#gcp-vertex-ai)
  - [Anthropic](#anthropic)
- [Proposed API Endpoints](#proposed-api-endpoints)
- [API Endpoint Specification](#api-endpoint-specification)
  - [File Management](#file-management)
  - [Batch Management](#batch-management)
- [Batch Request Routing Stickiness](#batch-request-routing-stickiness)
  - [Introduce FileID and BatchID Encoding](#introduce-fileid-and-batchid-encoding)
- [Request and Response Transformation](#request-and-response-transformation)
  - [OpenAI to AWS Bedrock](#openai-to-aws-bedrock)
  - [OpenAI to GCP Vertex AI](#openai-to-gcp-vertex-ai)
- [Out of Scope](#out-of-scope)
- [Future Work](#future-work)
- [Open Questions for Community Discussion](#open-questions-for-community-discussion)

<!-- /toc -->

## Summary

This proposal introduces support for batch processing APIs in the Envoy AI Gateway, enabling users to submit large-scale inference jobs for asynchronous processing. Cloud providers including OpenAI, Azure OpenAI, AWS Bedrock, and GCP Vertex AI all support batch processing with similar file-based approaches.

This proposal focuses on implementing a unified Batch API following the OpenAI specification for these providers, with Anthropic's native Batch API deferred to future work due to its fundamentally different design.

## Goals

- Implement a unified Batch API following the OpenAI specification
- Support OpenAI, Azure OpenAI, AWS Bedrock, and GCP Vertex AI providers
- Provide seamless request/response transformation between OpenAI format and provider-specific formats
- Enable file management operations (upload, retrieve, delete) with proper model routing
- Defer Anthropic Batch API support to future work due to architectural differences

## Background

Batch processing is a cost-effective approach for running large-scale AI inference workloads that don't require immediate responses. Most major cloud providers offer batch processing capabilities with significant cost savings (typically 50% compared to real-time inference) and higher rate limits.

The Envoy AI Gateway currently provides a unified OpenAI API interface for real-time inference. Extending this to support batch processing will enable users to:

- Submit batch inference jobs through a unified API
- Leverage cost savings from batch processing across different providers
- Manage batch jobs (create, retrieve, cancel) consistently
- Handle large-scale workloads with higher throughput limits

## Provider Batch API Comparison

### OpenAI and Azure OpenAI

OpenAI and Azure OpenAI use a file-based approach with two main API groups:

#### File Management Endpoints (`/v1/files`)

- **Upload File** (`POST /v1/files`): Upload a JSONL file where each line contains a batch request with `custom_id`, `method`, `url`, and `body`. Returns a `file_id`.
- **Get File Metadata** (`GET /v1/files/{file_id}`): Retrieve file information and metadata.
- **Download File Content** (`GET /v1/files/{file_id}/content`): Download file contents (used for retrieving batch results).
- **Delete File** (`DELETE /v1/files/{file_id}`): Remove an uploaded file.

**Input File Format:**

```jsonl
{"custom_id": "request-1", "method": "POST", "url": "/v1/chat/completions", "body": {"model": "gpt-3.5-turbo-0125", "messages": [{"role": "system", "content": "You are a helpful assistant."},{"role": "user", "content": "Hello world!"}],"max_tokens": 1000}}
{"custom_id": "request-2", "method": "POST", "url": "/v1/chat/completions", "body": {"model": "gpt-3.5-turbo-0125", "messages": [{"role": "system", "content": "You are an unhelpful assistant."},{"role": "user", "content": "Hello world!"}],"max_tokens": 1000}}
```

**Output File Format:**

```jsonl
{"id": "batch_req_abc123", "custom_id": "request-1", "response": {"status_code": 200, "request_id": "req_abc123", "body": {...}}, "error": null}
{"id": "batch_req_def456", "custom_id": "request-2", "response": {"status_code": 200, "request_id": "req_def456", "body": {...}}, "error": null}
```

#### Batch Management Endpoints (`/v1/batches`)

- **Create Batch** (`POST /v1/batches`): Create a batch job by referencing an `input_file_id`. Specify the `endpoint` (e.g., `/v1/chat/completions`) and `completion_window` (e.g., `24h`). Returns a `batch_id`.
- **List Batches** (`GET /v1/batches`): Retrieve all batch jobs with pagination support (`limit`, `after` parameters).
- **Get Batch Status** (`GET /v1/batches/{batch_id}`): Retrieve batch job details including status, progress counters, and output file IDs.
- **Cancel Batch** (`POST /v1/batches/{batch_id}/cancel`): Cancel an in-progress batch job.

**Batch Status Lifecycle:**

- `validating` → `in_progress` → `finalizing` → `completed`
- Alternative states: `failed`, `expired`, `cancelling`, `cancelled`

**Workflow Summary:**

1. Upload input JSONL file → receive `file_id`
2. Create batch with `input_file_id` → receive `batch_id`
3. Poll batch status until `completed`
4. Download results using `output_file_id` from batch response

**Reference**: See [OpenAI Batch API Documentation](https://platform.openai.com/docs/guides/batch) for detailed curl examples and complete request/response schemas.

### AWS Bedrock

AWS Bedrock uses a similar file-based approach but with S3:

1. **Upload to S3**: Upload JSONL input file to S3
2. **Create Batch Job**: Reference S3 URIs

```json
{
  "clientRequestToken": "string",
  "inputDataConfig": {
    "s3InputDataConfig": {
      "s3Uri": "s3://input-bucket/abc.jsonl"
    }
  },
  "jobName": "my-batch-job",
  "modelId": "anthropic.claude-3-haiku-20240307-v1:0",
  "outputDataConfig": {
    "s3OutputDataConfig": {
      "s3Uri": "s3://output-bucket/"
    }
  },
  "roleArn": "arn:aws:iam::123456789012:role/MyBatchInferenceRole"
}
```

**Reference**: See [AWS Bedrock Batch Inference](https://docs.aws.amazon.com/bedrock/latest/userguide/batch-inference-create.html) and [AWS Bedrock CreateModelInvocationJob](https://docs.aws.amazon.com/bedrock/latest/APIReference/API_CreateModelInvocationJob.html)

### GCP Vertex AI

GCP Vertex AI uses Google Cloud Storage (GCS):

1. **Upload to GCS**: Upload JSONL input file to GCS
2. **Create Batch Prediction Job**: Reference GCS URIs

```json
{
  "displayName": "my-cloud-storage-batch-inference-job",
  "model": "publishers/google/models/gemini-2.5-flash",
  "inputConfig": {
    "instancesFormat": "jsonl",
    "gcsSource": {
      "uris": ["gs://bucket/input.jsonl"]
    }
  },
  "outputConfig": {
    "predictionsFormat": "jsonl",
    "gcsDestination": {
      "outputUriPrefix": "gs://bucket/output/"
    }
  }
}
```

**Reference**: See [GCP Vertex Batch Inference](https://docs.cloud.google.com/vertex-ai/generative-ai/docs/multimodal/batch-prediction-from-cloud-storage#create-batch-job-drest) for detailed request/response schema.

### Anthropic

Anthropic takes a different approach with inline requests for batch inference processing:

1. **Create Batch**: Include complete message API payloads directly
   ```json
   {
     "requests": [
       {
         "custom_id": "req-1",
         "params": {
           "model": "claude-3-5-sonnet-20241022",
           "max_tokens": 1024,
           "messages": [...]
         }
       }
     ]
   }
   ```

**Key Difference**: Anthropic doesn't use file references - all request data is included inline in the batch creation request.

## Proposed API Endpoints

The following table outlines the unified Batch API endpoints following OpenAI's specification:

| Endpoint                        | Method | Description                        |
| ------------------------------- | ------ | ---------------------------------- |
| `/v1/files`                     | POST   | Upload a file for batch processing |
| `/v1/files/{file_id}`           | GET    | Retrieve file metadata             |
| `/v1/files/{file_id}/content`   | GET    | Download file content              |
| `/v1/files/{file_id}`           | DELETE | Delete a file                      |
| `/v1/batches`                   | POST   | Create a batch processing job      |
| `/v1/batches`                   | GET    | List all batch jobs                |
| `/v1/batches/{batch_id}`        | GET    | Retrieve batch job status          |
| `/v1/batches/{batch_id}/cancel` | POST   | Cancel a batch job                 |

## API Endpoint Specification

### File Management

#### Upload File

```http
POST /v1/files
Content-Type: multipart/form-data

file: <JSONL file>
purpose: "batch"
```

**Response:**

```json
{
  "id": "file-abc123",
  "object": "file",
  "bytes": 120000,
  "created_at": 1677610602,
  "filename": "batch_input.jsonl",
  "purpose": "batch"
}
```

#### Get File Metadata

```http
GET /v1/files/{file_id}
```

#### Download File Content

```http
GET /v1/files/{file_id}/content
```

#### Delete File

```http
DELETE /v1/files/{file_id}
```

### Batch Management

#### Create Batch

```http
POST /v1/batches
Content-Type: application/json

{
  "input_file_id": "file-abc123",
  "endpoint": "/v1/chat/completions",
  "completion_window": "24h",
  "metadata": {
    "description": "Monthly analysis job"
  }
}
```

**Response:**

```json
{
  "id": "batch-abc123",
  "object": "batch",
  "endpoint": "/v1/chat/completions",
  "errors": null,
  "input_file_id": "file-abc123",
  "completion_window": "24h",
  "status": "validating",
  "output_file_id": null,
  "error_file_id": null,
  "created_at": 1677610602,
  "in_progress_at": null,
  "expires_at": 1677697002,
  "completed_at": null,
  "failed_at": null,
  "expired_at": null,
  "request_counts": {
    "total": 0,
    "completed": 0,
    "failed": 0
  }
}
```

#### List Batches

```http
GET /v1/batches?limit=10
```

#### Get Batch Status

```http
GET /v1/batches/{batch_id}
```

#### Cancel Batch

```http
POST /v1/batches/{batch_id}/cancel
```

## Batch Request Routing Stickiness

Batch APIs routing presents unique challenges compared to real-time inference because batch call(which
happens after file request) must reference to targeted backend where file was uploaded, otherwise batch request will result
in a "file not found" error. For instance, file request hits Azure backend and subsequent batch request hits OpenAI
backend will certainly produce a failure due to file not found unless client upload to both which is out of Envoy AI Gateway
scope.

1. **File Upload**: Model/backend information must be specified via:
   - Request headers (e.g., `X-AI-Gateway-Model`, `X-AI-Gateway-Backend`)
   - Query parameters
   - Metadata in the request body

2. **File Operations**: The `file_id` returned from upload should encode the target backend information, allowing
   subsequent operations (GET, DELETE, GET content) to route correctly without requiring additional headers.

3. **Batch Creation**: When creating a batch job, the gateway can:
   - Extract backend information from the encoded `input_file_id`

4. **Batch Operations**: Similar to file operations, `batch_id` should encode backend information for automatic routing of status checks and cancellations.

### Introduce FileID and BatchID Encoding

Encodes model information into file and batch IDs using base64 (Inspired by how LiteLLM does)

```
Original:  file-abc123
Encoded:   file-bYWlndzpmaWxlLWFiYzEyMzttb2RlbDpncHQtNC4xCg==
           └─┬─┘ └──────────────────┬───────────────────────┘
          prefix      base64(aigw:file-abc123;model:gpt-4.1)

Original:  batch_xyz789
Encoded:   batch_bYWlndzpiYXRjaF94eXo3ODk7bW9kZWw6Z3B0LTQuMQo=
           └──┬──┘└──────────────────┬───────────────────────┘
           prefix       base64(aigw:batch_xyz789;model:gpt-4.1)
```

The encoding:

- Preserves OpenAI-compatible prefixes (`file-`, `batch_`)
- Is transparent to clients
- Enables automatic routing without additional parameters

## Request and Response Transformation

The gateway must translate between OpenAI's file-based format and provider-specific formats.

### OpenAI to AWS Bedrock

**File Upload Flow:**

1. Receive JSONL file via `/v1/files`
2. Transform each line from OpenAI format to Bedrock format
3. Upload transformed JSONL to customer's S3 bucket
4. Return file metadata with encoded S3 URI

**Batch Creation Flow:**

1. Receive `/v1/batches` request with `input_file_id`
2. Decode S3 URI from `file_id`
3. Create Bedrock batch job:
   ```json
   {
     "modelId": "anthropic.claude-3-sonnet-20240229-v1:0",
     "inputDataConfig": {
       "s3InputDataConfig": {
         "s3Uri": "s3://bucket/input.jsonl"
       }
     },
     "outputDataConfig": {
       "s3OutputDataConfig": {
         "s3Uri": "s3://bucket/output/"
       }
     }
   }
   ```
4. Return OpenAI-formatted batch response

**Example Request Transformation:**

OpenAI format:

```json
{
  "custom_id": "request-1",
  "method": "POST",
  "url": "/v1/chat/completions",
  "body": {
    "model": "gpt-3.5-turbo",
    "messages": [{ "role": "user", "content": "Hello" }]
  }
}
```

AWS Bedrock format:

```json
{
  "recordId": "request-1",
  "modelInput": {
    "anthropic_version": "bedrock-2023-05-31",
    "messages": [{ "role": "user", "content": "Hello" }],
    "max_tokens": 1024
  }
}
```

### OpenAI to GCP Vertex AI

Similar transformation approach:

1. Transform JSONL from OpenAI format to Vertex AI format
2. Upload to GCS bucket
3. Create batch prediction job with GCS references

**Example Request Transformation:**

OpenAI format:

```json
{
  "custom_id": "request-1",
  "method": "POST",
  "url": "/v1/chat/completions",
  "body": {
    "model": "gemini-3-pro",
    "messages": [{ "role": "user", "content": "Hello" }]
  }
}
```

GCP Vertex AI format:

```json
{
  "request": {
    "contents": [{ "role": "user", "parts": [{ "text": "Hello" }] }]
  },
  "params": { "temperature": 0.7 }
}
```

## Batch Token Usage and Rate Limiting

Batch API token usage tracking presents unique challenges due to the asynchronous nature of batch processing.
Each provider handles token usage reporting differently, introducing complexity in standardization and tracking.

### Token Usage Reporting Challenges

Token usage reporting for batch requests involves several key complexities:

- **Delayed Information**: Token usage is only available after result retrieval
- **State Management**: Tracking repeated retrievals and preventing double-counting
- **Retrieval Uncertainty**: Handling scenarios where results are never retrieved

### Rate Limiting Complexity

Applying rate limits for batch requests is challenging due to:

- Offline, asynchronous processing nature
- Delayed token usage information
- Multiple potential rate limiting strategies

### Potential Rate Limiting Approaches

1. **Request-Level Limiting**:
   - Constrain number of batch jobs submitted
   - Simple to implement
   - May not accurately reflect actual resource consumption

2. **Token Usage Limiting**:
   - Track and limit total tokens processed
   - More precise representation of computational resources
   - Requires complex tracking mechanism

3. **Hybrid Approach**:
   - Combine request-level and token-based limits
   - Most comprehensive but most complex to implement

### Open Questions

Critical considerations for future implementation:

- Gateway's architectural role: Stateless vs. stateful batch request management
- Interaction with cloud provider-specific storage (AWS S3, GCP GCS)
- Capability to parse results and extract token usage
- Handling of multiple result retrievals
- Management of unretrieved results

### Recommendation

Given the complexity and numerous open questions, I recommend **deferring comprehensive token usage and rate limiting implementation** to a future proposal. The initial implementation will focus on core batch processing functionality.

### Token Usage in Batch Responses

#### OpenAI/Azure

```jsonl
{"id": "batch_req_123", "custom_id": "request-2", "response": {"status_code": 200, "request_id": "req_123", "body": {"id": "chatcmpl-123", "object": "chat.completion", "created": 1711652795, "model": "gpt-3.5-turbo-0125", "choices": [{"index": 0, "message": {"role": "assistant", "content": "Hello."}, "logprobs": null, "finish_reason": "stop"}], "usage": {"prompt_tokens": 22, "completion_tokens": 2, "total_tokens": 24}, "system_fingerprint": "fp_123"}}, "error": null}
{"id": "batch_req_456", "custom_id": "request-1", "response": {"status_code": 200, "request_id": "req_789", "body": {"id": "chatcmpl-abc", "object": "chat.completion", "created": 1711652789, "model": "gpt-3.5-turbo-0125", "choices": [{"index": 0, "message": {"role": "assistant", "content": "Hello! How can I assist you today?"}, "logprobs": null, "finish_reason": "stop"}], "usage": {"prompt_tokens": 20, "completion_tokens": 9, "total_tokens": 29}, "system_fingerprint": "fp_3ba"}}, "error": null}
```

#### GCP Vertex AI

```json
{
  "status": "",
  "processed_time": "2024-11-01T18:13:16.826+00:00",
  "request": {
    "contents": [
      {
        "parts": [
          {
            "fileData": null,
            "text": "What is the relation between the following video and image samples?"
          },
          {
            "fileData": {
              "fileUri": "gs://cloud-samples-data/generative-ai/video/animals.mp4",
              "mimeType": "video/mp4"
            },
            "text": null
          },
          {
            "fileData": {
              "fileUri": "gs://cloud-samples-data/generative-ai/image/cricket.jpeg",
              "mimeType": "image/jpeg"
            },
            "text": null
          }
        ],
        "role": "user"
      }
    ]
  },
  "response": {
    "candidates": [
      {
        "avgLogprobs": -0.5782725546095107,
        "content": {
          "parts": [
            {
              "text": "This video shows a Google Photos marketing campaign where animals at the Los Angeles Zoo take self-portraits using a modified Google phone housed in a protective case. The image is unrelated."
            }
          ],
          "role": "model"
        },
        "finishReason": "STOP"
      }
    ],
    "modelVersion": "gemini-2.0-flash-001@default",
    "usageMetadata": {
      "candidatesTokenCount": 36,
      "promptTokenCount": 29180,
      "totalTokenCount": 29216
    }
  }
}
```

#### AWS Bedrock

```jsonl
{ "recordId" : "3223593EFGH", "modelInput" : {"inputText": "Roses are red, violets are"}, "modelOutput" : {'inputTextTokenCount': 8, 'results': [{'tokenCount': 3, 'outputText': 'blue\n', 'completionReason': 'FINISH'}]}}
{ "recordId" : "1223213ABCD", "modelInput" : {"inputText": "Hello world"}, "error" : {"errorCode" : 400, "errorMessage" : "bad request" }}
```

#### Anthropic

```jsonl
{"custom_id":"my-second-request","result":{"type":"succeeded","message":{"id":"msg_014VwiXbi91y3JMjcpyGBHX5","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[{"type":"text","text":"Hello again! It's nice to see you. How can I assist you today? Is there anything specific you'd like to chat about or any questions you have?"}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":11,"output_tokens":36}}}}
{"custom_id":"my-first-request","result":{"type":"succeeded","message":{"id":"msg_01FqfsLoHwgeFbguDgpz48m7","type":"message","role":"assistant","model":"claude-sonnet-4-5-20250929","content":[{"type":"text","text":"Hello! How can I assist you today? Feel free to ask me any questions or let me know if there's anything you'd like to chat about."}],"stop_reason":"end_turn","stop_sequence":null,"usage":{"input_tokens":10,"output_tokens":34}}}}
```

## Out of Scope

The following items are explicitly out of scope for this initial proposal:

1. **Anthropic Native Batch API**: Due to fundamental architectural differences (inline requests vs. file-based), Anthropic batch support is deferred to future work.
2. **Streaming Batch Results**: All providers return batch results as complete files; real-time streaming of batch progress is not supported.
3. **Cross-Provider Batch Aggregation**: Submitting a single batch job that spans multiple providers.

## Future Work

1. **Anthropic Batch API Support**: Implement support for Anthropic's native inline batch API.

### Anthropic Integration

**Question**: How should we integrate Anthropic's inline batch API in the future?

**Option 1: Automatic Translation**

- Accept OpenAI file-based format at `/v1/batches`
- Gateway reads file and converts to Anthropic's inline format
- Transparent to users
- **Pros**: Unified API, easier for users
- **Cons**: Additional transformation overhead, may not support all Anthropic-specific features

**Option 2: Separate Endpoint**

- Provide `/anthropic/v1/batches` with native inline format
- More aligned with Anthropic's design philosophy
- **Pros**: Native Anthropic support, no transformation overhead
- **Cons**: Different API for Anthropic, less unified

## Open Questions for Community Discussion

### API Endpoint Naming Convention

**Question**: Should we use unified endpoints or provider-prefixed endpoints? I slightly prefer unified endpoints approach.

| Aspect                | Option 1: Unified Endpoints (Proposed)                                                                                      | Option 2: Provider-Prefixed Endpoints                                                                                                    |
| --------------------- | --------------------------------------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| **Endpoint Examples** | `/v1/files`<br>`/v1/batches`                                                                                                | `/openai/v1/files`<br>`/openai/v1/batches`<br>`/bedrock/v1/files`<br>`/bedrock/v1/batches`<br>`/anthropic/v1/batches`                    |
| **Pros**              | • Simpler API surface<br>• Consistent with existing gateway design<br>• Easier migration from OpenAI<br>• OpenAI-compatible | • Explicit provider targeting<br>• No routing ambiguity<br>• Allows provider-specific features<br>• Clear provider separation            |
| **Cons**              | • Requires explicit model routing via headers/query params<br>• Potential routing complexity                                | • More complex API surface<br>• Less unified experience<br>• Breaks from OpenAI compatibility<br>• Client needs to know provider upfront |
| **Model Routing**     | Via headers (`X-AI-Gateway-Model`) or query parameters                                                                      | Implicit from URL path                                                                                                                   |
| **Use Case**          | Best for clients wanting provider-agnostic API                                                                              | Best for clients targeting specific providers                                                                                            |

**Community Input Needed**: Which approach best serves the use cases for batch processing in a multi-provider gateway?
