package protocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
)

// inboundItem is one decoded packet waiting in the Read queue, together with
// the byte charge to return to the budget when a reader takes it.
type inboundItem struct {
	packet Packet
	bytes  int
}

// writeJob is one built frame handed to the write pump.
type writeJob struct {
	frame  Frame
	result chan error
}

// writeRequest states.
const (
	writeQueued int32 = iota
	writeStarted
	writeAbandoned
)

// writeRequest is one accepted Write call travelling to the coordinator.
type writeRequest struct {
	packet Packet
	state  atomic.Int32
	// result is buffered so the coordinator never blocks reporting an
	// outcome, even to a caller that has already given up.
	result chan error
}

func newWriteRequest(packet Packet) *writeRequest {
	return &writeRequest{packet: packet, result: make(chan error, 1)}
}

// begin claims the request for the transport. It fails when the caller
// abandoned the write first.
func (r *writeRequest) begin() bool {
	return r.state.CompareAndSwap(writeQueued, writeStarted)
}

// abandon gives up on a write that has not reached the transport. It fails
// once the transport write has started, because from that moment the caller
// cannot know how much the peer received.
func (r *writeRequest) abandon() bool {
	return r.state.CompareAndSwap(writeQueued, writeAbandoned)
}

func (r *writeRequest) finish(err error) {
	select {
	case r.result <- err:
	default:
	}
}

// controlRequest is one runtime control travelling to the coordinator.
type controlRequest struct {
	control Control
	result  chan error
}

// transitionDecision is a resolved transition waiting to be committed.
type transitionDecision struct {
	transition Transition
	commit     bool
}

// shutdownRequest carries one graceful shutdown to the coordinator.
type shutdownRequest struct {
	reason string
	result chan error
}

// pendingInbound is a decoded packet waiting for queue capacity.
type pendingInbound struct {
	item   inboundItem
	waiter *budgetWaiter
}

// run starts the pumps, the observation dispatcher, and the coordinator, and
// returns once they all finish.
func (s *Stream) run(ctx context.Context) {
	var pumps, dispatcher sync.WaitGroup

	pumps.Add(2)
	go func() {
		defer pumps.Done()
		s.readPump(ctx)
	}()
	go func() {
		defer pumps.Done()
		s.writePump()
	}()

	dispatcher.Add(1)
	go func() {
		defer dispatcher.Done()
		s.dispatchObservations(ctx)
	}()

	s.coordinate(ctx)

	// The dispatcher drains before the stream stops, so a graceful shutdown
	// does not truncate the capture. Stopping first would race it.
	close(s.observationsDone)
	dispatcher.Wait()

	// Stopping unblocks the pumps. Their transport calls are unblocked by the
	// interrupt function that stop invokes.
	s.stop()
	pumps.Wait()
}

// readPump turns transport bytes into complete frames. It owns the processing
// slot from before framing until the coordinator takes the frame.
func (s *Stream) readPump(ctx context.Context) {
	reader := s.conduit

	claimed, err := runPreFrame(ctx, s.preFrame, s.conduit.PreFrameReader(), s.conduit)
	if err != nil {
		s.fail(fmt.Errorf("pre-frame hook: %w", err))
		s.stop()

		return
	}
	if claimed {
		// The hook owns the raw exchange and the connection is finished.
		s.succeed()
		s.stop()

		return
	}

	for {
		if !s.acquireProcessing() {
			return
		}

		frame, err := s.framer.ReadFrame(reader)
		if err != nil {
			s.releaseProcessing()
			s.reportReadFailure(err)

			return
		}

		select {
		case s.inboundFrames <- frame:
			// The coordinator now owns both the frame and the slot.
		case <-s.stopping:
			s.releaseProcessing()

			return
		}
	}
}

// reportReadFailure records why the read pump stopped. A peer that closes
// while the stream is already stopping is expected, not a failure.
func (s *Stream) reportReadFailure(err error) {
	select {
	case <-s.stopping:
		return
	default:
	}

	if errors.Is(err, io.EOF) {
		s.fail(fmt.Errorf("peer closed the connection: %w", err))
	} else {
		s.fail(fmt.Errorf("read inbound frame: %w", err))
	}
	s.stop()
}

