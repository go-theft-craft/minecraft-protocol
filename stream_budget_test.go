package protocol

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestBudgetGrantsWhatFits(t *testing.T) {
	t.Parallel()

	shared := newBudget(2, 100)
	if err := shared.acquire(context.Background(), 1, 60); err != nil {
		t.Fatalf("acquire() error = %v", err)
	}
	items, bytes := shared.usage()
	if items != 1 || bytes != 60 {
		t.Fatalf("usage() = %d items, %d bytes, want 1, 60", items, bytes)
	}

	shared.release(1, 60)
	if items, bytes := shared.usage(); items != 0 || bytes != 0 {
		t.Fatalf("usage() after release = %d, %d, want 0, 0", items, bytes)
	}
}

func TestBudgetRefusesRequestsLargerThanItself(t *testing.T) {
	t.Parallel()

	shared := newBudget(2, 100)

	// A request that can never fit must fail instead of blocking forever.
	if err := shared.acquire(context.Background(), 3, 1); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("acquire(too many items) error = %v, want ErrLimitExceeded", err)
	}
	if err := shared.acquire(context.Background(), 1, 101); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("acquire(too many bytes) error = %v, want ErrLimitExceeded", err)
	}
	if err := shared.acquire(context.Background(), -1, 0); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("acquire(negative) error = %v, want ErrInvalidStream", err)
	}
}

func TestBudgetRequiresBothUnits(t *testing.T) {
	t.Parallel()

	shared := newBudget(4, 100)
	if err := shared.acquire(context.Background(), 1, 90); err != nil {
		t.Fatal(err)
	}

	// Items are available but bytes are not, so this must wait.
	waiter, err := shared.reserve(1, 20)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-waiter.done():
		t.Fatal("reserve() granted a request that does not fit the byte budget")
	default:
	}

	shared.release(1, 90)
	<-waiter.done()
	if err := waiter.failure(); err != nil {
		t.Fatalf("waiter failed after capacity was released: %v", err)
	}
}

func TestBudgetServesWaitersInOrder(t *testing.T) {
	t.Parallel()

	shared := newBudget(10, 100)
	if err := shared.acquire(context.Background(), 1, 100); err != nil {
		t.Fatal(err)
	}

	first, err := shared.reserve(1, 80)
	if err != nil {
		t.Fatal(err)
	}
	second, err := shared.reserve(1, 10)
	if err != nil {
		t.Fatal(err)
	}

	// Releasing 80 bytes satisfies the head of the queue. The smaller second
	// waiter must not barge past it.
	shared.release(1, 100)

	<-first.done()
	if err := first.failure(); err != nil {
		t.Fatalf("first waiter failed: %v", err)
	}
	select {
	case <-second.done():
		if second.failure() != nil {
			t.Fatal("second waiter failed unexpectedly")
		}
	default:
		t.Fatal("second waiter did not fit in the remaining capacity")
	}

	// With the head still holding 80 of 100 bytes, a large follower waits.
	third, err := shared.reserve(1, 30)
	if err != nil {
		t.Fatal(err)
	}
	fourth, err := shared.reserve(1, 5)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-fourth.done():
		t.Fatal("a later small waiter barged past a blocked larger one")
	default:
	}

	shared.release(1, 80)
	<-third.done()
	<-fourth.done()
}

func TestBudgetCancellationLeaksNothing(t *testing.T) {
	t.Parallel()

	shared := newBudget(2, 100)
	if err := shared.acquire(context.Background(), 1, 100); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	waited := make(chan error, 1)
	go func() { waited <- shared.acquire(ctx, 1, 50) }()

	cancel()
	if err := <-waited; !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire() error = %v, want context.Canceled", err)
	}

	shared.release(1, 100)
	if items, bytes := shared.usage(); items != 0 || bytes != 0 {
		t.Fatalf("usage() = %d items, %d bytes after a cancelled wait, want 0, 0", items, bytes)
	}

	// The budget is fully available again.
	if err := shared.acquire(context.Background(), 2, 100); err != nil {
		t.Fatalf("acquire() after cancellation error = %v", err)
	}
}

func TestBudgetCancellationAfterGrantReturnsCapacity(t *testing.T) {
	t.Parallel()

	shared := newBudget(2, 100)
	waiter, err := shared.reserve(1, 40)
	if err != nil {
		t.Fatal(err)
	}
	<-waiter.done()

	shared.cancel(waiter)
	if items, bytes := shared.usage(); items != 0 || bytes != 0 {
		t.Fatalf("usage() = %d, %d after cancelling a granted waiter, want 0, 0", items, bytes)
	}

	// Cancelling twice must stay harmless.
	shared.cancel(waiter)
	if items, bytes := shared.usage(); items != 0 || bytes != 0 {
		t.Fatalf("usage() = %d, %d after a repeated cancel, want 0, 0", items, bytes)
	}
}

func TestBudgetCloseFailsWaiters(t *testing.T) {
	t.Parallel()

	shared := newBudget(1, 10)
	if err := shared.acquire(context.Background(), 1, 10); err != nil {
		t.Fatal(err)
	}

	waited := make(chan error, 1)
	go func() { waited <- shared.acquire(context.Background(), 1, 1) }()

	sentinel := errors.New("stream failed")
	shared.close(sentinel)

	if err := <-waited; !errors.Is(err, sentinel) {
		t.Fatalf("acquire() error = %v, want the close cause", err)
	}
	if err := shared.acquire(context.Background(), 1, 1); !errors.Is(err, sentinel) {
		t.Fatalf("acquire() after close error = %v, want the close cause", err)
	}

	// Closing again keeps the first cause.
	shared.close(errors.New("later"))
	if err := shared.acquire(context.Background(), 1, 1); !errors.Is(err, sentinel) {
		t.Fatalf("acquire() error = %v, want the first close cause", err)
	}
}

func TestBudgetCloseWithoutCauseReportsClosed(t *testing.T) {
	t.Parallel()

	shared := newBudget(1, 10)
	shared.close(nil)

	if err := shared.acquire(context.Background(), 1, 1); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("acquire() error = %v, want ErrStreamClosed", err)
	}
}

func TestBudgetConcurrentProducers(t *testing.T) {
	t.Parallel()

	const (
		producers = 16
		rounds    = 50
	)

	shared := newBudget(4, 64)

	var group sync.WaitGroup
	group.Add(producers)
	for producer := range producers {
		go func() {
			defer group.Done()

			bytes := 1 + producer%16
			for range rounds {
				if err := shared.acquire(context.Background(), 1, bytes); err != nil {
					t.Errorf("acquire() error = %v", err)
					return
				}
				shared.release(1, bytes)
			}
		}()
	}
	group.Wait()

	if items, bytes := shared.usage(); items != 0 || bytes != 0 {
		t.Fatalf("usage() = %d items, %d bytes after all producers finished, want 0, 0", items, bytes)
	}
}

func TestBudgetZeroByteCapacityStillRefusesQuickly(t *testing.T) {
	t.Parallel()

	// A configuration whose buffered bytes exactly equal the reserved
	// processing headroom leaves nothing for queues. Requests must fail rather
	// than hang.
	shared := newBudget(4, 0)
	if err := shared.acquire(context.Background(), 1, 1); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("acquire() error = %v, want ErrLimitExceeded", err)
	}
}
