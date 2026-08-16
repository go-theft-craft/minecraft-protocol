// Package middleware composes ordered wrappers around packet sending and
// packet handling.
//
// It is defined over two one-method interfaces and never mentions the stream.
// That is deliberate: routing and middleware sit above framing, and a package
// that named *protocol.Stream could not be tested — or reused — without one.
// A test drives a chain over a slice; a caller drives it over a stream.
//
// # Payload ownership
//
// A middleware receives a packet whose Payload it may read and may modify in
// place, and must not retain past the call. The chain clones the payload once
// on entry, so an edit reaches everything further down the chain and never
// reaches the caller's own slice. A chain with no middleware in it does not
// clone, and does not wrap: it hands back the base unchanged.
package middleware

import (
	"bytes"
	"context"
	"errors"
	"fmt"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

// ErrInvalidChain reports a chain that cannot be built. Every such fault is
// reported here, at construction, rather than as a nil dereference on the
// first packet — which would surface far from the code that caused it.
var ErrInvalidChain = errors.New("invalid middleware chain")

// Sender writes one packet.
type Sender interface {
	Send(context.Context, protocol.Packet) error
}

// Handler processes one packet.
type Handler interface {
	Handle(context.Context, protocol.Packet) error
}

// SenderFunc adapts a function to Sender.
type SenderFunc func(context.Context, protocol.Packet) error

// Send implements Sender.
func (f SenderFunc) Send(ctx context.Context, packet protocol.Packet) error { return f(ctx, packet) }

// HandlerFunc adapts a function to Handler.
type HandlerFunc func(context.Context, protocol.Packet) error

// Handle implements Handler.
func (f HandlerFunc) Handle(ctx context.Context, packet protocol.Packet) error {
	return f(ctx, packet)
}

// SendMiddleware wraps a sender in another sender.
type SendMiddleware func(Sender) Sender

// ReceiveMiddleware wraps a handler in another handler.
type ReceiveMiddleware func(Handler) Handler

// ChainSend wraps base so that the first middleware given is the outermost.
//
// Declaration order is the order a reader expects: the first one listed sees
// the packet first and sees the others' effects on the way back out. The fold
// therefore runs right to left.
func ChainSend(base Sender, middlewares ...SendMiddleware) (Sender, error) {
	if base == nil {
		return nil, fmt.Errorf("%w: nil base sender", ErrInvalidChain)
	}
	if len(middlewares) == 0 {
		return base, nil
	}

	chain := base
	for index := len(middlewares) - 1; index >= 0; index-- {
		wrap := middlewares[index]
		if wrap == nil {
			return nil, fmt.Errorf("%w: middleware %d is nil", ErrInvalidChain, index)
		}
		next := wrap(chain)
		if next == nil {
			return nil, fmt.Errorf("%w: middleware %d returned no sender", ErrInvalidChain, index)
		}
		chain = next
	}

	return owningSender{next: chain}, nil
}

// ChainReceive wraps base so that the first middleware given is the outermost.
func ChainReceive(base Handler, middlewares ...ReceiveMiddleware) (Handler, error) {
	if base == nil {
		return nil, fmt.Errorf("%w: nil base handler", ErrInvalidChain)
	}
	if len(middlewares) == 0 {
		return base, nil
	}

	chain := base
	for index := len(middlewares) - 1; index >= 0; index-- {
		wrap := middlewares[index]
		if wrap == nil {
			return nil, fmt.Errorf("%w: middleware %d is nil", ErrInvalidChain, index)
		}
		next := wrap(chain)
		if next == nil {
			return nil, fmt.Errorf("%w: middleware %d returned no handler", ErrInvalidChain, index)
		}
		chain = next
	}

	return owningHandler{next: chain}, nil
}

// owningSender gives the chain its own payload before the first middleware
// sees it, so the ownership rule is a property rather than a promise.
type owningSender struct {
	next Sender
}

// Send implements Sender.
func (s owningSender) Send(ctx context.Context, packet protocol.Packet) error {
	packet.Payload = bytes.Clone(packet.Payload)

	return s.next.Send(ctx, packet)
}

// owningHandler is the receive half of owningSender.
type owningHandler struct {
	next Handler
}

// Handle implements Handler.
func (h owningHandler) Handle(ctx context.Context, packet protocol.Packet) error {
	packet.Payload = bytes.Clone(packet.Payload)

	return h.next.Handle(ctx, packet)
}
