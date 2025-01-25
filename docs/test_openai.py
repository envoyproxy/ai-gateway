from openai import OpenAI

from os import environ

client = OpenAI(
    api_key=environ.get("OPENAI_API_KEY"),
    base_url="http://localhost:1062/v1/"  # Point to local Envoy proxy
)

# Make a completion request
completion = client.chat.completions.create(
            model="gpt-4o-mini",
            messages=[{
                "role": "user",
                "content": "Count from 1 to 5"
            }],
            stream=False,
)
print(completion.choices[0].message.content)