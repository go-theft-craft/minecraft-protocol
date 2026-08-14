package protocol

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
)

// countingTransport records whether the stream touched it during construction.
// Its reads block until the stream interrupts it, the way a live connection
// with a quiet peer behaves, so lifecycle tests are not racing a instant EOF.
type countingTransport struct {
	reads       atomic.Int64
	writes      atomic.Int64
	interrupts  atomic.Int64
	interrupt   error
	closed      chan struct{}
	closeOnce   sync.Once
	initialized sync.Once
}

func (c *countingTransport) closedChannel() chan struct{} {
	c.initialized.Do(func() { c.closed = make(chan struct{}) })
	return c.closed
}

func (c *countingTransport) Read([]byte) (int, error) {
	c.reads.Add(1)
	<-c.closedChannel()
	return 0, io.EOF
}

func (c *countingTransport) Write(data []byte) (int, error) {
	c.writes.Add(1)
	return len(data), nil
}

func (c *countingTransport) Interrupt() error {
	c.interrupts.Add(1)
	closed := c.closedChannel()
	c.closeOnce.Do(func() { close(closed) })
	return c.interrupt
}

func (c *countingTransport) transport() Transport {
	c.closedChannel()
	return Transport{Reader: c, Writer: c, Interrupt: c.Interrupt}
}

// recordingSession counts session calls so construction can be proven inert.
type recordingSession struct {
	stubSession
	calls atomic.Int64
}

func (s *recordingSession) DecodeFrame(payload []byte) (Packet, error) {
	s.calls.Add(1)
	return s.stubSession.DecodeFrame(payload)
}

func (s *recordingSession) EncodeFrame(packet Packet) ([]byte, error) {
	s.calls.Add(1)
	return s.stubSession.EncodeFrame(packet)
}

func testStreamLimits(t *testing.T) Limits {
	t.Helper()

	limits, err := NewLimits(
		MaxFrameBytes(4096),
		MaxDecompressedBytes(8192),
		MaxBufferedBytes(1<<20),
		MaxQueueItems(8),
	)
	if err != nil {
		t.Fatal(err)
	}
	return limits
}

func newTestStream(t *testing.T, options ...StreamOption) (*Stream, *countingTransport) {
	t.Helper()

	transport := &countingTransport{}
	stream, err := NewStream(
		newTestSession(t, testStreamLimits(t)),
		transport.transport(),
		options...,
	)
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	return stream, transport
}

func TestNewStreamRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	limits := testStreamLimits(t)
	session := &stubSession{limits: limits}
	complete := Transport{
		Reader:    bytes.NewReader(nil),
		Writer:    io.Discard,
		Interrupt: func() error { return nil },
	}

	t.Run("nil session", func(t *testing.T) {
		t.Parallel()

		if _, err := NewStream(nil, complete); !errors.Is(err, ErrInvalidStream) {
			t.Fatalf("NewStream(nil session) error = %v, want ErrInvalidStream", err)
		}
	})

	t.Run("incomplete transport", func(t *testing.T) {
		t.Parallel()

		for name, transport := range map[string]Transport{
			"nil reader":    {Writer: complete.Writer, Interrupt: complete.Interrupt},
			"nil writer":    {Reader: complete.Reader, Interrupt: complete.Interrupt},
			"nil interrupt": {Reader: complete.Reader, Writer: complete.Writer},
		} {
			if _, err := NewStream(session, transport); !errors.Is(err, ErrInvalidTransport) {
				t.Errorf("NewStream(%s) error = %v, want ErrInvalidTransport", name, err)
			}
		}
	})

	t.Run("unvalidated session limits", func(t *testing.T) {
		t.Parallel()

		if _, err := NewStream(&stubSession{}, complete); !errors.Is(err, ErrInvalidStream) {
			t.Fatalf("NewStream(invalid limits) error = %v, want ErrInvalidStream", err)
		}
	})

	t.Run("nil option", func(t *testing.T) {
		t.Parallel()

		if _, err := NewStream(session, complete, nil); !errors.Is(err, ErrInvalidStream) {
			t.Fatalf("NewStream(nil option) error = %v, want ErrInvalidStream", err)
		}
	})

	t.Run("nil configured policy", func(t *testing.T) {
		t.Parallel()

		if _, err := NewStream(session, complete, WithTransitionPolicy(nil)); !errors.Is(err, ErrInvalidStream) {
			t.Fatalf("WithTransitionPolicy(nil) error = %v, want ErrInvalidStream", err)
		}
		if _, err := NewStream(session, complete, WithPreFrameHook(nil)); !errors.Is(err, ErrInvalidStream) {
			t.Fatalf("WithPreFrameHook(nil) error = %v, want ErrInvalidStream", err)
		}
	})
}

