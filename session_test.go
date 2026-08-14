package protocol

import (
	"context"
	"errors"
	"io"
	"testing"
)

// stubFramer is a minimal Framer used to prove the contract compiles and that
// the stream never needs more than these three operations.
type stubFramer struct{}

func (stubFramer) ReadFrame(io.Reader) (Frame, error) { return Frame{}, io.EOF }

func (stubFramer) BuildFrame(payload []byte) (Frame, error) { return NewFrame(payload, 0) }

func (stubFramer) WriteFrame(io.Writer, Frame) error { return nil }

// stubSession is a minimal Session used to prove the contract compiles.
type stubSession struct {
	limits Limits
	state  State
}

func (s *stubSession) Framer() Framer            { return stubFramer{} }
func (s *stubSession) Role() Role                { return RoleClient }
func (s *stubSession) Limits() Limits            { return s.limits }
func (s *stubSession) State() State              { return s.state }
func (s *stubSession) Inbound() Direction        { return DirectionClientbound }
func (s *stubSession) Outbound() Direction       { return DirectionServerbound }
func (s *stubSession) Snapshot() Snapshot        { return NewSnapshot(s.state, nil) }
func (s *stubSession) ValidateState(State) error { return nil }
func (s *stubSession) SetState(state State)      { s.state = state }

func (s *stubSession) DecodeFrame(payload []byte) (Packet, error) {
	return Packet{Payload: payload}, nil
}

func (s *stubSession) EncodeFrame(packet Packet) ([]byte, error) { return packet.Payload, nil }

func (s *stubSession) ProposeTransition(Packet) (Transition, bool, error) {
	return Transition{}, false, nil
}

func (s *stubSession) ValidateTransition(Transition) error { return nil }
func (s *stubSession) ApplyTransition(Transition)          {}
func (s *stubSession) ValidateControl(Control) error       { return nil }
func (s *stubSession) ApplyControl(Control)                {}

func (s *stubSession) Disconnect(string) (Packet, bool, error) { return Packet{}, false, nil }

var (
	_ Framer  = stubFramer{}
	_ Session = (*stubSession)(nil)
)

func TestNewFrameRejectsInvalidInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		wire   []byte
		offset int
	}{
		{name: "nil wire", wire: nil, offset: 0},
		{name: "empty wire", wire: []byte{}, offset: 0},
		{name: "negative offset", wire: []byte{1, 2}, offset: -1},
		{name: "offset beyond wire", wire: []byte{1, 2}, offset: 3},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if _, err := NewFrame(testCase.wire, testCase.offset); !errors.Is(err, ErrInvalidFrame) {
				t.Fatalf("NewFrame() error = %v, want ErrInvalidFrame", err)
			}
		})
	}
}

func TestNewFrameOwnsWireAndExposesPayload(t *testing.T) {
	t.Parallel()

	wire := []byte{0x03, 0x00, 0xAA, 0xBB}
	frame, err := NewFrame(wire, 1)
	if err != nil {
		t.Fatalf("NewFrame() error = %v", err)
	}

	if got := string(frame.WireBytes()); got != string(wire) {
		t.Fatalf("WireBytes() = % x, want % x", frame.WireBytes(), wire)
	}
	if got, want := string(frame.Payload()), string(wire[1:]); got != want {
		t.Fatalf("Payload() = % x, want % x", frame.Payload(), wire[1:])
	}

	// NewFrame takes ownership rather than copying. Proving the view aliases
	// the supplied buffer documents that callers must hand the buffer over.
	wire[2] = 0x11
	if frame.Payload()[1] != 0x11 {
		t.Fatal("NewFrame() copied its input, want ownership transfer")
	}
}

func TestFrameAllowsEmptyPayloadView(t *testing.T) {
	t.Parallel()

	frame, err := NewFrame([]byte{0x00}, 1)
	if err != nil {
		t.Fatalf("NewFrame() error = %v", err)
	}
	if len(frame.Payload()) != 0 {
		t.Fatalf("Payload() length = %d, want 0", len(frame.Payload()))
	}
}

func TestZeroFrameHasNoBytes(t *testing.T) {
	t.Parallel()

	var frame Frame
	if frame.WireBytes() != nil || frame.Payload() != nil {
		t.Fatal("Frame{} exposed bytes, want nil views")
	}
}

func TestStateControlName(t *testing.T) {
	t.Parallel()

	var control Control = StateControl{State: State("play")}
	if control.ControlName() != "state" {
		t.Fatalf("ControlName() = %q, want %q", control.ControlName(), "state")
	}
}

func TestNewSnapshotClonesPipeline(t *testing.T) {
	t.Parallel()

	pipeline := map[string]string{"compression.enabled": "false"}
	snapshot := NewSnapshot(State("login"), pipeline)

	pipeline["compression.enabled"] = "true"
	if snapshot.Pipeline["compression.enabled"] != "false" {
		t.Fatal("NewSnapshot() retained the caller's map, want a clone")
	}

	clone := snapshot.Clone()
	clone.Pipeline["compression.enabled"] = "true"
	if snapshot.Pipeline["compression.enabled"] != "false" {
		t.Fatal("Clone() shared the pipeline map, want independent copies")
	}
}

func TestSnapshotCloneHandlesNilPipeline(t *testing.T) {
	t.Parallel()

	snapshot := NewSnapshot(State("status"), nil)
	if snapshot.Pipeline == nil {
		t.Fatal("NewSnapshot() left a nil pipeline, want an empty map")
	}
	if len(snapshot.Clone().Pipeline) != 0 {
		t.Fatal("Clone() invented pipeline entries")
	}
}

func TestTransitionIsZero(t *testing.T) {
	t.Parallel()

	var empty Transition
	if !empty.IsZero() {
		t.Fatal("Transition{}.IsZero() = false, want true")
	}

	state := State("status")
	if (Transition{State: &state}).IsZero() {
		t.Fatal("state transition reported as zero")
	}
	if (Transition{Control: StateControl{State: state}}).IsZero() {
		t.Fatal("control transition reported as zero")
	}
}

func TestAcceptTransitionsPassesProposalThrough(t *testing.T) {
	t.Parallel()

	state := State("login")
	proposed := Transition{State: &state}

	resolved, accepted, err := AcceptTransitions().Resolve(
		context.Background(),
		TransitionContext{Inbound: true},
		proposed,
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if !accepted {
		t.Fatal("Resolve() accepted = false, want true")
	}
	if resolved.State == nil || *resolved.State != state {
		t.Fatalf("Resolve() transition = %+v, want the proposal", resolved)
	}
}

func TestTransitionPolicyFuncImplementsPolicy(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("rejected")
	var policy TransitionPolicy = TransitionPolicyFunc(
		func(context.Context, TransitionContext, Transition) (Transition, bool, error) {
			return Transition{}, false, sentinel
		},
	)

	if _, _, err := policy.Resolve(context.Background(), TransitionContext{}, Transition{}); !errors.Is(err, sentinel) {
		t.Fatalf("Resolve() error = %v, want the policy error", err)
	}
}
