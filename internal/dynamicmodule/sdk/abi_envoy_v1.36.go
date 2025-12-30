// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build envoy

package sdk

// Following is a distillation of the Envoy ABI for dynamic modules:
// https://github.com/envoyproxy/envoy/blob/dc2d3098ae5641555f15c71d5bb5ce0060a8015c/source/extensions/dynamic_modules/abi.h
//
// Why not using the header file directly? That is because Go runtime complains
// about passing pointers to C code on the boundary. In the following code, we replace
// all the pointers with uintptr_t instread of *char. At the end of the day, what we
// need from the header is declarations of callbacks, not event hooks, so it won't be that hard to maintain.

/*
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

#cgo noescape envoy_dynamic_module_callback_log
#cgo nocallback envoy_dynamic_module_callback_log
void envoy_dynamic_module_callback_log(uintptr_t level, uintptr_t message_ptr, size_t message_length);

#cgo noescape envoy_dynamic_module_callback_log_enabled
#cgo nocallback envoy_dynamic_module_callback_log_enabled
bool envoy_dynamic_module_callback_log_enabled(uintptr_t level);

#cgo noescape envoy_dynamic_module_callback_http_get_request_header
#cgo nocallback envoy_dynamic_module_callback_http_get_request_header
size_t envoy_dynamic_module_callback_http_get_request_header(
    uintptr_t filter_envoy_ptr,
    uintptr_t key, size_t key_length,
    uintptr_t* result_buffer_ptr, size_t* result_buffer_length_ptr,
    size_t index);

#cgo noescape envoy_dynamic_module_callback_http_set_request_header
#cgo nocallback envoy_dynamic_module_callback_http_set_request_header
bool envoy_dynamic_module_callback_http_set_request_header(
    uintptr_t filter_envoy_ptr,
    uintptr_t key, size_t key_length,
    uintptr_t value, size_t value_length);

#cgo noescape envoy_dynamic_module_callback_http_get_response_header
#cgo nocallback envoy_dynamic_module_callback_http_get_response_header
size_t envoy_dynamic_module_callback_http_get_response_header(
    uintptr_t filter_envoy_ptr,
    uintptr_t key, size_t key_length,
    uintptr_t* result_buffer_ptr, size_t* result_buffer_length_ptr,
    size_t index);

#cgo noescape envoy_dynamic_module_callback_http_set_response_header
#cgo nocallback envoy_dynamic_module_callback_http_set_response_header
bool envoy_dynamic_module_callback_http_set_response_header(
    uintptr_t filter_envoy_ptr,
    uintptr_t key, size_t key_length,
    uintptr_t value, size_t value_length);

#cgo noescape envoy_dynamic_module_callback_http_append_buffered_request_body
#cgo nocallback envoy_dynamic_module_callback_http_append_buffered_request_body
bool envoy_dynamic_module_callback_http_append_buffered_request_body(
    uintptr_t filter_envoy_ptr,
    uintptr_t data, size_t length);

#cgo noescape envoy_dynamic_module_callback_http_drain_buffered_request_body
#cgo nocallback envoy_dynamic_module_callback_http_drain_buffered_request_body
bool envoy_dynamic_module_callback_http_drain_buffered_request_body(
	uintptr_t filter_envoy_ptr,
	size_t length);

#cgo noescape envoy_dynamic_module_callback_http_get_buffered_request_body_vector
#cgo nocallback envoy_dynamic_module_callback_http_get_buffered_request_body_vector
bool envoy_dynamic_module_callback_http_get_buffered_request_body_vector(
    uintptr_t filter_envoy_ptr,
    uintptr_t* result_buffer_vector);

#cgo noescape envoy_dynamic_module_callback_http_get_buffered_request_body_vector_size
#cgo nocallback envoy_dynamic_module_callback_http_get_buffered_request_body_vector_size
bool envoy_dynamic_module_callback_http_get_buffered_request_body_vector_size(
    uintptr_t filter_envoy_ptr, size_t* size);

#cgo noescape envoy_dynamic_module_callback_http_append_buffered_response_body
#cgo nocallback envoy_dynamic_module_callback_http_append_buffered_response_body
bool envoy_dynamic_module_callback_http_append_buffered_response_body(
    uintptr_t filter_envoy_ptr,
    uintptr_t data, size_t length);

#cgo noescape envoy_dynamic_module_callback_http_drain_buffered_response_body
#cgo nocallback envoy_dynamic_module_callback_http_drain_buffered_response_body
bool envoy_dynamic_module_callback_http_drain_buffered_response_body(
	uintptr_t filter_envoy_ptr,
	size_t length);

#cgo noescape envoy_dynamic_module_callback_http_get_buffered_response_body_vector
#cgo nocallback envoy_dynamic_module_callback_http_get_buffered_response_body_vector
bool envoy_dynamic_module_callback_http_get_buffered_response_body_vector(
    uintptr_t filter_envoy_ptr,
    uintptr_t* result_buffer_vector);

#cgo noescape envoy_dynamic_module_callback_http_get_buffered_response_body_vector_size
#cgo nocallback envoy_dynamic_module_callback_http_get_buffered_response_body_vector_size
bool envoy_dynamic_module_callback_http_get_buffered_response_body_vector_size(
    uintptr_t filter_envoy_ptr, size_t* size);

#cgo noescape envoy_dynamic_module_callback_http_get_received_request_body_vector
#cgo nocallback envoy_dynamic_module_callback_http_get_received_request_body_vector
bool envoy_dynamic_module_callback_http_get_received_request_body_vector(
    uintptr_t filter_envoy_ptr,
    uintptr_t* result_buffer_vector);

#cgo noescape envoy_dynamic_module_callback_http_get_received_request_body_vector_size
#cgo nocallback envoy_dynamic_module_callback_http_get_received_request_body_vector_size
bool envoy_dynamic_module_callback_http_get_received_request_body_vector_size(
    uintptr_t filter_envoy_ptr, size_t* size);

#cgo noescape envoy_dynamic_module_callback_http_append_received_request_body
#cgo nocallback envoy_dynamic_module_callback_http_append_received_request_body
bool envoy_dynamic_module_callback_http_append_received_request_body(
    uintptr_t filter_envoy_ptr,
    uintptr_t data, size_t length);

#cgo noescape envoy_dynamic_module_callback_http_drain_received_request_body
#cgo nocallback envoy_dynamic_module_callback_http_drain_received_request_body
bool envoy_dynamic_module_callback_http_drain_received_request_body(
	uintptr_t filter_envoy_ptr,
	size_t length);

#cgo noescape envoy_dynamic_module_callback_http_get_received_response_body_vector
#cgo nocallback envoy_dynamic_module_callback_http_get_received_response_body_vector
bool envoy_dynamic_module_callback_http_get_received_response_body_vector(
    uintptr_t filter_envoy_ptr,
    uintptr_t* result_buffer_vector);

#cgo noescape envoy_dynamic_module_callback_http_get_received_response_body_vector_size
#cgo nocallback envoy_dynamic_module_callback_http_get_received_response_body_vector_size
bool envoy_dynamic_module_callback_http_get_received_response_body_vector_size(
    uintptr_t filter_envoy_ptr, size_t* size);

#cgo noescape envoy_dynamic_module_callback_http_append_received_response_body
#cgo nocallback envoy_dynamic_module_callback_http_append_received_response_body
bool envoy_dynamic_module_callback_http_append_received_response_body(
    uintptr_t filter_envoy_ptr,
    uintptr_t data, size_t length);

#cgo noescape envoy_dynamic_module_callback_http_drain_received_response_body
#cgo nocallback envoy_dynamic_module_callback_http_drain_received_response_body
bool envoy_dynamic_module_callback_http_drain_received_response_body(
	uintptr_t filter_envoy_ptr,
	size_t length);

#cgo noescape envoy_dynamic_module_callback_http_send_response
// Uncomment once https://github.com/envoyproxy/envoy/pull/39206 is merged.
// #cgo nocallback envoy_dynamic_module_callback_http_send_response
void envoy_dynamic_module_callback_http_send_response(
    uintptr_t filter_envoy_ptr, uint32_t status_code,
    uintptr_t headers_vector, size_t headers_vector_size,
    uintptr_t body, size_t body_length);

#cgo noescape envoy_dynamic_module_callback_http_get_request_headers_count
#cgo nocallback envoy_dynamic_module_callback_http_get_request_headers_count
size_t envoy_dynamic_module_callback_http_get_request_headers_count(
	uintptr_t filter_envoy_ptr);

#cgo noescape envoy_dynamic_module_callback_http_get_request_headers
#cgo nocallback envoy_dynamic_module_callback_http_get_request_headers
bool envoy_dynamic_module_callback_http_get_request_headers(
    uintptr_t filter_envoy_ptr,
    uintptr_t* result_headers);

#cgo noescape envoy_dynamic_module_callback_http_get_response_headers_count
#cgo nocallback envoy_dynamic_module_callback_http_get_response_headers_count
size_t envoy_dynamic_module_callback_http_get_response_headers_count(
	uintptr_t filter_envoy_ptr);

#cgo noescape envoy_dynamic_module_callback_http_get_response_headers
#cgo nocallback envoy_dynamic_module_callback_http_get_response_headers
bool envoy_dynamic_module_callback_http_get_response_headers(
    uintptr_t filter_envoy_ptr,
    uintptr_t* result_headers);

#cgo noescape envoy_dynamic_module_callback_http_set_dynamic_metadata_string
#cgo nocallback envoy_dynamic_module_callback_http_set_dynamic_metadata_string
bool envoy_dynamic_module_callback_http_set_dynamic_metadata_string(
	uintptr_t filter_envoy_ptr,
	uintptr_t namespace_ptr, size_t namespace_size,
	uintptr_t key_ptr, size_t key_size,
	uintptr_t value_ptr, size_t value_size);

#cgo noescape envoy_dynamic_module_callback_http_set_dynamic_metadata_number
#cgo nocallback envoy_dynamic_module_callback_http_set_dynamic_metadata_number
bool envoy_dynamic_module_callback_http_set_dynamic_metadata_number(
	uintptr_t filter_envoy_ptr,
	uintptr_t namespace_ptr, size_t namespace_size,
	uintptr_t key_ptr, size_t key_size,
	double value_ptr);

#cgo noescape envoy_dynamic_module_callback_http_get_metadata_string
#cgo nocallback envoy_dynamic_module_callback_http_get_metadata_string
bool envoy_dynamic_module_callback_http_get_metadata_string(
	uintptr_t filter_envoy_ptr,
	uint32_t source,
	uintptr_t namespace_ptr, size_t namespace_size,
	uintptr_t key_ptr, size_t key_size,
	uintptr_t* result_ptr, size_t* result_size);

#cgo noescape envoy_dynamic_module_callback_http_clear_route_cache
#cgo nocallback envoy_dynamic_module_callback_http_clear_route_cache
void envoy_dynamic_module_callback_http_clear_route_cache(
	uintptr_t filter_envoy_ptr);

*/
import "C"

