Handle messages in input array that don't have explicit type field. When type is missing but role and content exist, treat as EasyInputMessageParam. This fixes compatibility with clients like Kilo Code that send [{"role": "user", "content": "Hello"}] without type field.

**Description**

The Responses API `/v1/responses` endpoint was returning HTTP 500 errors when clients sent array input without explicit `type` fields in each message object. According to the OpenAI Responses API specification, the `input` parameter accepts both string and array formats. However, the implementation required a `type` field to determine the message variant, causing failures when clients (like Kilo Code) sent simple message arrays like `[{"role": "user", "content": "Hello"}]`.

This commit adds fallback logic to handle messages without explicit `type` fields. When `type` is missing but `role` and `content` fields exist, the unmarshaling logic now treats the object as an `EasyInputMessageParam`, which is the simplest message format compatible with the Responses API specification.

**Related Issues/PRs (if applicable)**

Fixes #1838

**Special notes for reviewers (if applicable)**

The fix is minimal and backward-compatible. It only affects the unmarshaling behavior when `type` is missing, and existing requests with explicit `type` fields continue to work as before. The change adds a check before the switch statement in `ResponseInputItemUnionParam.UnmarshalJSON` to handle this common case.

---

**Acknowledgments**

This PR was developed with the assistance of LLMs deployed via Envoy AI Gateway on the NRP (National Research Platform). We are grateful to the NRP community for providing the infrastructure and resources that made this contribution possible.

