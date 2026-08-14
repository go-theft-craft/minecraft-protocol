package protocol

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
)

// stubPreFrameHook claims or declines on command and records what it saw.
type stubPreFrameHook struct {
	claim  bool
	err    error
	calls  int
	peeked []byte
}

func (h *stubPreFrameHook) HandlePreFrame(
	_ context.Context,
	reader *bufio.Reader,
	writer io.Writer,
) (bool, error) {
	h.calls++

	peeked, _ := reader.Peek(2)
	h.peeked = bytes.Clone(peeked)

	if !h.claim {
		return false, h.err
	}
	if _, err := reader.Discard(len(peeked)); err != nil {
		return true, err
	}
	if _, err := writer.Write([]byte{0xff}); err != nil {
		return true, err
	}

	return true, h.err
}

func TestRunPreFrameWithoutHook(t *testing.T) {
	t.Parallel()

	reader := bufio.NewReader(bytes.NewReader([]byte{0x01, 0x02}))
	claimed, err := runPreFrame(context.Background(), nil, reader, io.Discard)
	if err != nil {
		t.Fatalf("runPreFrame() error = %v", err)
	}
	if claimed {
		t.Fatal("runPreFrame() claimed = true without a hook")
	}
	if reader.Buffered() != 0 {
		t.Fatal("runPreFrame() read from the transport without a hook")
	}
}

func TestRunPreFrameClaimStopsFraming(t *testing.T) {
	t.Parallel()

	hook := &stubPreFrameHook{claim: true}
	reader := bufio.NewReader(bytes.NewReader([]byte{0xfe, 0x01, 0xaa}))
	var writer bytes.Buffer

	claimed, err := runPreFrame(context.Background(), hook, reader, &writer)
	if err != nil {
		t.Fatalf("runPreFrame() error = %v", err)
	}
	if !claimed {
		t.Fatal("runPreFrame() claimed = false, want true")
	}
	if writer.Len() == 0 {
		t.Fatal("a claiming hook wrote nothing")
	}

	// The framer must never see the claimed bytes.
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(remaining, []byte{0xaa}) {
		t.Fatalf("remaining bytes = %x, want %x", remaining, []byte{0xaa})
	}
}

func TestRunPreFrameDeclinePreservesEveryByte(t *testing.T) {
	t.Parallel()

	input := []byte{0x0f, 0x00, 0x2f, 0x09}
	hook := &stubPreFrameHook{}
	reader := bufio.NewReader(bytes.NewReader(input))
	var writer bytes.Buffer

	claimed, err := runPreFrame(context.Background(), hook, reader, &writer)
	if err != nil {
		t.Fatalf("runPreFrame() error = %v", err)
	}
	if claimed {
		t.Fatal("runPreFrame() claimed = true, want false")
	}
	if hook.calls != 1 {
		t.Fatalf("hook ran %d times, want 1", hook.calls)
	}
	if !bytes.Equal(hook.peeked, input[:2]) {
		t.Fatalf("hook peeked %x, want %x", hook.peeked, input[:2])
	}
	if writer.Len() != 0 {
		t.Fatalf("a declining hook wrote %d bytes", writer.Len())
	}

	// The same buffered reader hands every byte to the read pump.
	remaining, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(remaining, input) {
		t.Fatalf("remaining bytes = %x, want %x", remaining, input)
	}
}

func TestRunPreFrameReportsHookFailure(t *testing.T) {
	t.Parallel()

	sentinel := errors.New("hook failed")
	hook := &stubPreFrameHook{claim: true, err: sentinel}
	reader := bufio.NewReader(bytes.NewReader([]byte{0xfe, 0x01}))

	if _, err := runPreFrame(context.Background(), hook, reader, io.Discard); !errors.Is(err, sentinel) {
		t.Fatalf("runPreFrame() error = %v, want the hook error", err)
	}
}

func TestRunPreFrameHonoursCancelledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	hook := &stubPreFrameHook{claim: true}
	reader := bufio.NewReader(bytes.NewReader([]byte{0xfe, 0x01}))

	if _, err := runPreFrame(ctx, hook, reader, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatalf("runPreFrame() error = %v, want context.Canceled", err)
	}
	if hook.calls != 0 {
		t.Fatal("runPreFrame() ran the hook with a cancelled context")
	}
}
