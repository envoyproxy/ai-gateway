// Copyright Envoy AI Gateway Authors
// SPDX-License-Identifier: Apache-2.0
// The full text of the Apache license is available in the LICENSE file at
// the root of the repo.

package internaltesting

import (
	"context"
	"testing"
	"time"

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

// RequireItemsEventually returns a copy of the items.
func (s *NewControllerEventChanImpl[T]) RequireItemsEventually(t *testing.T, exp int) []T {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var ret []T
	for len(ret) < exp {
		select {
		case <-ctx.Done():
			t.Fatalf("timed out waiting for %d items, got %d", exp, len(ret))
		case item := <-s.Ch:
			ret = append(ret, item.Object.(T))
		default:
		}
	}
	return ret
}
