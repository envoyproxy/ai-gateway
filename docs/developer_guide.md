# Setup Envoy AI Gateway Local Development Environment

## Prerequisites
- Go 1.23 or later
- Envoy proxy installed
- OpenAI API or AWS Bedrock key

## Build Required Binaries

```bash
git clone https://github.com/envoyproxy/ai-gateway
cd ai-gateway

# Build the extproc binary
go build -o out/extproc cmd/extproc/main.go
```

## Start Envoy

Download Envoy from [release page](https://github.com/envoyproxy/envoy/releases). 

The following instructions assume you have the `envoy` binary in your PATH.

Use this [envoy.yaml](envoy.yaml) configuration, which is derived from the `test-extproc` test suite, as a starting point to set up the Envoy proxy.

```bash
envoy -c envoy.yaml
```

## Create External Processor Configuration

Save the OpenAI API key to a file named `/tmp/openai-api-key`.

Use this [extproc-config.yaml](extproc-config.yaml), which is also derived from the same test suite, to start ext_proc.


```bash
./out/extproc -configPath extproc-config.yaml
```

## Use with Python/Jupyter to Test

Now use the OpenAI Python client by pointing it to your local Envoy proxy. Here's an example:

```python
from openai import OpenAI

client = OpenAI(
    api_key="YOUR_API_KEY",  # Your OpenAI API key
    base_url="http://localhost:1062/v1/"  # Point to local Envoy proxy
)

# Make a completion request
completion = client.chat.completions.create(
    model="gpt-4o-mini",  # Model name must match the one in the extproc-config.yaml
    messages=[{"role": "user", "content": "Hello, how are you?"}]
)
print(completion.choices[0].message.content)
```