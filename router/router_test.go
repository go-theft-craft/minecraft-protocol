package router_test

import (
	"context"
	"errors"
	"io"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/middleware"
	"github.com/go-theft-craft/minecraft-protocol/router"
)

const (
	statePlay  = protocol.State("play")
	keepAlive  = "keep_alive"
	chat       = "chat"
	unknownHex = 0x7f
)

// fakeProtocol is a protocol that names three packets and creates no session.
// The router never creates one, which is the point of testing against this
// rather than against a generated version.
type fakeProtocol struct{}

func (fakeProtocol) ID() string                { return "test/1" }
func (fakeProtocol) Edition() protocol.Edition { return protocol.EditionJava }
func (fakeProtocol) Version() protocol.Version {
	return protocol.Version{Name: "1", Protocol: 1}
}

func (fakeProtocol) NewSession(protocol.Role, protocol.Limits) (protocol.Session, error) {
	return nil, errors.New("the router must not create a session")
}

var fakeNames = map[protocol.Direction]map[string]int32{
	protocol.DirectionClientbound: {keepAlive: 0x01, chat: 0x02},
	protocol.DirectionServerbound: {keepAlive: 0x03},
}

func (fakeProtocol) PacketID(state protocol.State, direction protocol.Direction, name string) (int32, bool) {
	if state != statePlay {
		return 0, false
	}
	id, ok := fakeNames[direction][name]

	return id, ok
}

func (fakeProtocol) PacketName(state protocol.State, direction protocol.Direction, id int32) (string, bool) {
	if state != statePlay {
		return "", false
	}
	for name, candidate := range fakeNames[direction] {
		if candidate == id {
			return name, true
		}
	}

	return "", false
}

// sliceReceiver hands out a fixed list of packets and then reports EOF, so a
// router test never needs a stream or a connection.
type sliceReceiver struct {
	packets []protocol.Packet
	index   int
	err     error
}

func (r *sliceReceiver) Receive(context.Context) (protocol.Packet, error) {
	if r.index >= len(r.packets) {
		if r.err != nil {
			return protocol.Packet{}, r.err
		}

		return protocol.Packet{}, io.EOF
	}
	packet := r.packets[r.index]
	r.index++

	return packet, nil
}

func clientbound(id int32) protocol.Packet {
	return protocol.Packet{State: statePlay, Direction: protocol.DirectionClientbound, ID: id}
}