func TestNewStreamPerformsNoIO(t *testing.T) {
	t.Parallel()

	transport := &countingTransport{}
	session := &recordingSession{stubSession: stubSession{limits: testStreamLimits(t)}}

	stream, err := NewStream(session, transport.transport())
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	if stream == nil {
		t.Fatal("NewStream() returned no stream")
	}

	if transport.reads.Load() != 0 || transport.writes.Load() != 0 || transport.interrupts.Load() != 0 {
		t.Fatalf(
			"NewStream() touched the transport: %d reads, %d writes, %d interrupts",
			transport.reads.Load(), transport.writes.Load(), transport.interrupts.Load(),
		)
	}
	if session.calls.Load() != 0 {
		t.Fatalf("NewStream() made %d coding calls on the session", session.calls.Load())
	}
}

func TestStreamDefaultsToAcceptingTransitions(t *testing.T) {
	t.Parallel()

	stream, _ := newTestStream(t)
	if stream.policy == nil {
		t.Fatal("NewStream() left the transition policy unset")
	}

	state := State("status")
	resolved, accepted, err := stream.policy.Resolve(
		context.Background(),
		TransitionContext{},
		Transition{State: &state},
	)
	if err != nil || !accepted || resolved.State == nil || *resolved.State != state {
		t.Fatalf("default policy resolved = %+v, %t, %v", resolved, accepted, err)
	}
}

func TestStreamReservesProcessingHeadroom(t *testing.T) {
	t.Parallel()

	limits := testStreamLimits(t)
	stream, _ := newTestStream(t)

	// One inbound frame in flight and one outbound frame in flight.
	want := limits.BufferedBytes() - 2*(limits.FrameBytes()+limits.DecompressedBytes())
	if stream.queued.maxBytes != want {
		t.Fatalf("queue byte capacity = %d, want %d", stream.queued.maxBytes, want)
	}
	if stream.queued.maxItems != limits.QueueItems() {
		t.Fatalf("queue item capacity = %d, want %d", stream.queued.maxItems, limits.QueueItems())
	}
}

func TestStreamProcessingSlotIsExclusive(t *testing.T) {
	t.Parallel()

	stream, _ := newTestStream(t)

	if !stream.acquireProcessing() {
		t.Fatal("acquireProcessing() = false on a fresh stream")
	}

	taken := make(chan bool, 1)
	go func() { taken <- stream.acquireProcessing() }()

	select {
	case <-taken:
		t.Fatal("acquireProcessing() granted the headroom slot twice")
	default:
	}

	stream.releaseProcessing()
	if !<-taken {
		t.Fatal("acquireProcessing() = false after the slot was released")
	}
}

func TestStreamProcessingSlotReleasesOnStop(t *testing.T) {
	t.Parallel()

	stream, _ := newTestStream(t)
	if !stream.acquireProcessing() {
		t.Fatal("acquireProcessing() = false on a fresh stream")
	}

	taken := make(chan bool, 1)
	go func() { taken <- stream.acquireProcessing() }()

	stream.stop()
	if <-taken {
		t.Fatal("acquireProcessing() granted the slot while stopping")
	}
}