import (
	"io"
	"log/slog"
	"runtime"
	"unsafe"
)

// https://github.com/envoyproxy/envoy/blob/bad8280de85c25b147a90c1d9b8a8c67a13e7134/source/extensions/dynamic_modules/abi_version.h#L9C28-L9C92
var version = append([]byte("7ee559f16f35086fa7dc9ed380e2efc6b4d89031a001fb504aa71eccd25882f7"), 0)

func init() {
	logFunc = func(slevel slog.Level, message string) {
		var level logLevel
		switch slevel {
		case slog.LevelDebug:
			level = logLevelDebug
		case slog.LevelInfo:
			level = logLevelInfo
		case slog.LevelWarn:
			level = logLevelWarn
		case slog.LevelError:
			level = logLevelError
		}
		messagePtr := uintptr(unsafe.Pointer(unsafe.StringData(message)))
		C.envoy_dynamic_module_callback_log(
			C.uintptr_t(level),
			C.uintptr_t(messagePtr),
			C.size_t(len(message)),
		)
		runtime.KeepAlive(message)
	}
	switch {
	case bool(C.envoy_dynamic_module_callback_log_enabled(C.uintptr_t(logLevelTrace))):
		logLevelEnabledOnEnvoy = slog.LevelDebug
	case bool(C.envoy_dynamic_module_callback_log_enabled(C.uintptr_t(logLevelDebug))):
		logLevelEnabledOnEnvoy = slog.LevelDebug
	case bool(C.envoy_dynamic_module_callback_log_enabled(C.uintptr_t(logLevelInfo))):
		logLevelEnabledOnEnvoy = slog.LevelInfo
	case bool(C.envoy_dynamic_module_callback_log_enabled(C.uintptr_t(logLevelWarn))):
		logLevelEnabledOnEnvoy = slog.LevelWarn
	case bool(C.envoy_dynamic_module_callback_log_enabled(C.uintptr_t(logLevelError))):
		logLevelEnabledOnEnvoy = slog.LevelError
	default:
		logLevelEnabledOnEnvoy = slog.Level(100) // Disable all logging
	}
}

