// Package router dispatches decoded packets to handlers registered by name or
// by ID.
//
// It is defined over a one-method Receiver and never mentions the stream, so a
// test drives it over a slice and a caller drives it over a connection. The
// one file that knows about a stream is adapter.go, and it exists only to
// build a Receiver and a Sender from one.
//
// A handler runs on the goroutine that called Dispatch or Run. Handler panics
// are not recovered: a panic is a bug in the handler, and turning it into a
// stream shutdown puts the report a long way from the cause.
package router

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/middleware"
)

var (
	// ErrInvalidRouter reports a router that cannot be built or registered on.
	ErrInvalidRouter = errors.New("invalid router")
	// ErrUnknownPacket reports a name the protocol does not define in the
	// given state and direction. It is returned at registration, where the
	// mistake is, rather than at dispatch, where it would look like silence.
	ErrUnknownPacket = errors.New("unknown packet name")
	// ErrRunning reports registration attempted after Run started. The table
	// is read without a lock on the dispatch path, which is only safe because
	// it stops changing first.
	ErrRunning = errors.New("router is already running")
	// ErrHandler wraps whatever a handler returned, so a caller can tell a
	// handler failure from a receiver failure without comparing types.
	ErrHandler = errors.New("packet handler failed")
)

// Receiver hands out decoded packets in arrival order.
type Receiver interface {
	Receive(context.Context) (protocol.Packet, error)
}

// packetKey identifies one dispatch table entry.
type packetKey struct {
	State     protocol.State
	Direction protocol.Direction
	ID        int32
}

// Router holds the dispatch table for one connection.
//
// It is not safe for concurrent registration. Registration happens before Run
// and is refused afterwards, which is what lets dispatch read the table
// without a lock.
type Router struct {
	descriptor protocol.Protocol
	names      protocol.PacketDescriptor
	middleware []middleware.ReceiveMiddleware

	handlers map[packetKey][]middleware.Handler
	fallback middleware.Handler
	// chain is dispatch wrapped in the configured middleware, built once at
	// construction. Building it per packet would allocate on the hot path of
	// every connection the router serves.
	chain   middleware.Handler
	running atomic.Bool
}

// Option configures a router at construction.
type Option func(*Router) error

// WithReceiveMiddleware wraps every dispatch, registered or not.
//
// It describes the dispatch rather than the handler, which is why an
// unregistered packet still passes through it: a middleware that counts or
// logs packets would otherwise report a number that silently excludes
// everything nobody registered for.
func WithReceiveMiddleware(middlewares ...middleware.ReceiveMiddleware) Option {
	return func(r *Router) error {
		for index, wrap := range middlewares {
			if wrap == nil {
				return fmt.Errorf("%w: receive middleware %d is nil", ErrInvalidRouter, index)
			}
		}
		r.middleware = append(r.middleware, middlewares...)

		return nil
	}
}

// New returns a router for one protocol.
//
// The protocol is used to resolve names at registration. One that does not
// implement protocol.PacketDescriptor is still usable through HandleID.
func New(descriptor protocol.Protocol, options ...Option) (*Router, error) {
	if descriptor == nil {
		return nil, fmt.Errorf("%w: nil protocol", ErrInvalidRouter)
	}

	made := &Router{
		descriptor: descriptor,
		handlers:   make(map[packetKey][]middleware.Handler),
	}
	names, ok := descriptor.(protocol.PacketDescriptor)
	if ok {
		made.names = names
	}

	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidRouter)
		}
		if err := option(made); err != nil {
			return nil, err
		}
	}

	chain, err := middleware.ChainReceive(middleware.HandlerFunc(made.dispatch), made.middleware...)
	if err != nil {
		return nil, err
	}
	made.chain = chain

	return made, nil
}

// Handle registers a handler for one named packet.
//
// Handlers on one key run in registration order, and the first error stops the
// rest: a handler that failed may have left the ones after it with nothing
// coherent to read.
func (r *Router) Handle(
	state protocol.State,
	direction protocol.Direction,
	name string,
	handler middleware.Handler,
) error {
	if r.names == nil {
		return fmt.Errorf(
			"%w: protocol %q cannot resolve packet names; register by ID",
			ErrInvalidRouter,
			r.descriptor.ID(),
		)
	}

	id, known := r.names.PacketID(state, direction, name)
	if !known {
		return fmt.Errorf(
			"%w: protocol %q has no packet %q in state %q direction %d",
			ErrUnknownPacket,
			r.descriptor.ID(),
			name,
			state,
			direction,
		)
	}

	return r.HandleID(state, direction, id, handler)
}

// HandleID registers a handler for one packet ID.
func (r *Router) HandleID(
	state protocol.State,
	direction protocol.Direction,
	id int32,
	handler middleware.Handler,
) error {
	if handler == nil {
		return fmt.Errorf("%w: nil handler", ErrInvalidRouter)
	}
	if r.running.Load() {
		return ErrRunning
	}

	key := packetKey{State: state, Direction: direction, ID: id}
	r.handlers[key] = append(r.handlers[key], handler)

	return nil
}

// Fallback registers the handler for packets no other handler claimed.
func (r *Router) Fallback(handler middleware.Handler) error {
	if handler == nil {
		return fmt.Errorf("%w: nil fallback handler", ErrInvalidRouter)
	}
	if r.running.Load() {
		return ErrRunning
	}
	r.fallback = handler

	return nil
}

// Dispatch runs the handlers registered for one packet.
//
// An unregistered packet with no fallback is skipped and reports no error:
// a connection carries packets a consumer did not ask for, and treating that
// as a failure would make every router have to register for everything.
func (r *Router) Dispatch(ctx context.Context, packet protocol.Packet) error {
	return r.chain.Handle(ctx, packet)
}

// dispatch is Dispatch without the middleware chain, and is the base of it.
func (r *Router) dispatch(ctx context.Context, packet protocol.Packet) error {
	key := packetKey{State: packet.State, Direction: packet.Direction, ID: packet.ID}

	handlers, registered := r.handlers[key]
	if !registered {
		if r.fallback == nil {
			return nil
		}

		return r.wrap(r.fallback.Handle(ctx, packet), packet)
	}

	for _, handler := range handlers {
		if err := handler.Handle(ctx, packet); err != nil {
			return r.wrap(err, packet)
		}
	}

	return nil
}

// wrap names the packet a handler failed on, because a bare handler error says
// nothing about which of a connection's packets produced it.
func (r *Router) wrap(err error, packet protocol.Packet) error {
	if err == nil {
		return nil
	}

	name := packet.Name
	if name == "" && r.names != nil {
		if resolved, ok := r.names.PacketName(packet.State, packet.Direction, packet.ID); ok {
			name = resolved
		}
	}
	if name == "" {
		name = fmt.Sprintf("%#x", packet.ID)
	}

	return fmt.Errorf("%w: %s/%s: %w", ErrHandler, packet.State, name, err)
}

// Run reads packets until the receiver ends, dispatching each one.
//
// It returns nil on a clean end of stream, the context's error on
// cancellation, and otherwise whatever failed. Registration is refused from
// the moment it starts.
func (r *Router) Run(ctx context.Context, receiver Receiver) error {
	if receiver == nil {
		return fmt.Errorf("%w: nil receiver", ErrInvalidRouter)
	}
	r.running.Store(true)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		packet, err := receiver.Receive(ctx)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}

			return err
		}

		if err := r.chain.Handle(ctx, packet); err != nil {
			return err
		}
	}
}