// writePump performs the transport writes. It exists so a transport that
// blocks forever cannot stop the coordinator from reacting to shutdown.
func (s *Stream) writePump() {
	for {
		select {
		case job := <-s.writeJobs:
			err := s.framer.WriteFrame(s.conduit, job.frame)
			select {
			case job.result <- err:
			case <-s.stopping:
				return
			}
		case <-s.stopping:
			return
		}
	}
}

// coordinate is the single goroutine that touches the session. Every decode,
// encode, transition, and control happens here, in one order.
func (s *Stream) coordinate(ctx context.Context) {
	var pending *pendingInbound

	for {
		// A frame is accepted only when the previous packet has left the
		// coordinator, which is what bounds decoded packets to one in flight.
		var frames <-chan Frame
		if pending == nil {
			frames = s.inboundFrames
		}

		var granted <-chan struct{}
		if pending != nil {
			granted = pending.waiter.done()
		}

		select {
		case <-s.stopping:
			return

		case frame := <-frames:
			next, err := s.decodeInbound(ctx, frame)
			if err != nil {
				s.fail(err)
				s.stop()

				return
			}
			pending = next

		case <-granted:
			if err := pending.waiter.failure(); err != nil {
				s.fail(err)
				s.stop()

				return
			}
			select {
			case s.inboundPackets <- pending.item:
				pending = nil
			case <-s.stopping:
				return
			}

		case request := <-s.writeRequests:
			if !s.processWrite(ctx, request) {
				return
			}

		case request := <-s.controlRequests:
			s.processControl(request)

		case result := <-s.snapshotRequests:
			select {
			case result <- s.snapshot():
			case <-s.stopping:
				return
			}

		case request := <-s.shutdownRequests:
			s.finishShutdown(ctx, request)

			return
		}
	}
}

// decodeInbound decodes one frame, commits any transition it implies, and
// reserves queue capacity for the result. It owns the processing slot on entry
// and returns it before reserving, so a full queue never holds the read pump's
// headroom.
//
// The packet is decoded under the snapshot that was current before it arrived,
// and its transition is committed before the packet reaches Read. A reader
// therefore never sees a packet whose own transition has not taken effect.
func (s *Stream) decodeInbound(ctx context.Context, frame Frame) (*pendingInbound, error) {
	before := s.session.Snapshot()
	s.frameCounter++
	frameID := s.frameCounter

	// The raw record is emitted before decoding, so a capture keeps the exact
	// bytes even when the frame turns out to be undecodable. Redaction is
	// therefore decided from the frame rather than from a packet that does
	// not exist yet.
	if err := s.observe(observationInput{
		direction: s.session.Inbound(),
		stage:     ObservationRawFrame,
		frame:     frameID,
		before:    before,
		after:     before,
		payload:   frame.WireBytes(),
		redacted:  s.sensitiveFrame(s.session.Inbound(), frame.Payload()),
	}); err != nil {
		return nil, err
	}

	packet, err := s.session.DecodeFrame(frame.Payload())
	s.releaseProcessing()
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedInbound, err)
	}

	decision, err := s.resolveTransition(ctx, packet, true, before)
	if err != nil {
		return nil, err
	}
	if decision.commit {
		s.session.ApplyTransition(decision.transition)
	}

	if err := s.observe(observationInput{
		direction: s.session.Inbound(),
		stage:     ObservationPacket,
		frame:     frameID,
		before:    before,
		after:     s.session.Snapshot(),
		packet:    packetMetadata(packet),
		payload:   packet.Payload,
		redacted:  s.sensitive(packet),
	}); err != nil {
		return nil, err
	}

	charge := len(packet.Payload)
	waiter, err := s.queued.reserve(1, charge)
	if err != nil {
		return nil, fmt.Errorf("queue inbound packet: %w", err)
	}

	return &pendingInbound{
		item:   inboundItem{packet: packet, bytes: charge},
		waiter: waiter,
	}, nil
}