func TestStreamStartRejectsSecondCall(t *testing.T) {
	t.Parallel()

	stream, _ := newTestStream(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := stream.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := stream.Start(ctx); !errors.Is(err, ErrStreamStarted) {
		t.Fatalf("second Start() error = %v, want ErrStreamStarted", err)
	}

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

//nolint:staticcheck // A nil context is exactly what this test rejects.
func TestStreamStartRejectsNilContext(t *testing.T) {
	t.Parallel()

	stream, _ := newTestStream(t)
	if err := stream.Start(nil); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("Start(nil) error = %v, want ErrInvalidStream", err)
	}
}

func TestStreamWaitBeforeStart(t *testing.T) {
	t.Parallel()

	stream, _ := newTestStream(t)
	if err := stream.Wait(); !errors.Is(err, ErrStreamNotStarted) {
		t.Fatalf("Wait() error = %v, want ErrStreamNotStarted", err)
	}
}

func TestStreamCloseBeforeStartTerminates(t *testing.T) {
	t.Parallel()

	stream, transport := newTestStream(t)

	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Wait(); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("Wait() error = %v, want ErrStreamClosed", err)
	}
	if transport.interrupts.Load() != 1 {
		t.Fatalf("Close() interrupted the transport %d times, want 1", transport.interrupts.Load())
	}
	if err := stream.Start(context.Background()); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("Start() after Close error = %v, want ErrStreamClosed", err)
	}
}

func TestStreamCloseIsIdempotent(t *testing.T) {
	t.Parallel()

	stream, transport := newTestStream(t)
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	var group sync.WaitGroup
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			if err := stream.Close(); err != nil {
				t.Errorf("Close() error = %v", err)
			}
		}()
	}
	group.Wait()

	if transport.interrupts.Load() != 1 {
		t.Fatalf("Close() interrupted the transport %d times, want 1", transport.interrupts.Load())
	}
	if err := stream.Wait(); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("Wait() error = %v, want ErrStreamClosed", err)
	}
}

func TestStreamCloseReportsInterruptFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("interrupt failed")
	transport := &countingTransport{interrupt: sentinel}
	stream, err := NewStream(
		&stubSession{limits: testStreamLimits(t)},
		transport.transport(),
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := stream.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("Close() error = %v, want the interrupt error", err)
	}
	if err := stream.Close(); !errors.Is(err, sentinel) {
		t.Fatalf("repeated Close() error = %v, want the same interrupt error", err)
	}
}

func TestStreamContextCancellationIsTheFirstCause(t *testing.T) {
	t.Parallel()

	stream, transport := newTestStream(t)

	ctx, cancel := context.WithCancel(context.Background())
	if err := stream.Start(ctx); err != nil {
		t.Fatal(err)
	}

	cancel()
	if err := stream.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() error = %v, want context.Canceled", err)
	}
	if transport.interrupts.Load() != 1 {
		t.Fatalf("cancellation interrupted the transport %d times, want 1", transport.interrupts.Load())
	}

	// A later Close must not overwrite the recorded cause.
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("Wait() error after Close = %v, want the first cause", err)
	}
}

func TestStreamGracefulTerminationReportsNoCause(t *testing.T) {
	t.Parallel()

	stream, _ := newTestStream(t)
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	// A graceful stop records success before anything can fail the stream.
	stream.succeed()
	stream.stop()

	if err := stream.Wait(); err != nil {
		t.Fatalf("Wait() error = %v, want nil after a graceful stop", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Wait(); err != nil {
		t.Fatalf("Wait() error = %v after Close, want the graceful result to stand", err)
	}
}

func TestStreamCloseFailsQueuedWork(t *testing.T) {
	t.Parallel()

	stream, _ := newTestStream(t)
	if err := stream.Close(); err != nil {
		t.Fatal(err)
	}

	if err := stream.queued.acquire(context.Background(), 1, 1); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("budget acquire after Close error = %v, want ErrStreamClosed", err)
	}
}