type logLevel int

const (
	logLevelTrace logLevel = iota
	logLevelDebug
	logLevelInfo
	logLevelWarn
	logLevelError
)

//export envoy_dynamic_module_on_program_init
func envoy_dynamic_module_on_program_init() uintptr {
	return uintptr(unsafe.Pointer(&version[0]))
}

//export envoy_dynamic_module_on_http_filter_config_new
func envoy_dynamic_module_on_http_filter_config_new(
	_ uintptr,
	namePtr *C.char,
	nameSize C.size_t,
	configPtr *C.char,
	configSize C.size_t,
) uintptr {
	name := C.GoStringN(namePtr, C.int(nameSize))
	config := C.GoBytes(unsafe.Pointer(configPtr), C.int(configSize))
	filterConfig := NewHTTPFilterConfig(name, config)
	if filterConfig == nil {
		return 0
	}
	// Pin the filter config to the memory manager.
	pinnedFilterConfig := memManager.pinHTTPFilterConfig(filterConfig)
	return uintptr(unsafe.Pointer(pinnedFilterConfig))
}

//export envoy_dynamic_module_on_http_filter_config_destroy
func envoy_dynamic_module_on_http_filter_config_destroy(ptr uintptr) {
	pinnedFilterConfig := unwrapPinnedHTTPFilterConfig(ptr)
	memManager.unpinHTTPFilterConfig(pinnedFilterConfig)
}

