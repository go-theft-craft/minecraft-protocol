package router

import (
	"context"
	"fmt"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/middleware"
)

// StreamAdapter presents a stream as the Receiver a router reads from and the
// Sender a middleware chain writes to.
//
// It is the only thing in this package that names a stream. Everything else is
// written against the two one-method interfaces, which is what lets the router
// be tested over a slice and reused over anything else that carries packets.
type StreamAdapter struct {
	stream *protocol.Stream
}

// FromStream adapts one started stream.
func FromStream(stream *protocol.Stream) (*StreamAdapter, error) {
	if stream == nil {
		return nil, fmt.Errorf("%w: nil stream", ErrInvalidRouter)
	}

	return &StreamAdapter{stream: stream}, nil
}

// Receive implements Receiver.
func (a *StreamAdapter) Receive(ctx context.Context) (protocol.Packet, error) {
	return a.stream.Read(ctx)
}

// Send implements middleware.Sender.
func (a *StreamAdapter) Send(ctx context.Context, packet protocol.Packet) error {
	return a.stream.Write(ctx, packet)
}

var (
	_ Receiver          = (*StreamAdapter)(nil)
	_ middleware.Sender = (*StreamAdapter)(nil)
)
