package protocol

import (
	"bufio"
	"context"
	"io"
)

// PreFrameHook inspects a connection once, before normal framing begins.
//
// A hook reports true when it has claimed the connection and owns the raw
// exchange from that point on. It reports false to decline, and declining must
// leave every inspected byte buffered for the framer: use bufio.Reader.Peek
// rather than Read until the hook is certain it will claim the connection.
type PreFrameHook interface {
	HandlePreFrame(context.Context, *bufio.Reader, io.Writer) (bool, error)
}

// runPreFrame runs an optional hook before the read pump starts framing. It
// reports whether the hook claimed the connection.
func runPreFrame(
	ctx context.Context,
	hook PreFrameHook,
	reader *bufio.Reader,
	writer io.Writer,
) (bool, error) {
	if hook == nil {
		return false, nil
	}
	if err := ctx.Err(); err != nil {
		return false, err
	}

	return hook.HandlePreFrame(ctx, reader, writer)
}