//export envoy_dynamic_module_on_http_filter_new
func envoy_dynamic_module_on_http_filter_new(
	filterConfigPtr,
	_ uintptr,
) uintptr {
	pinnedFilterConfig := unwrapPinnedHTTPFilterConfig(filterConfigPtr)
	filterConfig := pinnedFilterConfig.obj
	// Pin the filter to the memory manager.
	pinned := memManager.pinHTTPFilter(&pinedHTTPFilterItem{
		config: filterConfig,
	})
	// Return the pinned filter.
	return uintptr(unsafe.Pointer(pinned))
}

//export envoy_dynamic_module_on_http_filter_destroy
func envoy_dynamic_module_on_http_filter_destroy(
	filterPtr uintptr,
) {
	pinned := unwrapPinnedHTTPFilter(filterPtr)
	if f := pinned.obj.filter; f != nil {
		f.OnDestroy()
	}

	// Unpin the filter from the memory manager.
	memManager.unpinHTTPFilter(pinned)
}

//export envoy_dynamic_module_on_http_filter_request_headers
func envoy_dynamic_module_on_http_filter_request_headers(
	filterEnvoyPtr uintptr,
	filterModulePtr uintptr,
	endOfStream bool,
) uintptr {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pinned := unwrapPinnedHTTPFilter(filterModulePtr)
	f := pinned.obj.filter
	if f == nil {
		f = pinned.obj.config.NewFilter(envoyFilter{raw: filterEnvoyPtr})
		if f == nil {
			return 0
		}
		pinned.obj.filter = f
	}
	status := f.RequestHeaders(envoyFilter{raw: filterEnvoyPtr}, endOfStream)
	return uintptr(status)
}

//export envoy_dynamic_module_on_http_filter_request_body
func envoy_dynamic_module_on_http_filter_request_body(
	filterEnvoyPtr uintptr,
	filterModulePtr uintptr,
	endOfStream bool,
) uintptr {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pinned := unwrapPinnedHTTPFilter(filterModulePtr)
	status := pinned.obj.filter.RequestBody(envoyFilter{raw: filterEnvoyPtr}, endOfStream)
	return uintptr(status)
}

//export envoy_dynamic_module_on_http_filter_request_trailers
func envoy_dynamic_module_on_http_filter_request_trailers(uintptr, uintptr) uintptr {
	return 0
}

//export envoy_dynamic_module_on_http_filter_response_headers
func envoy_dynamic_module_on_http_filter_response_headers(
	filterEnvoyPtr uintptr,
	filterModulePtr uintptr,
	endOfStream bool,
) uintptr {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pinned := unwrapPinnedHTTPFilter(filterModulePtr)
	f := pinned.obj.filter
	if f == nil {
		f = pinned.obj.config.NewFilter(envoyFilter{raw: filterEnvoyPtr})
		if f == nil {
			return 0
		}
		pinned.obj.filter = f
	}
	status := f.ResponseHeaders(envoyFilter{raw: filterEnvoyPtr}, endOfStream)
	return uintptr(status)
}

//export envoy_dynamic_module_on_http_filter_response_body
func envoy_dynamic_module_on_http_filter_response_body(
	filterEnvoyPtr uintptr,
	filterModulePtr uintptr,
	endOfStream bool,
) uintptr {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	pinned := unwrapPinnedHTTPFilter(filterModulePtr)
	status := pinned.obj.filter.ResponseBody(envoyFilter{raw: filterEnvoyPtr}, endOfStream)
	return uintptr(status)
}

//export envoy_dynamic_module_on_http_filter_response_trailers
func envoy_dynamic_module_on_http_filter_response_trailers(uintptr, uintptr) uintptr {
	return 0
}

//export envoy_dynamic_module_on_http_filter_stream_complete
func envoy_dynamic_module_on_http_filter_stream_complete(uintptr, uintptr) {
}

//export envoy_dynamic_module_on_http_filter_http_callout_done
func envoy_dynamic_module_on_http_filter_http_callout_done(
	filterEnvoyPtr uintptr,
	filterModulePtr uintptr,
	calloutID C.uint32_t,
	result C.uint32_t,
	headersPtr uintptr,
	headersSize C.size_t,
	bodyVectorPtr uintptr,
	bodyVectorSize C.size_t,
) {
	_ = filterEnvoyPtr
	_ = filterModulePtr
	_ = calloutID
	_ = result
	_ = headersPtr
	_ = headersSize
	_ = bodyVectorPtr
	_ = bodyVectorSize
}

