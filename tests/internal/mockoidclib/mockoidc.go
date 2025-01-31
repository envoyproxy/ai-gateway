package mockoidclib

const (
	// ResponseBodyHeaderKey is the header key for the response body.
	// The value should be base64 encoded.
	ResponseBodyHeaderKey = "x-response-body"

	// ResponseStatusHeaderKey is the header key for the response status code.
	ResponseStatusHeaderKey = "x-response-status"

	// ResponseHeadersKey is the header key for additional response headers.
	// The value should be base64 encoded and contain comma-separated key:value pairs.
	ResponseHeadersKey = "x-response-headers"
)
