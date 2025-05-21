// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package internaltesting

import (
	"context"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
)

// NewControllerEventChanImpl is a test implementation of the controller event channels that are used in
// the cross-controller communication.
type NewControllerEventChanImpl[T client.Object] struct {
	Ch chan event.GenericEvent
}

// NewControllerEventChan creates a new SyncFnImpl.
func NewControllerEventChan[T client.Object]() *NewControllerEventChanImpl[T] {
	return &NewControllerEventChanImpl[T]{Ch: make(chan event.GenericEvent, 100)}
}

// GetItems returns a copy of the items.
func (s *NewControllerEventChanImpl[T]) GetItems(ctx context.Context, exp int) []T {
	var ret []T
	for i := 0; i < exp; i++ {
		select {
		case <-ctx.Done():
			return ret
		case item := <-s.Ch:
			ret = append(ret, item.Object.(T))
		default:
		}
	}
	return ret
}

// Reset resets the items.
func (s *NewControllerEventChanImpl[T]) Reset() {
	s.Ch = make(chan event.GenericEvent, 100)
}
