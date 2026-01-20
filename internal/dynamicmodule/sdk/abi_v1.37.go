// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

//go:build envoy && cgo

package sdk

// Following is a distillation of the Envoy ABI for dynamic modules:
// https://github.com/envoyproxy/envoy/blob/v1.37.0/source/extensions/dynamic_modules/abi.h
//
// Why not using the header file directly? That is because Go runtime complains
// about passing pointers to C code on the boundary. In the following code, we replace
// all the pointers with uintptr_t instread of *char. At the end of the day, what we
// need from the header is declarations of callbacks, not event hooks, so it won't be that hard to maintain.

/*
#include <stdbool.h>
#include <stddef.h>
#include <stdint.h>

typedef enum {
    envoy_dynamic_module_type_http_header_type_RequestHeader = 0,
    envoy_dynamic_module_type_http_header_type_RequestTrailer = 1,
    envoy_dynamic_module_type_http_header_type_ResponseHeader = 2,
    envoy_dynamic_module_type_http_header_type_ResponseTrailer = 3,
} envoy_dynamic_module_type_http_header_type;

typedef struct {
    uintptr_t ptr;
    size_t length;
} envoy_dynamic_module_type_envoy_buffer;

typedef struct {
    uintptr_t ptr;
    size_t length;
} envoy_dynamic_module_type_module_buffer;

typedef enum {
    envoy_dynamic_module_type_http_body_type_ReceivedRequestBody,
    envoy_dynamic_module_type_http_body_type_BufferedRequestBody,
    envoy_dynamic_module_type_http_body_type_ReceivedResponseBody,
    envoy_dynamic_module_type_http_body_type_BufferedResponseBody,
} envoy_dynamic_module_type_http_body_type;

#cgo noescape envoy_dynamic_module_callback_http_get_header
#cgo nocallback envoy_dynamic_module_callback_http_get_header
bool envoy_dynamic_module_callback_http_get_header(
    uintptr_t filter_envoy_ptr,
    int header_type,
    envoy_dynamic_module_type_module_buffer key,
    envoy_dynamic_module_type_envoy_buffer* result_buffer,
    size_t index,
    size_t* optional_size);

#cgo noescape envoy_dynamic_module_callback_http_set_header
#cgo nocallback envoy_dynamic_module_callback_http_set_header
bool envoy_dynamic_module_callback_http_set_header(
    uintptr_t filter_envoy_ptr,
    int header_type,
    envoy_dynamic_module_type_module_buffer key,
    envoy_dynamic_module_type_module_buffer value);

#cgo noescape envoy_dynamic_module_callback_http_append_body
#cgo nocallback envoy_dynamic_module_callback_http_append_body
bool envoy_dynamic_module_callback_http_append_body(
    uintptr_t filter_envoy_ptr,
    int body_type,
    envoy_dynamic_module_type_module_buffer data);

#cgo noescape envoy_dynamic_module_callback_http_drain_body
#cgo nocallback envoy_dynamic_module_callback_http_drain_body
bool envoy_dynamic_module_callback_http_drain_body(
    uintptr_t filter_envoy_ptr,
    int body_type,
    size_t number_of_bytes);

#cgo noescape envoy_dynamic_module_callback_http_get_body_chunks
#cgo nocallback envoy_dynamic_module_callback_http_get_body_chunks
bool envoy_dynamic_module_callback_http_get_body_chunks(
    uintptr_t filter_envoy_ptr,
    int body_type,
    envoy_dynamic_module_type_envoy_buffer* result_buffer_vector);

#cgo noescape envoy_dynamic_module_callback_http_get_body_chunks_size
#cgo nocallback envoy_dynamic_module_callback_http_get_body_chunks_size
size_t envoy_dynamic_module_callback_http_get_body_chunks_size(
    uintptr_t filter_envoy_ptr,
    int body_type);

#cgo noescape envoy_dynamic_module_callback_http_send_response
// Uncomment once https://github.com/envoyproxy/envoy/pull/39206 is merged.
// #cgo nocallback envoy_dynamic_module_callback_http_send_response
void envoy_dynamic_module_callback_http_send_response(
    uintptr_t filter_envoy_ptr, uint32_t status_code,
    uintptr_t headers_vector, size_t headers_vector_size,
    envoy_dynamic_module_type_module_buffer body,
    envoy_dynamic_module_type_module_buffer details);

typedef struct {
    uintptr_t key_ptr;
    size_t key_length;
    uintptr_t value_ptr;
    size_t value_length;
} envoy_dynamic_module_type_envoy_http_header;

#cgo noescape envoy_dynamic_module_callback_http_get_headers_size
#cgo nocallback envoy_dynamic_module_callback_http_get_headers_size
size_t envoy_dynamic_module_callback_http_get_headers_size(
    uintptr_t filter_envoy_ptr,
    int header_type);

#cgo noescape envoy_dynamic_module_callback_http_get_headers
#cgo nocallback envoy_dynamic_module_callback_http_get_headers
bool envoy_dynamic_module_callback_http_get_headers(
    uintptr_t filter_envoy_ptr,
    int header_type,
    envoy_dynamic_module_type_envoy_http_header* result_headers);

#cgo noescape envoy_dynamic_module_callback_http_clear_route_cache
#cgo nocallback envoy_dynamic_module_callback_http_clear_route_cache
void envoy_dynamic_module_callback_http_clear_route_cache(
	uintptr_t filter_envoy_ptr);

#cgo noescape envoy_dynamic_module_callback_http_get_metadata_string
#cgo nocallback envoy_dynamic_module_callback_http_get_metadata_string
bool envoy_dynamic_module_callback_http_get_metadata_string(
	uintptr_t filter_envoy_ptr,
	uint32_t source,
	uintptr_t namespace_ptr, size_t namespace_size,
	uintptr_t key_ptr, size_t key_size,
	envoy_dynamic_module_type_envoy_buffer* result);

#cgo noescape envoy_dynamic_module_callback_http_set_dynamic_metadata_string
#cgo nocallback envoy_dynamic_module_callback_http_set_dynamic_metadata_string
void envoy_dynamic_module_callback_http_set_dynamic_metadata_string(
	uintptr_t filter_envoy_ptr,
	envoy_dynamic_module_type_module_buffer ns,
	envoy_dynamic_module_type_module_buffer key,
	envoy_dynamic_module_type_module_buffer value);

#cgo noescape envoy_dynamic_module_callback_http_set_dynamic_metadata_number
#cgo nocallback envoy_dynamic_module_callback_http_set_dynamic_metadata_number
void envoy_dynamic_module_callback_http_set_dynamic_metadata_number(
	uintptr_t filter_envoy_ptr,
	uintptr_t namespace_ptr, size_t namespace_size,
	uintptr_t key_ptr, size_t key_size,
	double value_ptr);

#cgo noescape envoy_dynamic_module_callback_log
#cgo nocallback envoy_dynamic_module_callback_log
void envoy_dynamic_module_callback_log(uintptr_t level, uintptr_t message_ptr, size_t message_length);

#cgo noescape envoy_dynamic_module_callback_log_enabled
#cgo nocallback envoy_dynamic_module_callback_log_enabled
bool envoy_dynamic_module_callback_log_enabled(uintptr_t level);
*/
import "C"

