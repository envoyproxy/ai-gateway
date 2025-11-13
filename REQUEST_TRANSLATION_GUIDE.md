# Request Translation Flow - How to See Translated Requests

This guide explains how requests are translated in the AI Gateway and how to observe the translated endpoint and body sent to backends.

## Architecture Overview

The AI Gateway uses an **External Processing (ExtProc)** pattern where Envoy sends request/response data to an external processor that performs translation:

```
Client → Envoy → ExtProc (Translator) → Backend (OpenAI/GCP/AWS/etc)
         ↑                    ↓
         └────────────────────┘
```

## Request Translation Flow

### 1. **Request Body Processing** (Router Filter)

When a request comes in, the router filter processor parses the request:

**File**: `internal/extproc/audiospeech_processor.go`

```go
func (a *audioSpeechProcessorRouterFilter) ProcessRequestBody(ctx context.Context, rawBody *extprocv3.HttpBody) {
    // Parse the original OpenAI request
    model, body, err := parseAudioSpeechBody(rawBody)
    
    // Add model name to headers for routing
    a.requestHeaders[internalapi.ModelNameHeaderKeyDefault] = model
}
```

### 2. **Request Translation** (Upstream Filter)

The upstream filter uses a translator to convert the request format:

**File**: `internal/extproc/audiospeech_processor.go`

```go
func (a *audioSpeechProcessorUpstreamFilter) ProcessRequestHeaders(ctx context.Context, _ *corev3.HeaderMap) {
    // Translate the request body and headers
    headerMutation, bodyMutation, err := a.translator.RequestBody(
        a.originalRequestBodyRaw, 
        a.originalRequestBody, 
        a.onRetry
    )
}
```

### 3. **Building Translated Request** (Translator)

For OpenAI → GCP Vertex AI translation:

**File**: `internal/extproc/translator/audio_openai_gcpvertexai.go`

```go
func (a *audioSpeechOpenAIToGCPVertexAITranslator) RequestBody(...) {
    // 1. Build GCP Gemini request from OpenAI request
    geminiReq := gcp.GenerateContentRequest{
        Contents: []genai.Content{
            {
                Role: "user",
                Parts: []*genai.Part{
                    genai.NewPartFromText(body.Input),
                },
            },
        },
        GenerationConfig: &genai.GenerationConfig{
            ResponseModalities: []genai.Modality{genai.ModalityAudio},
            SpeechConfig: &genai.SpeechConfig{
                VoiceConfig: &genai.VoiceConfig{
                    PrebuiltVoiceConfig: &genai.PrebuiltVoiceConfig{
                        VoiceName: mapOpenAIVoiceToGemini(body.Voice),
                    },
                },
            },
        },
    }
    
    // 2. Marshal to JSON
    geminiReqBody, err := json.Marshal(geminiReq)
    
    // 3. Build the path/endpoint
    pathSuffix := buildGCPModelPathSuffix(
        gcpModelPublisherGoogle, 
        a.requestModel, 
        gcpMethodStreamGenerateContent, 
        "alt=sse"
    )
    // Example path: /v1/publishers/google/models/gemini-1.5-flash/streamGenerateContent?alt=sse
    
    // 4. Create mutations
    headerMutation, bodyMutation := buildRequestMutations(pathSuffix, geminiReqBody)
    
    return headerMutation, bodyMutation, nil
}
```

### 4. **Building Mutations** (Utility)

**File**: `internal/extproc/translator/util.go`

```go
func buildRequestMutations(path string, reqBody []byte) (*ext_procv3.HeaderMutation, *ext_procv3.BodyMutation) {
    // Set the :path header (endpoint)
    headerMutation = &ext_procv3.HeaderMutation{
        SetHeaders: []*corev3.HeaderValueOption{
            {
                Header: &corev3.HeaderValue{
                    Key:      ":path",           // This is the endpoint!
                    RawValue: []byte(path),      // e.g., /v1/publishers/google/models/gemini-1.5-flash/...
                },
            },
        },
    }
    
    // Set content-length
    headerMutation.SetHeaders = append(headerMutation.SetHeaders, &corev3.HeaderValueOption{
        Header: &corev3.HeaderValue{
            Key:      "content-length",
            RawValue: []byte(strconv.Itoa(len(reqBody))),
        },
    })
    
    // Set the body
    bodyMutation = &ext_procv3.BodyMutation{
        Mutation: &ext_procv3.BodyMutation_Body{Body: reqBody},  // This is the translated body!
    }
    
    return headerMutation, bodyMutation
}
```

## How to See Translated Requests

