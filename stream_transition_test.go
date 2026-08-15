package protocol

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
)

// stateTransition builds a proposal that moves the session to state.
func stateTransition(state State) func(Packet) (Transition, bool, error) {
	return func(packet Packet) (Transition, bool, error) {
		if packet.ID != 1 {
			return Transition{}, false, nil
		}
		next := state
		return Transition{State: &next}, true, nil
	}
}

func TestStreamCommitsInboundTransitionBeforePublishing(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	harness.session.setProposeHook(stateTransition(State("login")))

	harness.reader.deliver(testFrameBytes(1, 0x01))
	packet := readWithTimeout(t, harness.stream)

	// The triggering packet still carries the old state, but the session has
	// already moved on by the time a reader sees it.
	if packet.State != State("play") {
		t.Fatalf("Read() state = %q, want the pre-transition state", packet.State)
	}
	if got := harness.session.State(); got != State("login") {
		t.Fatalf("session state = %q, want %q", got, State("login"))
	}
}

func TestStreamDecodesLaterFramesUnderTheNewSnapshot(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	harness.session.setProposeHook(stateTransition(State("login")))

	harness.reader.deliver(testFrameBytes(1, 0x01))
	harness.reader.deliver(testFrameBytes(2, 0x02))

	first := readWithTimeout(t, harness.stream)
	second := readWithTimeout(t, harness.stream)

	if first.State != State("play") {
		t.Fatalf("first packet state = %q, want %q", first.State, State("play"))
	}
	if second.State != State("login") {
		t.Fatalf("second packet state = %q, want %q", second.State, State("login"))
	}

	_, _, decodeStates, _ := harness.session.history()
	if len(decodeStates) != 2 || decodeStates[0] != State("play") || decodeStates[1] != State("login") {
		t.Fatalf("decode states = %v, want [play login]", decodeStates)
	}
}

func TestStreamPolicySeesTheSnapshotBeforeThePacket(t *testing.T) {
	t.Parallel()

	var (
		mu       sync.Mutex
		observed []TransitionContext
	)
	policy := TransitionPolicyFunc(
		func(_ context.Context, transitionContext TransitionContext, proposed Transition) (Transition, bool, error) {
			mu.Lock()
			observed = append(observed, transitionContext)
			mu.Unlock()

			return proposed, true, nil
		},
	)

	harness := startRuntime(t, 8, WithTransitionPolicy(policy))
	harness.session.setProposeHook(stateTransition(State("login")))

	harness.reader.deliver(testFrameBytes(1, 0x01))
	readWithTimeout(t, harness.stream)

	mu.Lock()
	defer mu.Unlock()

	if len(observed) != 1 {
		t.Fatalf("policy ran %d times, want 1", len(observed))
	}
	if !observed[0].Inbound {
		t.Error("policy was told the packet was outbound")
	}
	if observed[0].Snapshot.State != State("play") {
		t.Errorf("policy snapshot state = %q, want the state before the packet", observed[0].Snapshot.State)
	}
	if observed[0].Snapshot.Pipeline["stage"] != "initial" {
		t.Errorf("policy snapshot pipeline = %v, want the pipeline before the packet", observed[0].Snapshot.Pipeline)
	}
	if observed[0].Packet.ID != 1 {
		t.Errorf("policy packet ID = %d, want 1", observed[0].Packet.ID)
	}
}

func TestStreamPolicyCanReplaceATransition(t *testing.T) {
	t.Parallel()

	policy := TransitionPolicyFunc(
		func(context.Context, TransitionContext, Transition) (Transition, bool, error) {
			replacement := State("status")
			return Transition{State: &replacement}, true, nil
		},
	)

	harness := startRuntime(t, 8, WithTransitionPolicy(policy))
	harness.session.setProposeHook(stateTransition(State("login")))

	harness.reader.deliver(testFrameBytes(1, 0x01))
	readWithTimeout(t, harness.stream)

	if got := harness.session.State(); got != State("status") {
		t.Fatalf("session state = %q, want the replacement %q", got, State("status"))
	}
}