import (
	"io"
	"log/slog"
	"runtime"
	"unsafe"
)

var version = append([]byte("4dae397a7c9ff0238d318d57ea656ce8b3fbff595787dcd7ee2ff5b79c9fe10f"), 0)

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
	filterConfigPtr uintptr,
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
	eventID uint64,
) {
	_ = filterEnvoyPtr
	_ = filterModulePtr
	_ = eventID
}

//export envoy_dynamic_module_on_http_filter_http_stream_reset
func envoy_dynamic_module_on_http_filter_http_stream_reset(
	filterEnvoyPtr uintptr,
	filterModulePtr uintptr,
	streamID uint64,
	reason uint32,
) {
}

//export envoy_dynamic_module_on_http_filter_http_stream_data
func envoy_dynamic_module_on_http_filter_http_stream_data(
	filterEnvoyPtr uintptr,
	filterModulePtr uintptr,
	streamID uint64,
	dataPtr uintptr,
	dataCount uint64,
	endStream bool,
) {
}

//export envoy_dynamic_module_on_http_filter_http_stream_trailers
func envoy_dynamic_module_on_http_filter_http_stream_trailers(
	filterEnvoyPtr uintptr,
	filterModulePtr uintptr,
	streamID uint64,
	trailersPtr uintptr,
	trailersSize uint64,
) {
}

