// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package sdk

import (
	"sync"
	"unsafe"
)

var memManager = memoryManager{
	httpFilterLists:      make([]*pinedHTTPFilter, shardingSize),
	httpFilterListsMuxes: make([]sync.Mutex, shardingSize),
}

const (
	shardingSize = 1 << 8
	shardingMask = shardingSize - 1
)

type (
	// memoryManager manages the heap allocated objects.
	// It is used to pin the objects to the heap to avoid them being garbage collected by the Go runtime.
	//
	// This will be unnecessary from v1.38 by leveraging https://github.com/envoyproxy/envoy/pull/42818
	memoryManager struct {
		// httpFilterConfigs holds a linked lists of HTTPFilter.
		httpFilterConfigs    *pinedHTTPFilterConfig
		httpFilterLists      []*pinedHTTPFilter
		httpFilterListsMuxes []sync.Mutex
	}

	// pinedHTTPFilterConfig holds a pinned HTTPFilter managed by the memory manager.
	pinedHTTPFilterConfig = linkedList[HTTPFilterConfig]

	// pinedHTTPFilter holds a pinned HTTPFilter managed by the memory manager.
	pinedHTTPFilter = linkedList[*pinedHTTPFilterItem]

	linkedList[T any] struct {
		obj        T
		next, prev *linkedList[T]
	}

	pinedHTTPFilterItem struct {
		filter HTTPFilter
		config HTTPFilterConfig
	}
)

// pinHTTPFilterConfig pins the HTTPFilterConfig to the memory manager.
func (m *memoryManager) pinHTTPFilterConfig(filterConfig HTTPFilterConfig) *pinedHTTPFilterConfig {
	item := &pinedHTTPFilterConfig{obj: filterConfig, next: m.httpFilterConfigs, prev: nil}
	if m.httpFilterConfigs != nil {
		m.httpFilterConfigs.prev = item
	}
	m.httpFilterConfigs = item
	return item
}

// unpinHTTPFilterConfig unpins the HTTPFilterConfig from the memory manager.
func (m *memoryManager) unpinHTTPFilterConfig(filterConfig *pinedHTTPFilterConfig) {
	if filterConfig.prev != nil {
		filterConfig.prev.next = filterConfig.next
	} else {
		m.httpFilterConfigs = filterConfig.next
	}
	if filterConfig.next != nil {
		filterConfig.next.prev = filterConfig.prev
	}
}

// unwrapPinnedHTTPFilterConfig unwraps the pinned http filter config.
func unwrapPinnedHTTPFilterConfig(raw uintptr) *pinedHTTPFilterConfig {
	return (*pinedHTTPFilterConfig)(unsafe.Pointer(raw))
}

// pinHTTPFilter pins the http filter to the memory manager.
func (m *memoryManager) pinHTTPFilter(filter *pinedHTTPFilterItem) *pinedHTTPFilter {
	item := &pinedHTTPFilter{obj: filter, next: nil, prev: nil}
	index := m.shardingKey(uintptr(unsafe.Pointer(item)))
	mux := &m.httpFilterListsMuxes[index]
	mux.Lock()
	defer mux.Unlock()
	item.next = m.httpFilterLists[index]
	if m.httpFilterLists[index] != nil {
		m.httpFilterLists[index].prev = item
	}
	m.httpFilterLists[index] = item
	return item
}

// unpinHTTPFilter unpins the http filter from the memory manager.
func (m *memoryManager) unpinHTTPFilter(filter *pinedHTTPFilter) {
	index := m.shardingKey(uintptr(unsafe.Pointer(filter)))
	mux := &m.httpFilterListsMuxes[index]
	mux.Lock()
	defer mux.Unlock()

	if filter.prev != nil {
		filter.prev.next = filter.next
	} else {
		m.httpFilterLists[index] = filter.next
	}
	if filter.next != nil {
		filter.next.prev = filter.prev
	}
}

// unwrapPinnedHTTPFilter unwraps the raw pointer to the pinned http filter.
func unwrapPinnedHTTPFilter(raw uintptr) *pinedHTTPFilter {
	return (*pinedHTTPFilter)(unsafe.Pointer(raw))
}

func (m *memoryManager) shardingKey(key uintptr) uintptr {
	return splitmix64(key) & uintptr(len(m.httpFilterListsMuxes)-1)
}

func splitmix64(x uintptr) uintptr {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	return x
}
