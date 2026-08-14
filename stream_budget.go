package protocol

import (
	"context"
	"fmt"
	"sync"
)

// budget is the shared item and byte allowance for everything a stream keeps
// in memory: queued inbound packets, queued outbound packets, and pending
// observations. One budget covers every queue, so the configured ceiling is
// the whole stream's ceiling rather than a per-queue one.
//
// Waiters are served strictly in arrival order. A waiter at the head that does
// not fit blocks the ones behind it rather than letting later, smaller
// requests barge past, which is what keeps a large packet from starving.
type budget struct {
	mu       sync.Mutex
	maxItems int
	maxBytes int
	items    int
	bytes    int
	waiters  []*budgetWaiter
	closed   error
}

// budgetWaiter is one pending or granted reservation.
type budgetWaiter struct {
	items int
	bytes int
	// ready fires exactly once, when the reservation is granted or refused.
	ready chan struct{}
	// granted and err are written under the owning budget's lock before
	// ready fires, so a reader that has seen ready may read them freely.
	granted bool
	err     error
}

func newBudget(maxItems, maxBytes int) *budget {
	return &budget{maxItems: maxItems, maxBytes: max(maxBytes, 0)}
}

// reserve queues a reservation. The returned waiter fires when the budget
// grants or refuses it. A request larger than the whole budget is refused
// immediately, because waiting for it could never succeed.
func (b *budget) reserve(items, bytes int) (*budgetWaiter, error) {
	if items < 0 || bytes < 0 {
		return nil, fmt.Errorf("%w: negative budget request", ErrInvalidStream)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed != nil {
		return nil, b.closed
	}
	if items > b.maxItems || bytes > b.maxBytes {
		return nil, fmt.Errorf(
			"%w: a request of %d items and %d bytes never fits a budget of %d items and %d bytes",
			ErrLimitExceeded,
			items,
			bytes,
			b.maxItems,
			b.maxBytes,
		)
	}

	waiter := &budgetWaiter{items: items, bytes: bytes, ready: make(chan struct{})}
	b.waiters = append(b.waiters, waiter)
	b.grantLocked()

	return waiter, nil
}

// ready exposes the completion channel for use in a select.
func (w *budgetWaiter) done() <-chan struct{} { return w.ready }

// grantLocked serves waiters from the head while they fit.
func (b *budget) grantLocked() {
	for len(b.waiters) > 0 {
		waiter := b.waiters[0]
		if b.items+waiter.items > b.maxItems || b.bytes+waiter.bytes > b.maxBytes {
			return
		}

		b.items += waiter.items
		b.bytes += waiter.bytes
		waiter.granted = true
		b.waiters = b.waiters[1:]
		close(waiter.ready)
	}
}

// cancel abandons a reservation. Capacity that was already granted is returned
// to the budget, so a lost race between cancellation and a grant cannot leak.
func (b *budget) cancel(waiter *budgetWaiter) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if waiter.granted {
		b.items -= waiter.items
		b.bytes -= waiter.bytes
		waiter.granted = false
		b.grantLocked()

		return
	}

	for index, pending := range b.waiters {
		if pending == waiter {
			b.waiters = append(b.waiters[:index], b.waiters[index+1:]...)
			close(waiter.ready)

			return
		}
	}
}

// err reports why a reservation finished without capacity.
func (w *budgetWaiter) failure() error {
	if w.granted {
		return nil
	}
	if w.err != nil {
		return w.err
	}

	return ErrStreamClosed
}

// acquire blocks until the budget grants the request, the context ends, or the
// budget closes.
func (b *budget) acquire(ctx context.Context, items, bytes int) error {
	waiter, err := b.reserve(items, bytes)
	if err != nil {
		return err
	}

	select {
	case <-waiter.done():
		return waiter.failure()
	case <-ctx.Done():
		b.cancel(waiter)
		return ctx.Err()
	}
}

// acquireUntil blocks until the budget grants the request or stop fires. It
// exists for the coordinator, which has no per-call context but must never
// outlive the stream.
func (b *budget) acquireUntil(stop <-chan struct{}, items, bytes int) error {
	waiter, err := b.reserve(items, bytes)
	if err != nil {
		return err
	}

	select {
	case <-waiter.done():
		return waiter.failure()
	case <-stop:
		b.cancel(waiter)
		return ErrStreamClosed
	}
}

// release returns capacity and wakes whatever now fits.
func (b *budget) release(items, bytes int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.items -= items
	b.bytes -= bytes
	if b.items < 0 || b.bytes < 0 {
		panic("protocol: released more budget than was acquired")
	}

	b.grantLocked()
}

// close refuses every pending and future reservation with cause.
func (b *budget) close(cause error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.closed != nil {
		return
	}
	if cause == nil {
		cause = ErrStreamClosed
	}
	b.closed = cause

	for _, waiter := range b.waiters {
		waiter.err = cause
		close(waiter.ready)
	}
	b.waiters = nil
}

// usage reports the current charge, for tests and diagnostics.
func (b *budget) usage() (int, int) {
	b.mu.Lock()
	defer b.mu.Unlock()

	return b.items, b.bytes
}