//export envoy_dynamic_module_on_http_filter_scheduled
func envoy_dynamic_module_on_http_filter_scheduled(
	filterEnvoyPtr uintptr,
	filterModulePtr uintptr,
	eventID C.uint64_t,
) {
	_ = filterEnvoyPtr
	_ = filterModulePtr
	_ = eventID
}

// GetRequestHeader implements [EnvoyHTTPFilter].
func (e envoyFilter) GetRequestHeader(key string) (string, bool) {
	keyPtr := uintptr(unsafe.Pointer(unsafe.StringData(key)))
	var resultBufferPtr *byte
	var resultBufferLengthPtr C.size_t

	ret := C.envoy_dynamic_module_callback_http_get_request_header(
		C.uintptr_t(e.raw),
		C.uintptr_t(keyPtr),
		C.size_t(len(key)),
		(*C.uintptr_t)(unsafe.Pointer(&resultBufferPtr)),
		(*C.size_t)(unsafe.Pointer(&resultBufferLengthPtr)),
		0,
	)

	if ret == 0 {
		return "", false
	}

	result := unsafe.Slice(resultBufferPtr, resultBufferLengthPtr)
	runtime.KeepAlive(key)
	return string(result), true
}

// GetResponseHeader implements [EnvoyHTTPFilter].
func (e envoyFilter) GetResponseHeader(key string) (string, bool) {
	keyPtr := uintptr(unsafe.Pointer(unsafe.StringData(key)))
	var resultBufferPtr *byte
	var resultBufferLengthPtr C.size_t

	ret := C.envoy_dynamic_module_callback_http_get_response_header(
		C.uintptr_t(e.raw),
		C.uintptr_t(keyPtr),
		C.size_t(len(key)),
		(*C.uintptr_t)(unsafe.Pointer(&resultBufferPtr)),
		(*C.size_t)(unsafe.Pointer(&resultBufferLengthPtr)),
		0,
	)

	if ret == 0 {
		return "", false
	}

	result := unsafe.Slice(resultBufferPtr, resultBufferLengthPtr)
	runtime.KeepAlive(key)
	return string(result), true
}

// SetRequestHeader implements [EnvoyHTTPFilter].
func (e envoyFilter) SetRequestHeader(key string, value []byte) bool {
	keyPtr := uintptr(unsafe.Pointer(unsafe.StringData(key)))
	valuePtr := uintptr(unsafe.Pointer(unsafe.SliceData(value)))

	ret := C.envoy_dynamic_module_callback_http_set_request_header(
		C.uintptr_t(e.raw),
		C.uintptr_t(keyPtr),
		C.size_t(len(key)),
		C.uintptr_t(valuePtr),
		C.size_t(len(value)),
	)

	runtime.KeepAlive(key)
	runtime.KeepAlive(value)
	return bool(ret)
}

// SetResponseHeader implements [EnvoyHTTPFilter].
func (e envoyFilter) SetResponseHeader(key string, value []byte) bool {
	keyPtr := uintptr(unsafe.Pointer(unsafe.StringData(key)))
	valuePtr := uintptr(unsafe.Pointer(unsafe.SliceData(value)))

	ret := C.envoy_dynamic_module_callback_http_set_response_header(
		C.uintptr_t(e.raw),
		C.uintptr_t(keyPtr),
		C.size_t(len(key)),
		C.uintptr_t(valuePtr),
		C.size_t(len(value)),
	)

	runtime.KeepAlive(key)
	runtime.KeepAlive(value)
	return bool(ret)
}

// bodyReader implements [io.Reader] for the request or response body.
type bodyReader struct {
	chunks        []envoySlice
	index, offset int
}

// Read implements [io.Reader].
func (b *bodyReader) Read(p []byte) (n int, err error) {
	for n < len(p) && b.index < len(b.chunks) {
		chunk := b.chunks[b.index]
		chunkData := unsafe.Slice((*byte)(unsafe.Pointer(chunk.data)), chunk.length)

		copied := copy(p[n:], chunkData[b.offset:])
		n += copied
		b.offset += copied

		if b.offset >= int(chunk.length) {
			b.index++
			b.offset = 0
		}
	}

	if n == 0 && b.index >= len(b.chunks) {
		return 0, io.EOF
	}

	return n, nil
}

func (b *bodyReader) WriteTo(w io.Writer) (n int64, err error) {
	for b.index < len(b.chunks) {
		chunk := b.chunks[b.index]
		data := unsafe.Slice((*byte)(unsafe.Pointer(chunk.data)), chunk.length)[b.offset:]
		m, err := w.Write(data)
		n += int64(m)
		if err != nil {
			return n, err
		}
		b.index++
	}
	return n, nil
}