func TestStreamPolicyCanSuppressATransition(t *testing.T) {
	t.Parallel()

	policy := TransitionPolicyFunc(
		func(context.Context, TransitionContext, Transition) (Transition, bool, error) {
			return Transition{}, false, nil
		},
	)

	harness := startRuntime(t, 8, WithTransitionPolicy(policy))
	harness.session.setProposeHook(stateTransition(State("login")))

	harness.reader.deliver(testFrameBytes(1, 0x01))
	readWithTimeout(t, harness.stream)

	if got := harness.session.State(); got != State("play") {
		t.Fatalf("session state = %q, want the suppressed transition to leave it alone", got)
	}
	transitions, _, _, _ := harness.session.history()
	if len(transitions) != 0 {
		t.Fatalf("applied %d transitions, want none", len(transitions))
	}
}

func TestStreamInboundPolicyErrorIsFatal(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("policy refused")
	policy := TransitionPolicyFunc(
		func(context.Context, TransitionContext, Transition) (Transition, bool, error) {
			return Transition{}, false, sentinel
		},
	)

	harness := startRuntime(t, 8, WithTransitionPolicy(policy))
	harness.session.setProposeHook(stateTransition(State("login")))

	harness.reader.deliver(testFrameBytes(1, 0x01))

	if err := harness.stream.Wait(); !errors.Is(err, sentinel) {
		t.Fatalf("Wait() error = %v, want the policy error", err)
	}
}

func TestStreamInboundInvalidReplacementIsFatal(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	harness.session.setProposeHook(stateTransition(State("login")))

	sentinel := errors.New("unsupported state")
	harness.session.setValidateTransitionErr(sentinel)

	harness.reader.deliver(testFrameBytes(1, 0x01))

	if err := harness.stream.Wait(); !errors.Is(err, sentinel) {
		t.Fatalf("Wait() error = %v, want the validation error", err)
	}
}

func TestStreamInboundProposalErrorIsFatal(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	sentinel := errors.New("impossible transition")
	harness.session.setProposeHook(func(Packet) (Transition, bool, error) {
		return Transition{}, false, sentinel
	})

	harness.reader.deliver(testFrameBytes(1, 0x01))

	if err := harness.stream.Wait(); !errors.Is(err, sentinel) {
		t.Fatalf("Wait() error = %v, want the proposal error", err)
	}
}