// processWrite encodes, frames, and writes one accepted packet. It reports
// whether the coordinator should keep running.
//
// The packet is encoded under the snapshot that is current before it is sent,
// and any transition it implies is resolved before the first byte leaves the
// process and committed only after the whole frame is written.
func (s *Stream) processWrite(ctx context.Context, request *writeRequest) bool {
	defer s.queued.release(1, 0)

	before := s.session.Snapshot()
	s.frameCounter++
	frameID := s.frameCounter

	payload, err := s.session.EncodeFrame(request.packet)
	if err != nil {
		// Nothing reached the transport, so the stream keeps running.
		s.observeRejected(before, request.packet, err)
		request.finish(err)

		return true
	}

	// Resolving before the write means a rejected transition fails the Write
	// call instead of leaving the connection in a state the peer disagrees
	// with after the bytes are already gone.
	decision, err := s.resolveTransition(ctx, request.packet, false, before)
	if err != nil {
		s.observeRejected(before, request.packet, err)
		request.finish(err)

		return true
	}

	frame, err := s.framer.BuildFrame(payload)
	if err != nil {
		s.observeRejected(before, request.packet, err)
		request.finish(err)

		return true
	}

	if !request.begin() {
		// The caller cancelled before any byte left the process.
		s.observeRejected(before, request.packet, context.Canceled)

		return true
	}

	result := make(chan error, 1)
	select {
	case s.writeJobs <- writeJob{frame: frame, result: result}:
	case <-s.stopping:
		request.finish(ErrStreamClosed)

		return false
	}

	select {
	case err := <-result:
		if err != nil {
			request.finish(err)
			s.fail(fmt.Errorf("write outbound frame: %w", err))
			s.stop()

			return false
		}
		if decision.commit {
			s.session.ApplyTransition(decision.transition)
		}

		if err := s.observeOutbound(frameID, before, frame, request.packet, payload); err != nil {
			request.finish(err)
			s.fail(err)
			s.stop()

			return false
		}
		request.finish(nil)

		return true

	case <-s.stopping:
		request.finish(ErrStreamClosed)

		return false
	}
}

// observeRejected records a write the stream accepted and then refused.
//
// Without it a rejected write leaves no trace at all: observeOutbound runs
// only after the transport has taken the frame, so a consumer looking for a
// packet that never appeared would find a capture that simply does not
// mention it.
//
// A failure to record is not reported. The write already failed, the caller
// already has that error, and replacing it with a bookkeeping error would hide
// the reason the packet did not go out.
func (s *Stream) observeRejected(before Snapshot, packet Packet, reason error) {
	_ = s.observe(observationInput{
		direction: s.session.Outbound(),
		stage:     ObservationRejected,
		frame:     s.frameCounter,
		before:    before,
		after:     before,
		packet:    packetMetadata(packet),
		rejected:  &RejectionMetadata{Reason: reason.Error()},
	})
}

// observeOutbound records one written frame and its packet.
func (s *Stream) observeOutbound(
	frameID uint64,
	before Snapshot,
	frame Frame,
	packet Packet,
	payload []byte,
) error {
	// Outbound has the packet in hand, so the raw record asks the same
	// question the packet record does rather than re-reading the frame.
	if err := s.observe(observationInput{
		direction: s.session.Outbound(),
		stage:     ObservationRawFrame,
		frame:     frameID,
		before:    before,
		after:     before,
		payload:   frame.WireBytes(),
		redacted:  s.sensitive(packet),
	}); err != nil {
		return err
	}

	return s.observe(observationInput{
		direction: s.session.Outbound(),
		stage:     ObservationPacket,
		frame:     frameID,
		before:    before,
		after:     s.session.Snapshot(),
		packet:    packetMetadata(packet),
		payload:   payload,
		redacted:  s.sensitive(packet),
	})
}

// finishShutdown runs the graceful shutdown sequence on the coordinator, which
// is the only goroutine allowed to touch the session.
func (s *Stream) finishShutdown(ctx context.Context, request *shutdownRequest) {
	err := s.sendDisconnect(ctx, request.reason)
	if err != nil {
		s.fail(err)
	} else {
		s.succeed()
	}

	request.result <- err
}

