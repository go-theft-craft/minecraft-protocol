package protocol

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// Stream is an asynchronous managed connection. It owns one transport and one
// session, runs a read pump and a write pump, and orders every state change,
// control, observation, and shutdown step through a single coordinator.
//
// Construction performs no I/O and starts no goroutine. Start does both.
type Stream struct {
	session   Session
	transport Transport

	// conduit is the byte-level stage both pumps use. It owns the read
	// buffer and the ciphers, so encryption is invisible to framing.
	conduit *Conduit

	limits   Limits
	framer   Framer
	policy   TransitionPolicy
	preFrame PreFrameHook
	sink     ObservationSink

	// inboundFrames hands one complete frame from the read pump to the
	// coordinator. Receiving from it also transfers the processing slot.
	inboundFrames chan Frame
	// inboundPackets is the Read queue. Its capacity matches the item budget,
	// so a granted budget always has somewhere to put the packet.
	inboundPackets chan inboundItem
	// writeRequests carries accepted Write calls to the coordinator in
	// acceptance order, which is the order they reach the wire.
	writeRequests chan *writeRequest
	// writeJobs carries built frames to the write pump.
	writeJobs chan writeJob
	// controlRequests carries runtime state and pipeline changes to the
	// coordinator, in the same order as writes.
	controlRequests chan *controlRequest
	// shutdownRequests carries one graceful shutdown to the coordinator.
	shutdownRequests chan *shutdownRequest
	// snapshotRequests asks the coordinator for the session configuration.
	snapshotRequests chan chan Snapshot
	// observations carries lossless records to the dispatcher goroutine.
	observations chan observationRecord
	// observationsDone tells the dispatcher to drain and finish.
	observationsDone chan struct{}

	// sequence and frameCounter are touched only by the coordinator.
	sequence     uint64
	frameCounter uint64

	closing        atomic.Bool
	shutdownOnce   sync.Once
	shutdownResult error

	// queued covers everything the stream retains: queued packets in both
	// directions and pending observations. Frame and decompression working
	// buffers are excluded and covered by the reserved processing headroom.
	queued *budget
	// processing bounds the read pump to one frame in flight. The read pump
	// takes it before framing and the coordinator returns it after decoding,
	// so ownership follows the frame rather than the goroutine.
	processing chan struct{}

	started atomic.Bool

	stopOnce sync.Once
	stopping chan struct{}

	interruptOnce sync.Once
	interruptErr  error

	finishOnce sync.Once
	done       chan struct{}

	causeMu  sync.Mutex
	cause    error
	causeSet bool
}

// StreamOption configures a stream before it starts.
type StreamOption func(*Stream) error

// WithTransitionPolicy installs the policy that decides what happens to each
// proposed transition. The default accepts every proposal.
func WithTransitionPolicy(policy TransitionPolicy) StreamOption {
	return func(stream *Stream) error {
		if policy == nil {
			return fmt.Errorf("%w: nil transition policy", ErrInvalidStream)
		}
		stream.policy = policy

		return nil
	}
}

// WithPreFrameHook installs a hook that inspects the connection once, before
// normal framing begins.
func WithPreFrameHook(hook PreFrameHook) StreamOption {
	return func(stream *Stream) error {
		if hook == nil {
			return fmt.Errorf("%w: nil pre-frame hook", ErrInvalidStream)
		}
		stream.preFrame = hook

		return nil
	}
}

// NewStream builds a stream over session and transport. It touches neither the
// transport nor the session beyond reading immutable configuration, and it
// starts no goroutine.
func NewStream(session Session, transport Transport, options ...StreamOption) (*Stream, error) {
	if session == nil {
		return nil, fmt.Errorf("%w: nil session", ErrInvalidStream)
	}
	if err := transport.validate(); err != nil {
		return nil, err
	}

	limits := session.Limits()
	if !limits.Valid() {
		return nil, fmt.Errorf("%w: session has unvalidated limits", ErrInvalidStream)
	}
	if session.Framer() == nil {
		return nil, fmt.Errorf("%w: session has no framer", ErrInvalidStream)
	}

	stream := &Stream{
		session:    session,
		transport:  transport,
		limits:     limits,
		framer:     session.Framer(),
		policy:     AcceptTransitions(),
		queued:     newBudget(limits.QueueItems(), queuedByteCapacity(limits)),
		processing: make(chan struct{}, 1),
		conduit:    newConduit(transport),
		stopping:   make(chan struct{}),
		done:       make(chan struct{}),

		inboundFrames:  make(chan Frame),
		inboundPackets: make(chan inboundItem, limits.QueueItems()),
		writeRequests:  make(chan *writeRequest),
		writeJobs:      make(chan writeJob),

		controlRequests:  make(chan *controlRequest),
		shutdownRequests: make(chan *shutdownRequest),
		snapshotRequests: make(chan chan Snapshot),
		observations:     make(chan observationRecord, limits.QueueItems()),
		observationsDone: make(chan struct{}),
	}
	stream.processing <- struct{}{}

	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidStream)
		}
		if err := option(stream); err != nil {
			return nil, err
		}
	}

	return stream, nil
}