// Len implements [BodyReader].
//
// This returns the length of the body in bytes, regardless of how much has been read.
func (b *bodyReader) Len() int {
	total := 0
	for _, chunk := range b.chunks {
		total += int(chunk.length)
	}
	return total
}

type envoySlice struct {
	data   uintptr
	length C.size_t
}

// envoyFilter implements [EnvoyHTTPFilter].
type envoyFilter struct{ raw uintptr }

// GetRequestHeaders implements EnvoyHTTPFilter.
func (e envoyFilter) GetRequestHeaders() map[string][]string {
	count := C.envoy_dynamic_module_callback_http_get_request_headers_count(C.uintptr_t(e.raw))
	raw := make([][2]envoySlice, count)
	ret := C.envoy_dynamic_module_callback_http_get_request_headers(
		C.uintptr_t(e.raw),
		(*C.uintptr_t)(unsafe.Pointer(&raw[0])),
	)
	if !ret {
		return nil
	}
	// Copy the headers to a Go slice.
	headers := make(map[string][]string, count) // The count is the number of (key, value) pairs, so this might be larger than the number of unique names.
	for i := range count {
		// Copy the Envoy owner data to a Go string.
		key := string(unsafe.Slice((*byte)(unsafe.Pointer(raw[i][0].data)), raw[i][0].length))
		value := string(unsafe.Slice((*byte)(unsafe.Pointer(raw[i][1].data)), raw[i][1].length))
		headers[key] = append(headers[key], value)
	}
	return headers
}

// GetResponseHeaders implements [EnvoyHTTPFilter].
func (e envoyFilter) GetResponseHeaders() map[string][]string {
	count := C.envoy_dynamic_module_callback_http_get_response_headers_count(C.uintptr_t(e.raw))
	raw := make([][2]envoySlice, count)
	ret := C.envoy_dynamic_module_callback_http_get_response_headers(
		C.uintptr_t(e.raw),
		(*C.uintptr_t)(unsafe.Pointer(&raw[0])),
	)
	if !ret {
		return nil
	}
	// Copy the headers to a Go slice.
	headers := make(map[string][]string, count) // The count is the number of (key, value) pairs, so this might be larger than the number of unique names.
	for i := range count {
		// Copy the Envoy owner data to a Go string.
		key := string(unsafe.Slice((*byte)(unsafe.Pointer(raw[i][0].data)), raw[i][0].length))
		value := string(unsafe.Slice((*byte)(unsafe.Pointer(raw[i][1].data)), raw[i][1].length))
		headers[key] = append(headers[key], value)
	}
	return headers
}

// SendLocalReply implements EnvoyHTTPFilter.
func (e envoyFilter) SendLocalReply(statusCode uint32, headers [][2]string, body []byte) {
	headersVecPtr := uintptr(unsafe.Pointer(unsafe.SliceData(headers)))
	headersVecSize := len(headers)
	bodyPtr := uintptr(unsafe.Pointer(unsafe.SliceData(body)))
	bodySize := len(body)
	C.envoy_dynamic_module_callback_http_send_response(
		C.uintptr_t(e.raw),
		C.uint32_t(statusCode),
		C.uintptr_t(headersVecPtr),
		C.size_t(headersVecSize),
		C.uintptr_t(bodyPtr),
		C.size_t(bodySize),
	)
	runtime.KeepAlive(headers)
	runtime.KeepAlive(body)
}

// AppendBufferedRequestBody implements [EnvoyHTTPFilter].
func (e envoyFilter) AppendBufferedRequestBody(data []byte) bool {
	dataPtr := uintptr(unsafe.Pointer(unsafe.SliceData(data)))
	ret := C.envoy_dynamic_module_callback_http_append_buffered_request_body(
		C.uintptr_t(e.raw),
		C.uintptr_t(dataPtr),
		C.size_t(len(data)),
	)
	runtime.KeepAlive(data)
	return bool(ret)
}

// DrainBufferedRequestBody implements [EnvoyHTTPFilter].
func (e envoyFilter) DrainBufferedRequestBody(n int) bool {
	if n == 0 {
		return true
	}
	ret := C.envoy_dynamic_module_callback_http_drain_buffered_request_body(
		C.uintptr_t(e.raw),
		C.size_t(n),
	)
	return bool(ret)
}

// GetBufferedRequestBody implements [EnvoyHTTPFilter].
func (e envoyFilter) GetBufferedRequestBody() (BodyReader, bool) {
	var vectorSize int
	ret := C.envoy_dynamic_module_callback_http_get_buffered_request_body_vector_size(
		C.uintptr_t(e.raw),
		(*C.size_t)(unsafe.Pointer(&vectorSize)),
	)
	if !ret {
		return nil, false
	} else if vectorSize == 0 {
		return &bodyReader{chunks: []envoySlice{}}, true
	}

	chunks := make([]envoySlice, vectorSize)
	ret = C.envoy_dynamic_module_callback_http_get_buffered_request_body_vector(
		C.uintptr_t(e.raw),
		(*C.uintptr_t)(unsafe.Pointer(&chunks[0])),
	)
	if !ret {
		return nil, false
	}
	return &bodyReader{chunks: chunks}, true
}