// sendDisconnect writes the state-appropriate disconnect packet as the final
// frame. It bypasses the shared queue budget deliberately: a full inbound
// queue must not stop a connection from closing politely.
func (s *Stream) sendDisconnect(ctx context.Context, reason string) error {
	packet, supported, err := s.session.Disconnect(reason)
	if err != nil {
		return fmt.Errorf("build disconnect packet: %w", err)
	}
	if !supported {
		// A client role, or a state with no disconnect packet, closes without
		// sending anything. That is a clean shutdown, not a failure.
		return nil
	}

	before := s.session.Snapshot()
	s.frameCounter++
	frameID := s.frameCounter

	payload, err := s.session.EncodeFrame(packet)
	if err != nil {
		return fmt.Errorf("encode disconnect packet: %w", err)
	}

	frame, err := s.framer.BuildFrame(payload)
	if err != nil {
		return fmt.Errorf("frame disconnect packet: %w", err)
	}

	result := make(chan error, 1)
	select {
	case s.writeJobs <- writeJob{frame: frame, result: result}:
	case <-s.stopping:
		return ErrStreamClosed
	case <-ctx.Done():
		return ctx.Err()
	}

	select {
	case err := <-result:
		if err != nil {
			return fmt.Errorf("write disconnect packet: %w", err)
		}
	case <-s.stopping:
		return ErrStreamClosed
	case <-ctx.Done():
		return ctx.Err()
	}

	return s.observeOutbound(frameID, before, frame, packet, payload)
}

// Shutdown ends the stream politely: it stops accepting new writes, finishes
// the write already in flight, sends the disconnect packet when the role and
// state have one, and then interrupts the transport.
//
// It is idempotent, and every call returns the outcome of the first one. If
// ctx expires the stream falls back to an abortive close.
func (s *Stream) Shutdown(ctx context.Context, reason string) error {
	s.shutdownOnce.Do(func() { s.shutdownResult = s.shutdown(ctx, reason) })

	<-s.done

	return s.shutdownResult
}

func (s *Stream) shutdown(ctx context.Context, reason string) error {
	if !s.started.Load() {
		s.fail(ErrStreamClosed)
		s.stop()
		s.finish()

		return ErrStreamNotStarted
	}
	if s.terminated() {
		return s.rejection()
	}

	// From here on, Write reports ErrStreamClosing instead of accepting work.
	s.closing.Store(true)

	request := &shutdownRequest{reason: reason, result: make(chan error, 1)}
	select {
	case s.shutdownRequests <- request:
	case <-ctx.Done():
		return s.abortShutdown(ctx.Err())
	case <-s.stopping:
		return s.rejection()
	case <-s.done:
		return s.rejection()
	}

	// The coordinator can deliver its result in the same instant the stream
	// terminates. Preferring the result keeps a completed shutdown from being
	// reported as an abortive close just because the select raced.
	select {
	case err := <-request.result:
		return err
	default:
	}

	select {
	case err := <-request.result:
		return err
	case <-ctx.Done():
		return s.abortShutdown(ctx.Err())
	case <-s.done:
		select {
		case err := <-request.result:
			return err
		default:
			return s.rejection()
		}
	}
}

// abortShutdown turns an expired graceful shutdown into an abortive close.
func (s *Stream) abortShutdown(cause error) error {
	wrapped := fmt.Errorf("graceful shutdown did not finish: %w", cause)
	s.fail(wrapped)
	s.stop()

	return wrapped
}