//export envoy_dynamic_module_on_http_filter_http_stream_complete
func envoy_dynamic_module_on_http_filter_http_stream_complete(
	filterEnvoyPtr uintptr,
	filterModulePtr uintptr,
	streamID uint64,
) {
}

//export envoy_dynamic_module_on_http_filter_config_scheduled
func envoy_dynamic_module_on_http_filter_config_scheduled(
	filterConfigEnvoyPtr uintptr,
	filterConfigPtr uintptr,
	eventID uint64,
) {
}

//export envoy_dynamic_module_on_http_filter_downstream_above_write_buffer_high_watermark
func envoy_dynamic_module_on_http_filter_downstream_above_write_buffer_high_watermark(
	filterEnvoyPtr uintptr,
	filterModulePtr uintptr,
) {
}

//export envoy_dynamic_module_on_http_filter_downstream_below_write_buffer_low_watermark
func envoy_dynamic_module_on_http_filter_downstream_below_write_buffer_low_watermark(
	filterEnvoyPtr uintptr,
	filterModulePtr uintptr,
) {
}

//export envoy_dynamic_module_on_http_filter_http_stream_headers
func envoy_dynamic_module_on_http_filter_http_stream_headers(
	filterEnvoyPtr uintptr,
	filterModulePtr uintptr,
	streamID uint64,
	headersPtr uintptr,
	headersSize uint64,
	endStream bool,
) {
}

// GetRequestHeader implements [EnvoyHttpFilter].
func (e envoyFilter) GetRequestHeader(key string) (string, bool) {
	keyBuf := C.envoy_dynamic_module_type_module_buffer{
		ptr:    C.uintptr_t(uintptr(unsafe.Pointer(unsafe.StringData(key)))),
		length: C.size_t(len(key)),
	}
	var resultBuf C.envoy_dynamic_module_type_envoy_buffer

	ret := C.envoy_dynamic_module_callback_http_get_header(
		C.uintptr_t(e.raw),
		C.int(0), // RequestHeader
		keyBuf,
		&resultBuf,
		0,
		nil,
	)

	if !ret {
		return "", false
	}

	result := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(resultBuf.ptr))), resultBuf.length)
	runtime.KeepAlive(key)
	return string(result), true
}

// GetResponseHeader implements [EnvoyHttpFilter].
func (e envoyFilter) GetResponseHeader(key string) (string, bool) {
	keyBuf := C.envoy_dynamic_module_type_module_buffer{
		ptr:    C.uintptr_t(uintptr(unsafe.Pointer(unsafe.StringData(key)))),
		length: C.size_t(len(key)),
	}
	var resultBuf C.envoy_dynamic_module_type_envoy_buffer

	ret := C.envoy_dynamic_module_callback_http_get_header(
		C.uintptr_t(e.raw),
		C.int(2), // ResponseHeader
		keyBuf,
		&resultBuf,
		0,
		nil,
	)

	if !ret {
		return "", false
	}

	result := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(resultBuf.ptr))), resultBuf.length)
	runtime.KeepAlive(key)
	return string(result), true
}

// SetRequestHeader implements [EnvoyHttpFilter].
func (e envoyFilter) SetRequestHeader(key string, value []byte) bool {
	keyBuf := C.envoy_dynamic_module_type_module_buffer{
		ptr:    C.uintptr_t(uintptr(unsafe.Pointer(unsafe.StringData(key)))),
		length: C.size_t(len(key)),
	}
	valueBuf := C.envoy_dynamic_module_type_module_buffer{
		ptr:    C.uintptr_t(uintptr(unsafe.Pointer(unsafe.SliceData(value)))),
		length: C.size_t(len(value)),
	}

	ret := C.envoy_dynamic_module_callback_http_set_header(
		C.uintptr_t(e.raw),
		C.int(0), // RequestHeader
		keyBuf,
		valueBuf,
	)

	runtime.KeepAlive(key)
	runtime.KeepAlive(value)
	return bool(ret)
}

