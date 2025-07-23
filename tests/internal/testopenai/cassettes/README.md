## Pre-recorded HTTP interactions

Pre-recorded OpenAI request/responses are stored as YAML files in this
directory, using the format defined by https://github.com/dnaeon/go-vcr.

To record, delete the cassette and run [requests_test.go](../requests_test.go)
with your `OPENAI_API_KEY` set.