### Method 1: Enable Debug Logging

The ExtProc server logs request details when DEBUG level is enabled.

**File**: `internal/extproc/server.go`

```go
func (s *Server) processMsg(ctx context.Context, l *slog.Logger, p Processor, req *extprocv3.ProcessingRequest, ...) {
    case *extprocv3.ProcessingRequest_RequestHeaders:
        if l.Enabled(ctx, slog.LevelDebug) {
            l.Debug("request headers processing", slog.Any("request_headers", filteredHdrs))
        }
        
    case *extprocv3.ProcessingRequest_RequestBody:
        l.Debug("request body processing", slog.Any("request", req))
        // After processing:
        l.Debug("request body processed", slog.Any("response", filteredBody))
}
```

#### Enable Debug Logging:

**For Helm Deployment:**

Edit `manifests/charts/ai-gateway-helm/values.yaml`:

```yaml
extProc:
  logLevel: debug  # Change from 'info' to 'debug'
```

**For Local Development:**

Run the extproc with `-logLevel=debug` flag:

```bash
go run ./cmd/extproc/main.go -configPath=./config.yaml -logLevel=debug
```

**For aigw CLI:**

```bash
go run ./cmd/aigw run ./examples/mcp/mcp_example.yaml --debug
```

### Method 2: View Logs

Once debug logging is enabled, you'll see logs like:

```
DEBUG request body processing request=<original OpenAI request>
DEBUG request body processed response=<with mutations>
DEBUG request headers processing request_headers=<with :path header showing endpoint>
```

The `:path` header in the logs shows the **translated endpoint**.
The body mutation shows the **translated request body**.

### Method 3: Network Traffic Inspection

You can use tools to inspect the actual HTTP traffic:

**Using tcpdump:**
```bash
# Capture traffic to backend
tcpdump -i any -A 'host <backend-host> and port <backend-port>'
```

**Using Envoy Access Logs:**

Configure Envoy to log full request/response bodies in the gateway configuration.

### Method 4: Add Custom Logging

You can add custom logging to see the exact translated values:

**In the translator file** (`audio_openai_gcpvertexai.go`):

```go
func (a *audioSpeechOpenAIToGCPVertexAITranslator) RequestBody(...) {
    // ... existing code ...
    
    pathSuffix := buildGCPModelPathSuffix(...)
    
    // Add custom logging
    fmt.Printf("=== TRANSLATED REQUEST ===\n")
    fmt.Printf("Endpoint: %s\n", pathSuffix)
    fmt.Printf("Body: %s\n", string(geminiReqBody))
    fmt.Printf("========================\n")
    
    headerMutation, bodyMutation := buildRequestMutations(pathSuffix, geminiReqBody)
    return headerMutation, bodyMutation, nil
}
```

## Example Translation

### Original OpenAI Request

```http
POST /v1/audio/speech
Content-Type: application/json

{
  "model": "tts-1",
  "voice": "alloy",
  "input": "Hello world"
}
```

### Translated GCP Vertex AI Request

```http
POST /v1/publishers/google/models/gemini-1.5-flash/streamGenerateContent?alt=sse
Content-Type: application/json

{
  "contents": [
    {
      "role": "user",
      "parts": [
        {
          "text": "Hello world"
        }
      ]
    }
  ],
  "generationConfig": {
    "responseModalities": ["AUDIO"],
    "temperature": 1.0,
    "speechConfig": {
      "voiceConfig": {
        "prebuiltVoiceConfig": {
          "voiceName": "Zephyr"
        }
      }
    }
  }
}
```

## Key Translation Points

1. **Endpoint/Path**: Constructed by translators using methods like `buildGCPModelPathSuffix()`
2. **Request Body**: Transformed from OpenAI format to backend-specific format (GCP, AWS, Azure, etc.)
3. **Headers**: Mutations include `:path`, `content-length`, and authentication headers
4. **Voice Mapping**: OpenAI voices mapped to backend-specific voices (e.g., "alloy" → "Zephyr")

## Supported Translators

- `openai_openai.go` - OpenAI to OpenAI (passthrough with model override)
- `openai_gcpvertexai.go` - OpenAI to GCP Vertex AI
- `openai_gcpanthropic.go` - OpenAI to GCP Anthropic
- `openai_azureopenai.go` - OpenAI to Azure OpenAI
- `openai_awsbedrock.go` - OpenAI to AWS Bedrock
- `audio_openai_gcpvertexai.go` - OpenAI Audio to GCP Gemini Audio

Each translator implements the translation logic for its specific backend API format.