// SetResponseHeader implements [EnvoyHttpFilter].
func (e envoyFilter) SetResponseHeader(key string, value []byte) bool {
	keyBuf := C.envoy_dynamic_module_type_module_buffer{
		ptr:    C.uintptr_t(uintptr(unsafe.Pointer(unsafe.StringData(key)))),
		length: C.size_t(len(key)),
	}
	valueBuf := C.envoy_dynamic_module_type_module_buffer{
		ptr:    C.uintptr_t(uintptr(unsafe.Pointer(unsafe.SliceData(value)))),
		length: C.size_t(len(value)),
	}

	ret := C.envoy_dynamic_module_callback_http_set_header(
		C.uintptr_t(e.raw),
		C.int(2), // ResponseHeader
		keyBuf,
		valueBuf,
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

// envoyFilter implements [EnvoyHttpFilter].
type envoyFilter struct{ raw uintptr }

// GetRequestHeaders implements EnvoyHttpFilter.
func (e envoyFilter) GetRequestHeaders() map[string][]string {
	count := C.envoy_dynamic_module_callback_http_get_headers_size(
		C.uintptr_t(e.raw),
		C.int(0), // RequestHeader
	)
	raw := make([]C.envoy_dynamic_module_type_envoy_http_header, count)
	ret := C.envoy_dynamic_module_callback_http_get_headers(
		C.uintptr_t(e.raw),
		C.int(0), // RequestHeader
		&raw[0],
	)
	if !ret {
		return nil
	}
	// Copy the headers to a Go slice.
	headers := make(map[string][]string, count) // The count is the number of (key, value) pairs, so this might be larger than the number of unique names.
	for i := range count {
		// Copy the Envoy owner data to a Go string.
		key := string(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(raw[i].key_ptr))), raw[i].key_length))
		value := string(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(raw[i].value_ptr))), raw[i].value_length))
		headers[key] = append(headers[key], value)
	}
	return headers
}

// GetResponseHeaders implements [EnvoyHttpFilter].
func (e envoyFilter) GetResponseHeaders() map[string][]string {
	count := C.envoy_dynamic_module_callback_http_get_headers_size(
		C.uintptr_t(e.raw),
		C.int(2), // ResponseHeader
	)
	raw := make([]C.envoy_dynamic_module_type_envoy_http_header, count)
	ret := C.envoy_dynamic_module_callback_http_get_headers(
		C.uintptr_t(e.raw),
		C.int(2), // ResponseHeader
		&raw[0],
	)
	if !ret {
		return nil
	}
	// Copy the headers to a Go slice.
	headers := make(map[string][]string, count) // The count is the number of (key, value) pairs, so this might be larger than the number of unique names.
	for i := range count {
		// Copy the Envoy owner data to a Go string.
		key := string(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(raw[i].key_ptr))), raw[i].key_length))
		value := string(unsafe.Slice((*byte)(unsafe.Pointer(uintptr(raw[i].value_ptr))), raw[i].value_length))
		headers[key] = append(headers[key], value)
	}
	return headers
}

// SendLocalReply implements EnvoyHttpFilter.
func (e envoyFilter) SendLocalReply(statusCode uint32, headers [][2]string, body []byte) {
	headersVecPtr := uintptr(unsafe.Pointer(unsafe.SliceData(headers)))
	headersVecSize := len(headers)
	bodyBuf := C.envoy_dynamic_module_type_module_buffer{
		ptr:    C.uintptr_t(uintptr(unsafe.Pointer(unsafe.SliceData(body)))),
		length: C.size_t(len(body)),
	}
	// Empty details buffer (v1.37 addition)
	detailsBuf := C.envoy_dynamic_module_type_module_buffer{
		ptr:    C.uintptr_t(0),
		length: C.size_t(0),
	}
	C.envoy_dynamic_module_callback_http_send_response(
		C.uintptr_t(e.raw),
		C.uint32_t(statusCode),
		C.uintptr_t(headersVecPtr),
		C.size_t(headersVecSize),
		bodyBuf,
		detailsBuf,
	)
	runtime.KeepAlive(headers)
	runtime.KeepAlive(body)
}

