This contains a basic Python agent example that evaluates the given prompt on the configured
OpenAI compatible provider.

Refer to the [cmd/aigw](../../cmd/aigw) directory README and Docker files for details and examples on
how to start the different example OpenTelemetry compatible services.

Once you have everything running you can start the agent by passing a prompt file or directly typing it
into the terminal.

```shell
$ uv run --exact -q --env-file <environment file>> main.py /path/to/prompt.txt
```

or

```shell
$ uv run --exact -q --env-file <environment file>> main.py <<EOF
> your prompt here
EOF
```