// AppendBufferedResponseBody implements [EnvoyHTTPFilter].
func (e envoyFilter) AppendBufferedResponseBody(data []byte) bool {
	dataPtr := uintptr(unsafe.Pointer(unsafe.SliceData(data)))
	ret := C.envoy_dynamic_module_callback_http_append_buffered_response_body(
		C.uintptr_t(e.raw),
		C.uintptr_t(dataPtr),
		C.size_t(len(data)),
	)
	runtime.KeepAlive(data)
	return bool(ret)
}

// DrainBufferedResponseBody implements [EnvoyHTTPFilter].
func (e envoyFilter) DrainBufferedResponseBody(n int) bool {
	if n == 0 {
		return true
	}
	ret := C.envoy_dynamic_module_callback_http_drain_buffered_response_body(
		C.uintptr_t(e.raw),
		C.size_t(n),
	)
	return bool(ret)
}

// GetBufferedResponseBody implements [EnvoyHTTPFilter].
func (e envoyFilter) GetBufferedResponseBody() (BodyReader, bool) {
	var vectorSize int
	ret := C.envoy_dynamic_module_callback_http_get_buffered_response_body_vector_size(
		C.uintptr_t(e.raw),
		(*C.size_t)(unsafe.Pointer(&vectorSize)),
	)
	if !ret {
		return nil, false
	} else if vectorSize == 0 {
		return &bodyReader{chunks: []envoySlice{}}, true
	}
	chunks := make([]envoySlice, vectorSize)
	ret = C.envoy_dynamic_module_callback_http_get_buffered_response_body_vector(
		C.uintptr_t(e.raw),
		(*C.uintptr_t)(unsafe.Pointer(&chunks[0])),
	)
	if !ret {
		return nil, false
	}
	return &bodyReader{chunks: chunks}, true
}

// AppendReceivedRequestBody implements [EnvoyHTTPFilter].
func (e envoyFilter) AppendReceivedRequestBody(data []byte) bool {
	dataPtr := uintptr(unsafe.Pointer(unsafe.SliceData(data)))
	ret := C.envoy_dynamic_module_callback_http_append_received_request_body(
		C.uintptr_t(e.raw),
		C.uintptr_t(dataPtr),
		C.size_t(len(data)),
	)
	runtime.KeepAlive(data)
	return bool(ret)
}

// DrainReceivedRequestBody implements [EnvoyHTTPFilter].
func (e envoyFilter) DrainReceivedRequestBody(n int) bool {
	if n == 0 {
		return true
	}
	ret := C.envoy_dynamic_module_callback_http_drain_received_request_body(
		C.uintptr_t(e.raw),
		C.size_t(n),
	)
	return bool(ret)
}

// GetReceivedRequestBody implements [EnvoyHTTPFilter].
func (e envoyFilter) GetReceivedRequestBody() (BodyReader, bool) {
	var vectorSize int
	ret := C.envoy_dynamic_module_callback_http_get_received_request_body_vector_size(
		C.uintptr_t(e.raw),
		(*C.size_t)(unsafe.Pointer(&vectorSize)),
	)
	if !ret {
		return nil, false
	} else if vectorSize == 0 {
		return &bodyReader{chunks: []envoySlice{}}, true
	}

	chunks := make([]envoySlice, vectorSize)
	ret = C.envoy_dynamic_module_callback_http_get_received_request_body_vector(
		C.uintptr_t(e.raw),
		(*C.uintptr_t)(unsafe.Pointer(&chunks[0])),
	)
	if !ret {
		return nil, false
	}
	return &bodyReader{chunks: chunks}, true
}

// AppendReceivedResponseBody implements [EnvoyHTTPFilter].
func (e envoyFilter) AppendReceivedResponseBody(data []byte) bool {
	dataPtr := uintptr(unsafe.Pointer(unsafe.SliceData(data)))
	ret := C.envoy_dynamic_module_callback_http_append_received_response_body(
		C.uintptr_t(e.raw),
		C.uintptr_t(dataPtr),
		C.size_t(len(data)),
	)
	runtime.KeepAlive(data)
	return bool(ret)
}

// DrainReceivedResponseBody implements [EnvoyHTTPFilter].
func (e envoyFilter) DrainReceivedResponseBody(n int) bool {
	if n == 0 {
		return true
	}
	ret := C.envoy_dynamic_module_callback_http_drain_received_response_body(
		C.uintptr_t(e.raw),
		C.size_t(n),
	)
	return bool(ret)
}