// GetBufferedRequestBody implements [EnvoyHttpFilter].
func (e envoyFilter) GetBufferedRequestBody() (BodyReader, bool) {
	return e.getBody(C.int(C.envoy_dynamic_module_type_http_body_type_BufferedRequestBody))
}

// GetReceivedRequestBody implements [EnvoyHttpFilter].
func (e envoyFilter) GetReceivedRequestBody() (BodyReader, bool) {
	return e.getBody(C.int(C.envoy_dynamic_module_type_http_body_type_ReceivedRequestBody))
}

// GetBufferedResponseBody implements [EnvoyHttpFilter].
func (e envoyFilter) GetBufferedResponseBody() (BodyReader, bool) {
	return e.getBody(C.int(C.envoy_dynamic_module_type_http_body_type_BufferedResponseBody))
}

// GetReceivedResponseBody implements [EnvoyHttpFilter].
func (e envoyFilter) GetReceivedResponseBody() (BodyReader, bool) {
	return e.getBody(C.int(C.envoy_dynamic_module_type_http_body_type_ReceivedResponseBody))
}

// AppendBufferedRequestBody implements [EnvoyHttpFilter].
func (e envoyFilter) AppendBufferedRequestBody(data []byte) bool {
	return e.appendBody(data, C.int(C.envoy_dynamic_module_type_http_body_type_BufferedRequestBody))
}

// AppendReceivedRequestBody implements [EnvoyHttpFilter].
func (e envoyFilter) AppendReceivedRequestBody(data []byte) bool {
	return e.appendBody(data, C.int(C.envoy_dynamic_module_type_http_body_type_ReceivedRequestBody))
}

// AppendBufferedResponseBody implements [EnvoyHttpFilter].
func (e envoyFilter) AppendBufferedResponseBody(data []byte) bool {
	return e.appendBody(data, C.int(C.envoy_dynamic_module_type_http_body_type_BufferedResponseBody))
}

// AppendReceivedResponseBody implements [EnvoyHttpFilter].
func (e envoyFilter) AppendReceivedResponseBody(data []byte) bool {
	return e.appendBody(data, C.int(C.envoy_dynamic_module_type_http_body_type_ReceivedResponseBody))
}

// DrainBufferedRequestBody implements [EnvoyHttpFilter].
func (e envoyFilter) DrainBufferedRequestBody(n int) bool {
	return e.drainBody(n, C.int(C.envoy_dynamic_module_type_http_body_type_BufferedRequestBody))
}

// DrainReceivedRequestBody implements [EnvoyHttpFilter].
func (e envoyFilter) DrainReceivedRequestBody(n int) bool {
	return e.drainBody(n, C.int(C.envoy_dynamic_module_type_http_body_type_ReceivedRequestBody))
}

// DrainBufferedResponseBody implements [EnvoyHttpFilter].
func (e envoyFilter) DrainBufferedResponseBody(n int) bool {
	return e.drainBody(n, C.int(C.envoy_dynamic_module_type_http_body_type_BufferedResponseBody))
}

// DrainReceivedResponseBody implements [EnvoyHttpFilter].
func (e envoyFilter) DrainReceivedResponseBody(n int) bool {
	return e.drainBody(n, C.int(C.envoy_dynamic_module_type_http_body_type_ReceivedResponseBody))
}

