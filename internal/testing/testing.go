// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package internaltesting

import (
	"context"
	"sync"
)

// SyncFnImpl is a test implementation of the sync* functions in the controller package.
type SyncFnImpl[T any] struct {
	items []*T
	mux   sync.Mutex
}

// NewSyncFnImpl creates a new SyncFnImpl.
func NewSyncFnImpl[T any]() *SyncFnImpl[T] {
	return &SyncFnImpl[T]{}
}

// Sync is the actual implementation of the sync function.
func (s *SyncFnImpl[T]) Sync(_ context.Context, item *T) error {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.items = append(s.items, item)
	return nil
}

// GetItems returns a copy of the items.
func (s *SyncFnImpl[T]) GetItems() []*T {
	s.mux.Lock()
	defer s.mux.Unlock()
	ret := make([]*T, len(s.items))
	copy(ret, s.items)
	return ret
}

// Reset resets the items.
func (s *SyncFnImpl[T]) Reset() {
	s.mux.Lock()
	defer s.mux.Unlock()
	s.items = s.items[:0]
}