func newRouter(t *testing.T, options ...router.Option) *router.Router {
	t.Helper()

	made, err := router.New(fakeProtocol{}, options...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return made
}

func TestHandlersOnOneKeyRunInRegistrationOrder(t *testing.T) {
	t.Parallel()

	var order []string
	made := newRouter(t)

	for _, name := range []string{"first", "second", "third"} {
		if err := made.Handle(statePlay, protocol.DirectionClientbound, keepAlive,
			middleware.HandlerFunc(func(context.Context, protocol.Packet) error {
				order = append(order, name)

				return nil
			}),
		); err != nil {
			t.Fatalf("Handle(%s): %v", name, err)
		}
	}

	if err := made.Dispatch(t.Context(), clientbound(0x01)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}

	if len(order) != 3 || order[0] != "first" || order[2] != "third" {
		t.Fatalf("order = %v, want registration order", order)
	}
}

func TestAHandlerErrorStopsTheChainAndAbortsRun(t *testing.T) {
	t.Parallel()

	refusal := errors.New("refused")
	var ran int

	made := newRouter(t)
	if err := made.Handle(statePlay, protocol.DirectionClientbound, keepAlive,
		middleware.HandlerFunc(func(context.Context, protocol.Packet) error {
			ran++

			return refusal
		}),
	); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := made.Handle(statePlay, protocol.DirectionClientbound, keepAlive,
		middleware.HandlerFunc(func(context.Context, protocol.Packet) error {
			ran++

			return nil
		}),
	); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	receiver := &sliceReceiver{packets: []protocol.Packet{clientbound(0x01), clientbound(0x01)}}
	err := made.Run(t.Context(), receiver)
	if !errors.Is(err, refusal) {
		t.Fatalf("Run error = %v, want the handler's refusal", err)
	}
	if !errors.Is(err, router.ErrHandler) {
		t.Fatalf("Run error = %v, want it wrapped as ErrHandler", err)
	}
	if ran != 1 {
		t.Fatalf("%d handlers ran, want the chain to stop at the first error", ran)
	}
	if receiver.index != 1 {
		t.Fatalf("the router consumed %d packets, want it to stop at the failing one", receiver.index)
	}
}

func TestAnUnregisteredPacketIsSkipped(t *testing.T) {
	t.Parallel()

	made := newRouter(t)
	if err := made.Run(t.Context(), &sliceReceiver{packets: []protocol.Packet{clientbound(unknownHex)}}); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestTheFallbackReceivesExactlyTheUnregisteredPackets(t *testing.T) {
	t.Parallel()

	var fell []int32
	made := newRouter(t)

	if err := made.Handle(statePlay, protocol.DirectionClientbound, keepAlive,
		middleware.HandlerFunc(func(context.Context, protocol.Packet) error { return nil }),
	); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if err := made.Fallback(middleware.HandlerFunc(func(_ context.Context, packet protocol.Packet) error {
		fell = append(fell, packet.ID)

		return nil
	})); err != nil {
		t.Fatalf("Fallback: %v", err)
	}

	receiver := &sliceReceiver{packets: []protocol.Packet{
		clientbound(0x01), clientbound(0x02), clientbound(unknownHex),
	}}
	if err := made.Run(t.Context(), receiver); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(fell) != 2 || fell[0] != 0x02 || fell[1] != unknownHex {
		t.Fatalf("fallback saw %v, want only the unregistered packets", fell)
	}
}

func TestRegisteringAnUnknownNameFailsAtRegistration(t *testing.T) {
	t.Parallel()

	made := newRouter(t)
	err := made.Handle(statePlay, protocol.DirectionClientbound, "no_such_packet",
		middleware.HandlerFunc(func(context.Context, protocol.Packet) error { return nil }),
	)
	if !errors.Is(err, router.ErrUnknownPacket) {
		t.Fatalf("Handle error = %v, want ErrUnknownPacket", err)
	}
}

func TestRegisteringAfterRunStartsFails(t *testing.T) {
	t.Parallel()

	made := newRouter(t)
	if err := made.Run(t.Context(), &sliceReceiver{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	err := made.Handle(statePlay, protocol.DirectionClientbound, keepAlive,
		middleware.HandlerFunc(func(context.Context, protocol.Packet) error { return nil }),
	)
	if !errors.Is(err, router.ErrRunning) {
		t.Fatalf("Handle error = %v, want ErrRunning", err)
	}
	if err := made.Fallback(middleware.HandlerFunc(func(context.Context, protocol.Packet) error {
		return nil
	})); !errors.Is(err, router.ErrRunning) {
		t.Fatalf("Fallback error = %v, want ErrRunning", err)
	}
}

func TestRunReportsCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	made := newRouter(t)
	receiver := &sliceReceiver{packets: []protocol.Packet{clientbound(0x01)}}
	if err := made.Run(ctx, receiver); !errors.Is(err, context.Canceled) {
		t.Fatalf("Run error = %v, want context.Canceled", err)
	}
}

func TestRunReturnsNilOnACleanEOF(t *testing.T) {
	t.Parallel()

	made := newRouter(t)
	if err := made.Run(t.Context(), &sliceReceiver{}); err != nil {
		t.Fatalf("Run error = %v, want nil on EOF", err)
	}
}

func TestRunReportsAReceiverFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("transport gone")
	made := newRouter(t)

	err := made.Run(t.Context(), &sliceReceiver{err: sentinel})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Run error = %v, want the receiver's failure", err)
	}
}

func TestHandleIDDoesNotNeedANameTable(t *testing.T) {
	t.Parallel()

	var seen bool
	made := newRouter(t)

	if err := made.HandleID(statePlay, protocol.DirectionClientbound, unknownHex,
		middleware.HandlerFunc(func(context.Context, protocol.Packet) error {
			seen = true

			return nil
		}),
	); err != nil {
		t.Fatalf("HandleID: %v", err)
	}
	if err := made.Dispatch(t.Context(), clientbound(unknownHex)); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if !seen {
		t.Fatal("a handler registered by ID never ran")
	}
}

func TestReceiveMiddlewareWrapsEveryDispatch(t *testing.T) {
	t.Parallel()

	var wrapped int
	made := newRouter(t, router.WithReceiveMiddleware(func(next middleware.Handler) middleware.Handler {
		return middleware.HandlerFunc(func(ctx context.Context, packet protocol.Packet) error {
			wrapped++

			return next.Handle(ctx, packet)
		})
	}))

	if err := made.Handle(statePlay, protocol.DirectionClientbound, keepAlive,
		middleware.HandlerFunc(func(context.Context, protocol.Packet) error { return nil }),
	); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	// Once for the registered packet, once for the unregistered one: the
	// wrapper describes the dispatch, not the handler, so it runs either way.
	receiver := &sliceReceiver{packets: []protocol.Packet{clientbound(0x01), clientbound(unknownHex)}}
	if err := made.Run(t.Context(), receiver); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if wrapped != 2 {
		t.Fatalf("middleware ran %d times, want 2", wrapped)
	}
}

func TestNewRejectsANilProtocol(t *testing.T) {
	t.Parallel()

	if _, err := router.New(nil); !errors.Is(err, router.ErrInvalidRouter) {
		t.Fatalf("New error = %v, want ErrInvalidRouter", err)
	}
}

func TestHandleRejectsANilHandler(t *testing.T) {
	t.Parallel()

	made := newRouter(t)
	if err := made.Handle(statePlay, protocol.DirectionClientbound, keepAlive, nil); !errors.Is(err, router.ErrInvalidRouter) {
		t.Fatalf("Handle error = %v, want ErrInvalidRouter", err)
	}
}

// TestDispatchAllocatesNothingPerPacket keeps the hot path honest: a router
// sits under every inbound packet of a connection, and a lookup that allocated
// would allocate thousands of times a second.
func TestDispatchAllocatesNothingPerPacket(t *testing.T) {
	made := newRouter(t)
	if err := made.Handle(statePlay, protocol.DirectionClientbound, keepAlive,
		middleware.HandlerFunc(func(context.Context, protocol.Packet) error { return nil }),
	); err != nil {
		t.Fatalf("Handle: %v", err)
	}

	ctx := t.Context()
	registered := clientbound(0x01)
	unregistered := clientbound(unknownHex)

	allocations := testing.AllocsPerRun(100, func() {
		_ = made.Dispatch(ctx, registered)
		_ = made.Dispatch(ctx, unregistered)
	})
	if allocations != 0 {
		t.Fatalf("Dispatch allocated %.1f times per packet pair, want 0", allocations)
	}
}

// TestRouterCompilesAgainstANonStreamReceiver is the independence claim as a
// test: nothing in this package requires a stream, and this receiver is a
// struct literal with no transport behind it.
func TestRouterCompilesAgainstANonStreamReceiver(t *testing.T) {
	t.Parallel()

	var receiver router.Receiver = &sliceReceiver{}
	if _, err := receiver.Receive(t.Context()); !errors.Is(err, io.EOF) {
		t.Fatalf("Receive error = %v, want io.EOF", err)
	}
}

func TestFromStreamRejectsANilStream(t *testing.T) {
	t.Parallel()

	if _, err := router.FromStream(nil); !errors.Is(err, router.ErrInvalidRouter) {
		t.Fatalf("FromStream error = %v, want ErrInvalidRouter", err)
	}
}