// resolveTransition asks the session what a packet implies and lets the
// configured policy accept, replace, or suppress it. It never mutates the
// session: the caller decides when to commit.
func (s *Stream) resolveTransition(
	ctx context.Context,
	packet Packet,
	inbound bool,
	before Snapshot,
) (transitionDecision, error) {
	proposed, proposes, err := s.session.ProposeTransition(packet)
	if err != nil {
		return transitionDecision{}, fmt.Errorf("propose transition: %w", err)
	}
	if !proposes {
		return transitionDecision{}, nil
	}

	resolved, accepted, err := s.policy.Resolve(ctx, TransitionContext{
		Packet:   packet,
		Inbound:  inbound,
		Snapshot: before,
	}, proposed)
	if err != nil {
		return transitionDecision{}, fmt.Errorf("resolve transition: %w", err)
	}
	if !accepted || resolved.IsZero() {
		return transitionDecision{}, nil
	}

	if err := s.session.ValidateTransition(resolved); err != nil {
		return transitionDecision{}, fmt.Errorf("validate transition: %w", err)
	}

	return transitionDecision{transition: resolved, commit: true}, nil
}

// observeSecret records that secret material was installed. The key itself is
// present only under disclosure; otherwise the record marks the switch point
// so a capture is never silently missing it.
func (s *Stream) observeSecret(control Control) error {
	disclosing, ok := control.(SecretDisclosure)
	if !ok {
		return nil
	}

	// The label is always recorded; the material is only read when the
	// developer asked for it, so the default path never materializes a key.
	redacted := s.disclosureReason == ""

	var material []byte
	if !redacted {
		material = disclosing.DisclosedSecret()
	}

	snapshot := s.snapshot()

	return s.observe(observationInput{
		direction: s.session.Outbound(),
		stage:     ObservationSecret,
		frame:     s.frameCounter,
		before:    snapshot,
		after:     snapshot,
		secret:    &SecretMetadata{Label: disclosing.SecretLabel()},
		payload:   material,
		redacted:  redacted,
	})
}

// snapshot merges the session's view with the conduit's, so one snapshot
// describes everything a caller can configure at runtime.
func (s *Stream) snapshot() Snapshot {
	merged := s.session.Snapshot()
	pipeline := merged.Pipeline
	if pipeline == nil {
		pipeline = map[string]string{}
	}
	for key, value := range s.conduit.pipeline() {
		pipeline[key] = value
	}

	return NewSnapshot(merged.State, pipeline)
}

// processControl validates and applies one runtime control. A control that
// reconfigures the transport goes to the conduit; every other control goes to
// the session. An unsupported or rejected control fails the caller without
// stopping the stream.
func (s *Stream) processControl(request *controlRequest) {
	if transport, ok := request.control.(TransportControl); ok {
		if err := transport.ApplyTransport(s.conduit); err != nil {
			request.result <- err

			return
		}
		if err := s.observeSecret(request.control); err != nil {
			request.result <- err
			s.fail(err)
			s.stop()

			return
		}
		request.result <- nil

		return
	}

	if err := s.session.ValidateControl(request.control); err != nil {
		request.result <- err

		return
	}

	s.session.ApplyControl(request.control)
	request.result <- nil
}

// Snapshot returns the session configuration as of the last frame boundary.
//
// A running stream owns its session exclusively, so reading the session
// directly races the coordinator. This is the supported way to see the current
// state and pipeline settings.
func (s *Stream) Snapshot(ctx context.Context) (Snapshot, error) {
	if !s.started.Load() && !s.terminated() {
		return Snapshot{}, ErrStreamNotStarted
	}

	result := make(chan Snapshot, 1)
	select {
	case s.snapshotRequests <- result:
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case <-s.stopping:
		return Snapshot{}, s.rejection()
	case <-s.done:
		return Snapshot{}, s.rejection()
	}

	select {
	case snapshot := <-result:
		return snapshot, nil
	default:
	}

	select {
	case snapshot := <-result:
		return snapshot, nil
	case <-ctx.Done():
		return Snapshot{}, ctx.Err()
	case <-s.done:
		select {
		case snapshot := <-result:
			return snapshot, nil
		default:
			return Snapshot{}, s.rejection()
		}
	}
}

// SetState changes the protocol connection state at the next frame boundary.
func (s *Stream) SetState(ctx context.Context, state State) error {
	return s.Control(ctx, StateControl{State: state})
}