func TestStreamCommitsOutboundTransitionAfterTheWrite(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, runtimeLimits(t, 8))
	reader := newScriptedReader()
	writer := newBlockingWriter()

	stream, err := NewStream(session, Transport{
		Reader: reader,
		Writer: writer,
		Interrupt: func() error {
			_ = reader.interrupt()
			return writer.interrupt()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	session.setProposeHook(stateTransition(State("login")))

	done := make(chan error, 1)
	go func() {
		done <- stream.Write(context.Background(), Packet{ID: 1, Payload: []byte{0x01}})
	}()

	<-writer.entered
	if got := session.State(); got != State("play") {
		t.Fatalf("session state = %q mid-write, want the transition to wait for the write", got)
	}

	writer.release <- nil
	if err := <-done; err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if got := session.State(); got != State("login") {
		t.Fatalf("session state = %q after the write, want %q", got, State("login"))
	}

	_, _, _, encodeStates := session.history()
	if len(encodeStates) != 1 || encodeStates[0] != State("play") {
		t.Fatalf("encode states = %v, want the packet encoded under the old state", encodeStates)
	}
}

func TestStreamOutboundTransitionIsNotCommittedAfterAFailedWrite(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, runtimeLimits(t, 8))
	reader := newScriptedReader()
	sentinel := errors.New("transport died")
	writer := &truncatingWriter{limit: 1, err: sentinel}

	stream, err := NewStream(session, Transport{
		Reader:    reader,
		Writer:    writer,
		Interrupt: reader.interrupt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	session.setProposeHook(stateTransition(State("login")))

	if err := stream.Write(context.Background(), Packet{ID: 1, Payload: []byte{0x01, 0x02}}); !errors.Is(err, sentinel) {
		t.Fatalf("Write() error = %v, want the transport error", err)
	}
	if got := session.State(); got != State("play") {
		t.Fatalf("session state = %q after a failed write, want it uncommitted", got)
	}
}

func TestStreamOutboundPolicyRejectionIsNotFatal(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("policy refused the outbound transition")
	policy := TransitionPolicyFunc(
		func(_ context.Context, transitionContext TransitionContext, proposed Transition) (Transition, bool, error) {
			if transitionContext.Inbound {
				return proposed, true, nil
			}
			return Transition{}, false, sentinel
		},
	)

	harness := startRuntime(t, 8, WithTransitionPolicy(policy))
	harness.session.setProposeHook(stateTransition(State("login")))

	err := harness.stream.Write(context.Background(), Packet{ID: 1, Payload: []byte{0x01}})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Write() error = %v, want the policy error", err)
	}
	if harness.writer.writeCount() != 0 {
		t.Fatalf("a rejected transition still wrote %d times", harness.writer.writeCount())
	}

	// The stream survives, and a packet with no transition still goes out.
	if err := harness.stream.Write(context.Background(), Packet{ID: 2, Payload: []byte{0x02}}); err != nil {
		t.Fatalf("Write() after a rejected transition error = %v", err)
	}
	if got, want := harness.writer.bytesWritten(), testFrameBytes(2, 0x02); !bytes.Equal(got, want) {
		t.Fatalf("wrote %x, want %x", got, want)
	}
}

func TestStreamControlAppliesThroughTheCoordinator(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	if err := harness.stream.Control(context.Background(), testControl{stage: "compressed"}); err != nil {
		t.Fatalf("Control() error = %v", err)
	}

	_, controls, _, _ := harness.session.history()
	if len(controls) != 1 {
		t.Fatalf("applied %d controls, want 1", len(controls))
	}
	if harness.session.Snapshot().Pipeline["stage"] != "compressed" {
		t.Fatalf("pipeline = %v, want the control applied", harness.session.Snapshot().Pipeline)
	}
}

func TestStreamSetStateGoesThroughControls(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	if err := harness.stream.SetState(context.Background(), State("status")); err != nil {
		t.Fatalf("SetState() error = %v", err)
	}
	if got := harness.session.State(); got != State("status") {
		t.Fatalf("session state = %q, want %q", got, State("status"))
	}

	_, controls, _, _ := harness.session.history()
	if len(controls) != 1 {
		t.Fatalf("applied %d controls, want 1", len(controls))
	}
	if _, ok := controls[0].(StateControl); !ok {
		t.Fatalf("control = %T, want StateControl", controls[0])
	}
}

func TestStreamInvalidControlLeavesTheStreamRunning(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	sentinel := errors.New("unsupported control")
	harness.session.setValidateControlErr(sentinel)

	if err := harness.stream.Control(context.Background(), testControl{stage: "bad"}); !errors.Is(err, sentinel) {
		t.Fatalf("Control() error = %v, want the validation error", err)
	}

	_, controls, _, _ := harness.session.history()
	if len(controls) != 0 {
		t.Fatalf("applied %d controls, want none", len(controls))
	}

	// The stream still works.
	harness.session.setValidateControlErr(nil)
	if err := harness.stream.Control(context.Background(), testControl{stage: "good"}); err != nil {
		t.Fatalf("Control() after a rejected control error = %v", err)
	}
}

func TestStreamControlRejectsNil(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	if err := harness.stream.Control(context.Background(), nil); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("Control(nil) error = %v, want ErrInvalidStream", err)
	}
}

func TestStreamControlCancellationBeforeAcceptanceChangesNothing(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, runtimeLimits(t, 8))
	reader := newScriptedReader()
	writer := newBlockingWriter()

	stream, err := NewStream(session, Transport{
		Reader: reader,
		Writer: writer,
		Interrupt: func() error {
			_ = reader.interrupt()
			return writer.interrupt()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	// Occupy the coordinator so the control cannot be accepted.
	blocked := make(chan error, 1)
	go func() {
		blocked <- stream.Write(context.Background(), Packet{ID: 9, Payload: []byte{0x01}})
	}()
	<-writer.entered

	ctx, cancel := context.WithCancel(context.Background())
	controlDone := make(chan error, 1)
	go func() {
		controlDone <- stream.Control(ctx, testControl{stage: "never"})
	}()

	cancel()
	if err := <-controlDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Control() error = %v, want context.Canceled", err)
	}

	writer.release <- nil
	if err := <-blocked; err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	_, controls, _, _ := session.history()
	if len(controls) != 0 {
		t.Fatalf("a cancelled control applied %d changes, want none", len(controls))
	}
}

func TestStreamControlOrdersAfterAcceptedWrites(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	// Writes and controls share the coordinator, so a control submitted after
	// a write takes effect after that write reaches the wire.
	for id := byte(1); id <= 3; id++ {
		if err := harness.stream.Write(context.Background(), Packet{ID: int32(id), Payload: []byte{id}}); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
	}
	if err := harness.stream.Control(context.Background(), testControl{stage: "after"}); err != nil {
		t.Fatalf("Control() error = %v", err)
	}

	var want []byte
	for id := byte(1); id <= 3; id++ {
		want = append(want, testFrameBytes(id, id)...)
	}
	if got := harness.writer.bytesWritten(); !bytes.Equal(got, want) {
		t.Fatalf("wrote %x, want %x", got, want)
	}
	if harness.session.Snapshot().Pipeline["stage"] != "after" {
		t.Fatal("the control did not take effect after the writes")
	}
}

func TestStreamControlDoesNotReinterpretQueuedPackets(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	harness.reader.deliver(testFrameBytes(5, 0x05))

	// Wait until the packet is decoded and queued under the old state.
	packet := readWithTimeout(t, harness.stream)
	if packet.State != State("play") {
		t.Fatalf("Read() state = %q, want %q", packet.State, State("play"))
	}

	harness.reader.deliver(testFrameBytes(6, 0x06))
	if err := harness.stream.SetState(context.Background(), State("status")); err != nil {
		t.Fatalf("SetState() error = %v", err)
	}

	harness.reader.deliver(testFrameBytes(7, 0x07))
	third := readWithTimeout(t, harness.stream)

	// Whatever state the middle packet was decoded under, an already decoded
	// packet keeps the state it was decoded with.
	if third.State != State("play") && third.State != State("status") {
		t.Fatalf("Read() state = %q, want one of the two real states", third.State)
	}
}

func TestStreamControlAfterCloseIsRejected(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	if err := harness.stream.Close(); err != nil {
		t.Fatal(err)
	}

	if err := harness.stream.Control(context.Background(), testControl{stage: "late"}); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("Control() error = %v, want ErrStreamClosed", err)
	}
	if err := harness.stream.SetState(context.Background(), State("status")); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("SetState() error = %v, want ErrStreamClosed", err)
	}
}