func (e envoyFilter) getBody(bodyType C.int) (BodyReader, bool) {
	vectorSize := C.envoy_dynamic_module_callback_http_get_body_chunks_size(
		C.uintptr_t(e.raw),
		bodyType,
	)
	if vectorSize == 0 {
		return nil, false
	}

	chunks := make([]envoySlice, vectorSize)
	ret := C.envoy_dynamic_module_callback_http_get_body_chunks(
		C.uintptr_t(e.raw),
		bodyType,
		(*C.envoy_dynamic_module_type_envoy_buffer)(unsafe.Pointer(&chunks[0])),
	)
	if !ret {
		return nil, false
	}
	return &bodyReader{chunks: chunks}, true
}

func (e envoyFilter) appendBody(data []byte, bodyType C.int) bool {
	buf := C.envoy_dynamic_module_type_module_buffer{
		ptr:    C.uintptr_t(uintptr(unsafe.Pointer(unsafe.SliceData(data)))),
		length: C.size_t(len(data)),
	}
	ret := C.envoy_dynamic_module_callback_http_append_body(
		C.uintptr_t(e.raw),
		bodyType,
		buf,
	)
	runtime.KeepAlive(data)
	return bool(ret)
}

func (e envoyFilter) drainBody(n int, bodyType C.int) bool {
	ret := C.envoy_dynamic_module_callback_http_drain_body(
		C.uintptr_t(e.raw),
		bodyType,
		C.size_t(n),
	)
	return bool(ret)
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
	var resultBuffer C.envoy_dynamic_module_type_envoy_buffer

	ret := C.envoy_dynamic_module_callback_http_get_metadata_string(
		C.uintptr_t(e.raw),
		C.uint32_t(source),
		C.uintptr_t(namespacePtr),
		C.size_t(len(namespace)),
		C.uintptr_t(keyPtr),
		C.size_t(len(key)),
		&resultBuffer,
	)

	if !ret {
		return "", false
	}

	result := unsafe.Slice((*byte)(unsafe.Pointer(uintptr(resultBuffer.ptr))), resultBuffer.length)
	runtime.KeepAlive(namespace)
	runtime.KeepAlive(key)
	return string(result), true
}

// SetDynamicMetadataString implements [EnvoyHTTPFilter].
func (e envoyFilter) SetDynamicMetadataString(namespace string, key string, value string) {
	namespacePtr := uintptr(unsafe.Pointer(unsafe.StringData(namespace)))
	keyPtr := uintptr(unsafe.Pointer(unsafe.StringData(key)))
	valuePtr := uintptr(unsafe.Pointer(unsafe.StringData(value)))

	C.envoy_dynamic_module_callback_http_set_dynamic_metadata_string(
		C.uintptr_t(e.raw),
		C.envoy_dynamic_module_type_module_buffer{
			ptr:    C.uintptr_t(namespacePtr),
			length: C.size_t(len(namespace)),
		},
		C.envoy_dynamic_module_type_module_buffer{
			ptr:    C.uintptr_t(keyPtr),
			length: C.size_t(len(key)),
		},
		C.envoy_dynamic_module_type_module_buffer{
			ptr:    C.uintptr_t(valuePtr),
			length: C.size_t(len(value)),
		},
	)
	runtime.KeepAlive(namespace)
	runtime.KeepAlive(key)
	runtime.KeepAlive(value)
}

// SetDynamicMetadataNumber implements [EnvoyHTTPFilter].
func (e envoyFilter) SetDynamicMetadataNumber(namespace string, key string, value float64) {
	namespacePtr := uintptr(unsafe.Pointer(unsafe.StringData(namespace)))
	keyPtr := uintptr(unsafe.Pointer(unsafe.StringData(key)))
	C.envoy_dynamic_module_callback_http_set_dynamic_metadata_number(
		C.uintptr_t(e.raw),
		C.uintptr_t(namespacePtr),
		C.size_t(len(namespace)),
		C.uintptr_t(keyPtr),
		C.size_t(len(key)),
		C.double(value),
	)
	runtime.KeepAlive(namespace)
	runtime.KeepAlive(key)
}

// ClearRouteCache implements [EnvoyHTTPFilter].
func (e envoyFilter) ClearRouteCache() {
	C.envoy_dynamic_module_callback_http_clear_route_cache(
		C.uintptr_t(e.raw),
	)
}