// queuedByteCapacity is the part of the buffered-byte limit that queues may
// use. The rest is processing headroom.
//
// The headroom covers two frames in flight, not one: an inbound frame being
// framed and decompressed, and an outbound packet being encoded and framed.
// Reserving only one would mean either sharing a single slot between the read
// pump and the coordinator, which deadlocks when the read pump holds the slot
// while handing its frame over, or letting the outbound path allocate outside
// the ceiling. Reserving both keeps the ceiling honest.
func queuedByteCapacity(limits Limits) int {
	return limits.BufferedBytes() - 2*(limits.FrameBytes()+limits.DecompressedBytes())
}

// acquireProcessing takes the reserved headroom slot. It reports false when
// the stream is stopping.
func (s *Stream) acquireProcessing() bool {
	select {
	case <-s.processing:
		return true
	case <-s.stopping:
		return false
	}
}

// releaseProcessing returns the headroom slot.
func (s *Stream) releaseProcessing() {
	select {
	case s.processing <- struct{}{}:
	default:
		panic("protocol: released processing headroom that was not held")
	}
}

// Start runs the stream until it terminates. It returns an error only when the
// stream cannot start; every later failure is reported by Wait.
//
// Cancelling ctx aborts the stream and interrupts blocked transport calls.
func (s *Stream) Start(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidStream)
	}
	if !s.started.CompareAndSwap(false, true) {
		return ErrStreamStarted
	}
	if s.terminated() {
		return ErrStreamClosed
	}

	go s.supervise(ctx)

	return nil
}

// supervise runs the pumps and terminates the stream once they finish.
func (s *Stream) supervise(ctx context.Context) {
	defer s.finish()

	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			s.fail(ctx.Err())
			s.stop()
		case <-s.stopping:
		}
	}()

	s.run(ctx)

	// The watcher above races with a run loop that noticed the cancellation
	// itself, so record the cause here too. Whichever gets there first wins,
	// and both happen before finish unblocks Wait.
	s.fail(ctx.Err())
	s.stop()
	<-watchDone
}

// Close ends the stream immediately. It interrupts the transport, fails queued
// work, and is safe to call more than once. Wait reports ErrStreamClosed
// unless an earlier cause was already recorded.
func (s *Stream) Close() error {
	s.fail(ErrStreamClosed)
	s.stop()

	if !s.started.Load() {
		// Nothing will ever run, so nothing else can terminate the stream.
		s.finish()
	}

	<-s.done

	return s.interruptErr
}

// Wait blocks until the stream terminates and returns its first fatal cause. A
// graceful shutdown reports nil.
func (s *Stream) Wait() error {
	if !s.started.Load() && !s.terminated() {
		return ErrStreamNotStarted
	}

	<-s.done

	return s.firstCause()
}

// fail records the first fatal cause. Later causes are consequences of it and
// would only obscure the original failure.
func (s *Stream) fail(cause error) {
	if cause == nil {
		return
	}

	s.causeMu.Lock()
	defer s.causeMu.Unlock()

	if s.causeSet {
		return
	}
	s.cause = cause
	s.causeSet = true
}

// succeed records a clean termination, so a later abortive Close cannot turn a
// completed graceful shutdown into a failure.
func (s *Stream) succeed() {
	s.causeMu.Lock()
	defer s.causeMu.Unlock()

	if s.causeSet {
		return
	}
	s.causeSet = true
}

func (s *Stream) firstCause() error {
	s.causeMu.Lock()
	defer s.causeMu.Unlock()

	return s.cause
}

// stop asks the pumps to finish and interrupts the transport exactly once.
func (s *Stream) stop() {
	s.stopOnce.Do(func() {
		close(s.stopping)
		s.queued.close(s.firstCause())
		s.interruptOnce.Do(func() {
			s.interruptErr = s.transport.Interrupt()
		})
	})
}

// finish marks the stream terminated and unblocks Wait.
func (s *Stream) finish() {
	s.finishOnce.Do(func() { close(s.done) })
}

func (s *Stream) terminated() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}
