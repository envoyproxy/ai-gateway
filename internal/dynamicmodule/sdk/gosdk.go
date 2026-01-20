// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package sdk

import "io"

// NewHTTPFilterConfig is a function that creates a new HTTPFilter that corresponds to each filter configuration in the Envoy filter chain.
// This is a global variable that should be set in the init function in the program once.
//
// The function is called once globally. The function is only called by the main thread,
// so it does not need to be thread-safe.
var NewHTTPFilterConfig func(name string, config []byte) HTTPFilterConfig

// HTTPFilterConfig is an interface that represents a single http filter in the Envoy filter chain.
// It is used to create HTTPFilter(s) that correspond to each Http request.
//
// This is only created once per module configuration via the NewHTTPFilter function.
type HTTPFilterConfig interface {
	// NewFilter is called for each new Http request.
	// Note that this must be concurrency-safe as it can be called concurrently for multiple requests.
	NewFilter(e EnvoyHTTPFilter) HTTPFilter
}

// EnvoyHTTPFilter is an interface that represents the underlying Envoy filter.
// This is passed to each event hook of the HTTPFilter.
//
// **WARNING**: This must not outlive each event hook since there's no guarantee that the EnvoyHTTPFilter will be valid after the event hook is returned.
// To perform the asynchronous operations, use [EnvoyHTTPFilter.NewScheduler] to create a [Scheduler] and perform the operations in a separate Goroutine.
// Then, use the [Scheduler.Commit] method to commit the event to the Envoy filter on the correct worker thread to continue processing the request.
type EnvoyHTTPFilter interface {
	// GetRequestHeader gets the first value of the request header. Returns the value and true if the header is found.
	GetRequestHeader(key string) (string, bool)
	// GetRequestHeaders gets all the request headers.
	GetRequestHeaders() map[string][]string
	// SetRequestHeader sets the request header. Returns true if the header is set successfully.
	SetRequestHeader(key string, value []byte) bool
	// GetResponseHeader gets the first value of the response header. Returns the value and true if the header is found.
	GetResponseHeader(key string) (string, bool)
	// GetResponseHeaders gets all the response headers.
	GetResponseHeaders() map[string][]string
	// SetResponseHeader sets the response header. Returns true if the header is set successfully.
	SetResponseHeader(key string, value []byte) bool

	// GetBufferedRequestBody gets the request body. Returns the io.Reader and true if the body is found.
	GetBufferedRequestBody() (BodyReader, bool)
	// DrainBufferedRequestBody drains n bytes from the request body. This will invalidate the io.Reader returned by GetRequestBody before this is called.
	DrainBufferedRequestBody(n int) bool
	// AppendBufferedRequestBody appends the data to the request body. This will invalidate the io.Reader returned by GetRequestBody before this is called.
	AppendBufferedRequestBody(data []byte) bool
	// GetBufferedResponseBody gets the response body. Returns the io.Reader and true if the body is found.
	GetBufferedResponseBody() (BodyReader, bool)
	// DrainBufferedResponseBody drains n bytes from the response body. This will invalidate the io.Reader returned by GetResponseBody before this is called.
	DrainBufferedResponseBody(n int) bool
	// AppendBufferedResponseBody appends the data to the response body. This will invalidate the io.Reader returned by GetResponseBody before this is called.
	AppendBufferedResponseBody(data []byte) bool

	// GetReceivedRequestBody gets the received request body. Returns the io.Reader and true if the body is found.
	GetReceivedRequestBody() (BodyReader, bool)
	// DrainReceivedRequestBody drains n bytes from the received request body. This will invalidate the io.Reader returned by GetReceivedRequestBody before this is called.
	DrainReceivedRequestBody(n int) bool
	// AppendReceivedRequestBody appends the data to the received request body. This will invalidate the io.Reader returned by GetReceivedRequestBody before this is called.
	AppendReceivedRequestBody(data []byte) bool
	// GetReceivedResponseBody gets the received response body. Returns the io.Reader and true if the body is found.
	GetReceivedResponseBody() (BodyReader, bool)
	// DrainReceivedResponseBody drains n bytes from the received response body. This will invalidate the io.Reader returned by GetReceivedResponseBody before this is called.
	DrainReceivedResponseBody(n int) bool
	// AppendReceivedResponseBody appends the data to the received response body. This will invalidate the io.Reader returned by GetReceivedResponseBody before this is called.
	AppendReceivedResponseBody(data []byte) bool

	// SendLocalReply sends a local reply to the client. This must not be used in after returning continue from the response headers phase.
	SendLocalReply(statusCode uint32, headers [][2]string, body []byte)
	// GetDynamicMetadataString gets the dynamic metadata value for the given namespace and key. Returns the value and true if the value is found.
	GetDynamicMetadataString(namespace string, key string) (string, bool)
	// GetUpstreamHostMetadataString gets the upstream host metadata value for the given namespace and key. Returns the value and true if the value is found.
	GetUpstreamHostMetadataString(namespace string, key string) (string, bool)
	// SetDynamicMetadataString sets the dynamic metadata value for the given namespace and key. Returns true if the value is set successfully.
	SetDynamicMetadataString(namespace string, key string, value string) bool
	// SetDynamicMetadataNumber sets the dynamic metadata value for the given namespace and key. Returns true if the value is set successfully.
	SetDynamicMetadataNumber(namespace string, key string, value float64) bool

	// ClearRouteCache clears the route cache for the current request.
	ClearRouteCache()
}