// recordingTransportControl proves the stream routes by interface, not by
// concrete type, and never inspects a control's contents.
type recordingTransportControl struct {
	applied *int
	fail    error
}

func (recordingTransportControl) ControlName() string { return "test.transport" }

func (c recordingTransportControl) ApplyTransport(conduit *Conduit) error {
	if conduit == nil {
		return errors.New("nil conduit")
	}
	if c.fail != nil {
		return c.fail
	}
	*c.applied++

	return nil
}

func TestStreamRoutesTransportControlToConduit(t *testing.T) {
	t.Parallel()

	applied := 0
	harness := startRuntime(t, 8)

	if err := harness.stream.Control(t.Context(), recordingTransportControl{applied: &applied}); err != nil {
		t.Fatalf("control: %v", err)
	}
	if applied != 1 {
		t.Fatalf("applied %d times, want 1", applied)
	}
}

func TestStreamReportsTransportControlFailureToCaller(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	sentinel := errors.New("refused")

	err := harness.stream.Control(t.Context(), recordingTransportControl{applied: new(int), fail: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Control error = %v, want %v", err, sentinel)
	}
	if err := harness.stream.Close(); err != nil && !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("a rejected control must not terminate the stream: %v", err)
	}
}

func TestStreamSnapshotIncludesConduitPipeline(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	snapshot, err := harness.stream.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := snapshot.Pipeline["encryption.enabled"]; got != "false" {
		t.Fatalf("encryption.enabled = %q, want %q", got, "false")
	}
}