// GetReceivedResponseBody implements [EnvoyHTTPFilter].
func (e envoyFilter) GetReceivedResponseBody() (BodyReader, bool) {
	var vectorSize int
	ret := C.envoy_dynamic_module_callback_http_get_received_response_body_vector_size(
		C.uintptr_t(e.raw),
		(*C.size_t)(unsafe.Pointer(&vectorSize)),
	)
	if !ret {
		return nil, false
	} else if vectorSize == 0 {
		return &bodyReader{chunks: []envoySlice{}}, true
	}
	chunks := make([]envoySlice, vectorSize)
	ret = C.envoy_dynamic_module_callback_http_get_received_response_body_vector(
		C.uintptr_t(e.raw),
		(*C.uintptr_t)(unsafe.Pointer(&chunks[0])),
	)
	if !ret {
		return nil, false
	}
	return &bodyReader{chunks: chunks}, true
}

// https://github.com/envoyproxy/envoy/blob/dc2d3098ae5641555f15c71d5bb5ce0060a8015c/source/extensions/dynamic_modules/abi.h#L271-L282
type metadataSource uint32

const (
	metadataSourceDynamic      metadataSource = 0
	metadataSourceUpstreamHost metadataSource = 3
)

// GetDynamicMetadataString implements [EnvoyHTTPFilter].
func (e envoyFilter) GetDynamicMetadataString(namespace string, key string) (string, bool) {
	return e.getMetadataString(namespace, key, metadataSourceDynamic)
}

// GetUpstreamHostMetadataString implements [EnvoyHTTPFilter].
func (e envoyFilter) GetUpstreamHostMetadataString(namespace string, key string) (string, bool) {
	return e.getMetadataString(namespace, key, metadataSourceUpstreamHost)
}

func (e envoyFilter) getMetadataString(namespace string, key string, source metadataSource) (string, bool) {
	namespacePtr := uintptr(unsafe.Pointer(unsafe.StringData(namespace)))
	keyPtr := uintptr(unsafe.Pointer(unsafe.StringData(key)))
	var resultBufferPtr *byte
	var resultBufferLengthPtr C.size_t

	ret := C.envoy_dynamic_module_callback_http_get_metadata_string(
		C.uintptr_t(e.raw),
		C.uint32_t(source),
		C.uintptr_t(namespacePtr),
		C.size_t(len(namespace)),
		C.uintptr_t(keyPtr),
		C.size_t(len(key)),
		(*C.uintptr_t)(unsafe.Pointer(&resultBufferPtr)),
		(*C.size_t)(unsafe.Pointer(&resultBufferLengthPtr)),
	)

	if !ret {
		return "", false
	}

	result := unsafe.Slice(resultBufferPtr, resultBufferLengthPtr)
	runtime.KeepAlive(namespace)
	runtime.KeepAlive(key)
	return string(result), true
}

// SetDynamicMetadataString implements [EnvoyHTTPFilter].
func (e envoyFilter) SetDynamicMetadataString(namespace string, key string, value string) bool {
	namespacePtr := uintptr(unsafe.Pointer(unsafe.StringData(namespace)))
	keyPtr := uintptr(unsafe.Pointer(unsafe.StringData(key)))
	valuePtr := uintptr(unsafe.Pointer(unsafe.StringData(value)))

	ret := C.envoy_dynamic_module_callback_http_set_dynamic_metadata_string(
		C.uintptr_t(e.raw),
		C.uintptr_t(namespacePtr),
		C.size_t(len(namespace)),
		C.uintptr_t(keyPtr),
		C.size_t(len(key)),
		C.uintptr_t(valuePtr),
		C.size_t(len(value)),
	)
	runtime.KeepAlive(namespace)
	runtime.KeepAlive(key)
	runtime.KeepAlive(value)
	return bool(ret)
}

// SetDynamicMetadataNumber implements [EnvoyHTTPFilter].
func (e envoyFilter) SetDynamicMetadataNumber(namespace string, key string, value float64) bool {
	namespacePtr := uintptr(unsafe.Pointer(unsafe.StringData(namespace)))
	keyPtr := uintptr(unsafe.Pointer(unsafe.StringData(key)))
	ret := C.envoy_dynamic_module_callback_http_set_dynamic_metadata_number(
		C.uintptr_t(e.raw),
		C.uintptr_t(namespacePtr),
		C.size_t(len(namespace)),
		C.uintptr_t(keyPtr),
		C.size_t(len(key)),
		C.double(value),
	)
	runtime.KeepAlive(namespace)
	runtime.KeepAlive(key)
	return bool(ret)
}

// ClearRouteCache implements [EnvoyHTTPFilter].
func (e envoyFilter) ClearRouteCache() {
	C.envoy_dynamic_module_callback_http_clear_route_cache(
		C.uintptr_t(e.raw),
	)
}