// Control applies one runtime pipeline change at a frame boundary.
//
// Cancelling ctx before the coordinator accepts the control guarantees that
// nothing changed. After acceptance the call waits for the result, because
// reporting cancellation for a control that already took effect would leave
// the caller unable to tell what the session is doing.
//
// A control never reinterprets a packet that is already in the Read queue.
func (s *Stream) Control(ctx context.Context, control Control) error {
	if err := s.writable(); err != nil {
		return err
	}
	if control == nil {
		return fmt.Errorf("%w: nil control", ErrInvalidStream)
	}

	request := &controlRequest{control: control, result: make(chan error, 1)}

	select {
	case s.controlRequests <- request:
	case <-ctx.Done():
		return ctx.Err()
	case <-s.stopping:
		return s.rejection()
	case <-s.done:
		return s.rejection()
	}

	select {
	case err := <-request.result:
		return err
	default:
	}

	select {
	case err := <-request.result:
		return err
	case <-s.done:
		select {
		case err := <-request.result:
			return err
		default:
			return s.rejection()
		}
	}
}

// Read returns the next inbound packet. Cancelling ctx abandons only this
// call: a queued packet stays queued for the next reader, and the stream keeps
// running.
func (s *Stream) Read(ctx context.Context) (Packet, error) {
	if err := s.readable(); err != nil {
		return Packet{}, err
	}

	// Drain what is already queued before reporting termination, so a stream
	// that ends does not discard packets a reader could still take.
	select {
	case item := <-s.inboundPackets:
		s.queued.release(1, item.bytes)
		return item.packet, nil
	default:
	}

	select {
	case item := <-s.inboundPackets:
		s.queued.release(1, item.bytes)
		return item.packet, nil
	case <-s.done:
		return Packet{}, s.terminalError()
	case <-ctx.Done():
		return Packet{}, ctx.Err()
	}
}

func (s *Stream) readable() error {
	if !s.started.Load() && !s.terminated() {
		return ErrStreamNotStarted
	}

	return nil
}

// terminalError reports why no more packets will arrive.
func (s *Stream) terminalError() error {
	if cause := s.firstCause(); cause != nil {
		return cause
	}

	return io.EOF
}

// Write sends one packet and returns only after the write pump has written the
// complete frame.
//
// Cancelling ctx before the coordinator starts the transport write guarantees
// that no byte was sent. Cancelling after it started aborts the stream, since
// the caller cannot know how much the peer received.
func (s *Stream) Write(ctx context.Context, packet Packet) error {
	if err := s.writable(); err != nil {
		return err
	}

	// One item of queue capacity models one packet in flight. The bytes stay
	// caller-owned until the coordinator encodes them, and that encoding is
	// covered by the reserved processing headroom.
	if err := s.queued.acquire(ctx, 1, 0); err != nil {
		return err
	}

	request := newWriteRequest(packet)
	select {
	case s.writeRequests <- request:
	case <-ctx.Done():
		s.queued.release(1, 0)
		return ctx.Err()
	case <-s.stopping:
		s.queued.release(1, 0)
		return s.rejection()
	case <-s.done:
		s.queued.release(1, 0)
		return s.rejection()
	}

	select {
	case err := <-request.result:
		return err
	case <-ctx.Done():
		if request.abandon() {
			// The transport never saw this packet.
			return ctx.Err()
		}

		// The write was already in progress, so the connection is no longer
		// in a known state.
		abort := fmt.Errorf("%w: %w", ErrAmbiguousWrite, ctx.Err())
		s.fail(abort)
		s.stop()

		return abort
	}
}

func (s *Stream) writable() error {
	if s.terminated() {
		return s.rejection()
	}
	if !s.started.Load() {
		return ErrStreamNotStarted
	}
	if s.closing.Load() {
		return s.rejection()
	}

	select {
	case <-s.stopping:
		return s.rejection()
	default:
	}

	return nil
}

// rejection explains why the stream refuses new work.
func (s *Stream) rejection() error {
	if cause := s.firstCause(); cause != nil {
		return cause
	}
	if s.closing.Load() && !s.terminated() {
		return ErrStreamClosing
	}

	return ErrStreamClosed
}
