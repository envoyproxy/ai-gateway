# OpenInference Test Support

This package provides [OpenInference] spans for testing AI Gateway's
[OpenTelemetry] tracing implementation.

## How It Works

1. **Cached spans**: Pre-recorded spans are stored as JSON in the [spans](spans)
   directory.
2. **Automatic recording**: Missing spans are recorded using Docker when
   `RECORD_SPANS=true`
3. **OpenInference instrumentation**: Uses the official Python [OpenInference]
   library to generate spans.
4. **OpenTelemetry collector**: Spans are sent to an in-memory OTLP HTTP
   collector for validation and storage.

## Usage

Choose an appropriate [testopenai cassette](../testopenai/requests.go), and get
an OpenTelemetry span for it like this:

```go
span, err := testopeninference.GetChatSpan(t.Context(), os.Stdout, testopenai.CassetteChatBasic)
```

## Recording New Spans

You can record new spans for the following scenarios:

- A new [testopenai cassette](../testopenai/requests.go) exists, and you want
  to backfill the span for it [spans](spans).
- You have made changes to [openai_proxy.py](openai_proxy.py), or its
  [requirements.txt](requirements.txt) and deleted all JSON in the
  [spans](spans) directory for re-recording.
- You are unsure about a specific span in the [spans](spans) directory, so you
  deleted it to re-record it.

In any of these cases, you can backfill any missing spans like this:

```bash
RECORD_SPANS=true go test -v -run TestGetAllSpans
```

Any missing spans will be automatically recorded using a Docker container
that runs the OpenAI Python SDK with [OpenInference][OpenInference]
instrumentation.

## Why is this needed?

This package bridges two open source communities:

- Envoy AI Gateway, who are experts in GenAI routing and policy, and typically
  program in Go and C++.
- OpenInference, who are experts in GenAI observability and typically program
  in Python and JavaScript.

As this project grows from dozens to hundreds of request shapes, it is
important to have a standardized bridge between the two communities, so that
troubleshooting or getting to the same page is easy.

---

[OpenTelemetry]: https://opentelemetry.io/docs/languages/go/
[OpenInference]: https://github.com/Arize-ai/openinference/tree/main/python/instrumentation/openinference-instrumentation-openai
