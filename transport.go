package protocol

import (
	"errors"
	"fmt"
	"io"
)

// ErrInvalidTransport reports a transport that a managed stream cannot own.
var ErrInvalidTransport = errors.New("invalid protocol transport")

// Transport is the byte-level connection a managed stream owns.
//
// Interrupt must unblock a reader and a writer that are already blocked in the
// transport, in both directions, from any goroutine. Without it a stream
// cannot stop while a peer holds a connection open. For a net.Conn, Close
// satisfies this requirement.
type Transport struct {
	Reader    io.Reader
	Writer    io.Writer
	Interrupt func() error
}

func (t Transport) validate() error {
	switch {
	case t.Reader == nil:
		return fmt.Errorf("%w: nil reader", ErrInvalidTransport)
	case t.Writer == nil:
		return fmt.Errorf("%w: nil writer", ErrInvalidTransport)
	case t.Interrupt == nil:
		return fmt.Errorf("%w: nil interrupt function", ErrInvalidTransport)
	default:
		return nil
	}
}