type BodyReader interface {
	// Len returns the length of the body in bytes, regardless of how much has been read.
	Len() int
	io.Reader
	io.WriterTo
}

// HTTPFilter is an interface that represents each Http request.
//
// This is created for each new Http request and is destroyed when the request is completed.
type HTTPFilter interface {
	// RequestHeaders is called when the request headers are received.
	RequestHeaders(e EnvoyHTTPFilter, endOfStream bool) RequestHeadersStatus
	// RequestBody is called when the request body is received.
	RequestBody(e EnvoyHTTPFilter, endOfStream bool) RequestBodyStatus
	// ResponseHeaders is called when the response headers are received.
	ResponseHeaders(e EnvoyHTTPFilter, endOfStream bool) ResponseHeadersStatus
	// ResponseBody is called when the response body is received.
	ResponseBody(e EnvoyHTTPFilter, endOfStream bool) ResponseBodyStatus
	// OnDestroy is called when the filter is being destroyed.
	OnDestroy()
}

// NoopHTTPFilter is a no-op implementation of the HTTPFilter interface.
type NoopHTTPFilter struct{}

func (f NoopHTTPFilter) RequestHeaders(EnvoyHTTPFilter, bool) RequestHeadersStatus {
	return RequestHeadersStatusContinue
}

func (f NoopHTTPFilter) RequestBody(EnvoyHTTPFilter, bool) RequestBodyStatus {
	return RequestBodyStatusContinue
}

func (f NoopHTTPFilter) ResponseHeaders(EnvoyHTTPFilter, bool) ResponseHeadersStatus {
	return ResponseHeadersStatusContinue
}

func (f NoopHTTPFilter) ResponseBody(EnvoyHTTPFilter, bool) ResponseBodyStatus {
	return ResponseBodyStatusContinue
}

func (f NoopHTTPFilter) OnDestroy() {}

// RequestHeadersStatus is the return value of the HTTPFilter.RequestHeaders.
type RequestHeadersStatus int

const (
	// RequestHeadersStatusContinue is returned when the operation should continue.
	RequestHeadersStatusContinue                  RequestHeadersStatus = 0
	RequestHeadersStatusStopIteration             RequestHeadersStatus = 1
	RequestHeadersStatusStopAllIterationAndBuffer RequestHeadersStatus = 3
)

// RequestBodyStatus is the return value of the HTTPFilter.RequestBody event.
type RequestBodyStatus int

const (
	RequestBodyStatusContinue               RequestBodyStatus = 0
	RequestBodyStatusStopIterationAndBuffer RequestBodyStatus = 1
)

// ResponseHeadersStatus is the return value of the HTTPFilter.ResponseHeaders event.
type ResponseHeadersStatus int

const (
	ResponseHeadersStatusContinue                  ResponseHeadersStatus = 0
	ResponseHeadersStatusStopIteration             ResponseHeadersStatus = 1
	ResponseHeadersStatusStopAllIterationAndBuffer ResponseHeadersStatus = 3
)

// ResponseBodyStatus is the return value of the HTTPFilter.ResponseBody event.
type ResponseBodyStatus int

const (
	ResponseBodyStatusContinue               ResponseBodyStatus = 0
	ResponseBodyStatusStopIterationAndBuffer ResponseBodyStatus = 1
)
