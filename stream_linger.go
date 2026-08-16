package protocol

import (
	"context"
	"time"
)

// LingerTimeout bounds how long a graceful shutdown waits for the peer to
// finish reading the disconnect before the connection is closed anyway.
//
// It is short because it is only ever waiting for a peer to notice a FIN and
// hang up, which is one round trip on a healthy connection. A peer that is
// gone or wedged costs this much and no more.
const LingerTimeout = time.Second

// halfCloser is a transport whose write side can be closed on its own.
// *net.TCPConn is one; a pipe or an in-memory transport is not.
type halfCloser interface {
	CloseWrite() error
}

// linger ends a graceful shutdown without discarding what it just said.
//
// Closing a TCP socket that still holds unread bytes in its receive buffer
// makes the kernel send RST rather than FIN, and an RST tells the peer's
// kernel to discard everything it has buffered — including the disconnect
// packet written a moment earlier. The peer then reports a lost connection
// instead of the reason it was given, which is the exact failure the polite
// disconnect exists to prevent.
//
// The fix is the standard lingering close: shut down the write side so the
// peer sees a clean end of stream, keep reading until the peer closes in
// turn, and only then close the socket. By that point there is nothing unread
// left to turn the close into a reset.
//
// It does nothing for a transport that cannot half-close, and nothing at all
// unless a disconnect was actually sent: an abortive close has nothing to
// protect.
func (s *Stream) linger(ctx context.Context) {
	if !s.graceful.Load() {
		return
	}

	writer, ok := s.transport.Writer.(halfCloser)
	if !ok {
		return
	}

	s.lingering.Store(true)
	if err := writer.CloseWrite(); err != nil {
		return
	}

	timer := time.NewTimer(LingerTimeout)
	defer timer.Stop()

	select {
	case <-s.readDone:
		// The peer closed, so the disconnect was read or will never be.
	case <-timer.C:
	case <-ctx.Done():
	}
}
