package protocol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
)

// ErrInvalidFrame reports a frame that cannot describe a packet body.
var ErrInvalidFrame = errors.New("invalid protocol frame")

// Frame is one complete wire frame together with the offset at which its
// packet body begins. The wire bytes include every transport-visible byte, so
// observers record exactly what crossed the connection.
//
// A Frame owns its buffer. WireBytes and Payload return borrowed views that
// callers must neither retain nor mutate.
type Frame struct {
	wire          []byte
	payloadOffset int
}

// NewFrame takes ownership of wire and describes the packet body that begins
// at payloadOffset. The caller must not reuse wire afterwards.
func NewFrame(wire []byte, payloadOffset int) (Frame, error) {
	if len(wire) == 0 {
		return Frame{}, fmt.Errorf("%w: empty wire buffer", ErrInvalidFrame)
	}
	if payloadOffset < 0 {
		return Frame{}, fmt.Errorf("%w: negative payload offset %d", ErrInvalidFrame, payloadOffset)
	}
	if payloadOffset > len(wire) {
		return Frame{}, fmt.Errorf(
			"%w: payload offset %d exceeds wire length %d",
			ErrInvalidFrame,
			payloadOffset,
			len(wire),
		)
	}

	return Frame{wire: wire, payloadOffset: payloadOffset}, nil
}

// WireBytes returns a borrowed view of every byte of the frame.
func (f Frame) WireBytes() []byte { return f.wire }

// Payload returns a borrowed view of the packet body inside the frame.
func (f Frame) Payload() []byte {
	if f.wire == nil {
		return nil
	}

	return f.wire[f.payloadOffset:]
}

// Framer moves complete frames between a transport and owned buffers. It holds
// no connection state, so a stream can call it from either pump.
type Framer interface {
	// ReadFrame reads exactly one complete frame.
	ReadFrame(io.Reader) (Frame, error)
	// BuildFrame wraps one encoded packet body in a transmittable frame.
	BuildFrame([]byte) (Frame, error)
	// WriteFrame writes every byte of one frame or returns an error.
	WriteFrame(io.Writer, Frame) error
}

// Control is one runtime pipeline change a session understands. Editions
// define their own controls, so the stream never interprets their contents.
type Control interface {
	ControlName() string
}

// StateControl changes the protocol connection state.
type StateControl struct {
	State State
}

// ControlName implements Control.
func (StateControl) ControlName() string { return "state" }

// Transition is one atomic change that a packet implies. Either field may be
// nil, and an entirely empty Transition means the packet changes nothing.
type Transition struct {
	State   *State
	Control Control
}

// IsZero reports whether the transition changes nothing.
func (t Transition) IsZero() bool { return t.State == nil && t.Control == nil }

// Snapshot describes session configuration at one frame boundary. Pipeline
// holds edition-defined key and value pairs such as compression settings.
type Snapshot struct {
	State    State
	Pipeline map[string]string
}

// NewSnapshot copies pipeline so later mutation cannot change the snapshot.
func NewSnapshot(state State, pipeline map[string]string) Snapshot {
	return Snapshot{State: state, Pipeline: maps.Clone(emptyIfNil(pipeline))}
}

// Clone returns an independent copy of the snapshot.
func (s Snapshot) Clone() Snapshot { return NewSnapshot(s.State, s.Pipeline) }

func emptyIfNil(pipeline map[string]string) map[string]string {
	if pipeline == nil {
		return map[string]string{}
	}

	return pipeline
}

// Session owns mutable coding and pipeline state for one connection. A session
// performs no I/O of its own: it turns bytes into packets, packets into bytes,
// and packets into proposed transitions, while a stream owns the transport.
//
// A Session is not safe for concurrent use. Once it is handed to a Stream, the
// stream owns it exclusively and callers must use Stream.Snapshot rather than
// reading the session directly.
//
// Validation and application are deliberately split. Every check that can fail
// belongs in a Validate method so the matching Apply method cannot fail after
// bytes have already left the process.
type Session interface {
	Framer() Framer
	Role() Role
	Limits() Limits
	State() State
	Inbound() Direction
	Outbound() Direction
	Snapshot() Snapshot

	ValidateState(State) error
	SetState(State)

	DecodeFrame([]byte) (Packet, error)
	EncodeFrame(Packet) ([]byte, error)

	ProposeTransition(Packet) (Transition, bool, error)
	ValidateTransition(Transition) error
	ApplyTransition(Transition)

	ValidateControl(Control) error
	ApplyControl(Control)

	// Disconnect builds the state-appropriate disconnect packet. It reports
	// false when the current role and state have no such packet.
	Disconnect(string) (Packet, bool, error)
}

// TransitionContext describes the packet that produced a proposed transition.
type TransitionContext struct {
	// Packet is the decoded inbound packet or the accepted outbound packet.
	Packet Packet
	// Inbound reports whether the packet arrived from the peer.
	Inbound bool
	// Snapshot is the session configuration before the packet is applied.
	Snapshot Snapshot
}

// TransitionPolicy decides what a stream does with a proposed transition. A
// policy may accept the proposal, replace it, or suppress it entirely.
type TransitionPolicy interface {
	Resolve(context.Context, TransitionContext, Transition) (Transition, bool, error)
}

// TransitionPolicyFunc adapts a function to TransitionPolicy.
type TransitionPolicyFunc func(context.Context, TransitionContext, Transition) (Transition, bool, error)

// Resolve implements TransitionPolicy.
func (f TransitionPolicyFunc) Resolve(
	ctx context.Context,
	transitionContext TransitionContext,
	proposed Transition,
) (Transition, bool, error) {
	return f(ctx, transitionContext, proposed)
}

// AcceptTransitions returns the default policy, which commits every proposal
// the session makes.
func AcceptTransitions() TransitionPolicy {
	return TransitionPolicyFunc(
		func(_ context.Context, _ TransitionContext, proposed Transition) (Transition, bool, error) {
			return proposed, true, nil
		},
	)
}
