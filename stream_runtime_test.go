package protocol

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"
)

func runtimeLimits(t *testing.T, queueItems int) Limits {
	t.Helper()

	limits, err := NewLimits(
		MaxFrameBytes(4096),
		MaxDecompressedBytes(8192),
		MaxBufferedBytes(1<<20),
		MaxQueueItems(queueItems),
	)
	if err != nil {
		t.Fatal(err)
	}
	return limits
}

type runtimeHarness struct {
	stream  *Stream
	session *testSession
	reader  *scriptedReader
	writer  *syncWriter
}

func startRuntime(t *testing.T, queueItems int, options ...StreamOption) *runtimeHarness {
	t.Helper()

	session := newTestSession(t, runtimeLimits(t, queueItems))
	reader := newScriptedReader()
	writer := &syncWriter{}

	stream, err := NewStream(session, Transport{
		Reader:    reader,
		Writer:    writer,
		Interrupt: reader.interrupt,
	}, options...)
	if err != nil {
		t.Fatalf("NewStream() error = %v", err)
	}
	if err := stream.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	return &runtimeHarness{stream: stream, session: session, reader: reader, writer: writer}
}

func readWithTimeout(t *testing.T, stream *Stream) Packet {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	packet, err := stream.Read(ctx)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	return packet
}

func TestStreamReadPreservesFrameOrder(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	for id := byte(1); id <= 5; id++ {
		harness.reader.deliver(testFrameBytes(id, id*2))
	}

	for id := byte(1); id <= 5; id++ {
		packet := readWithTimeout(t, harness.stream)
		if packet.ID != int32(id) {
			t.Fatalf("Read() ID = %d, want %d", packet.ID, id)
		}
		if !bytes.Equal(packet.Payload, []byte{id * 2}) {
			t.Fatalf("Read() payload = %x, want %x", packet.Payload, []byte{id * 2})
		}
	}
}

func TestStreamReadOwnsItsPacket(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	harness.reader.deliver(testFrameBytes(7, 0xaa, 0xbb))

	packet := readWithTimeout(t, harness.stream)
	packet.Payload[0] = 0xff

	harness.reader.deliver(testFrameBytes(8, 0xcc))
	next := readWithTimeout(t, harness.stream)
	if next.ID != 8 || !bytes.Equal(next.Payload, []byte{0xcc}) {
		t.Fatalf("mutating a delivered packet disturbed the stream: %+v", next)
	}
}

func TestStreamReadCancellationKeepsPacketQueued(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := harness.stream.Read(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Read(cancelled) error = %v, want context.Canceled", err)
	}

	harness.reader.deliver(testFrameBytes(3, 0x01))

	// A cancelled Read must not have consumed anything, and the stream keeps
	// running for the next reader.
	packet := readWithTimeout(t, harness.stream)
	if packet.ID != 3 {
		t.Fatalf("Read() ID = %d, want 3", packet.ID)
	}
}

func TestStreamReadBeforeStart(t *testing.T) {
	t.Parallel()

	stream, _ := newTestStream(t)
	if _, err := stream.Read(context.Background()); !errors.Is(err, ErrStreamNotStarted) {
		t.Fatalf("Read() error = %v, want ErrStreamNotStarted", err)
	}
}

func TestStreamPeerEOFIsFatal(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	harness.reader.fail(io.EOF)

	if err := harness.stream.Wait(); !errors.Is(err, io.EOF) {
		t.Fatalf("Wait() error = %v, want io.EOF", err)
	}
	if _, err := harness.stream.Read(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("Read() after EOF error = %v, want the fatal cause", err)
	}
}

func TestStreamDrainsQueuedPacketsAfterTermination(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	harness.reader.deliver(testFrameBytes(4, 0x09))

	// Let the packet reach the queue before the peer disappears.
	packet := readWithTimeout(t, harness.stream)
	if packet.ID != 4 {
		t.Fatalf("Read() ID = %d, want 4", packet.ID)
	}

	harness.reader.deliver(testFrameBytes(5, 0x0a))
	second := readWithTimeout(t, harness.stream)
	if second.ID != 5 {
		t.Fatalf("Read() ID = %d, want 5", second.ID)
	}
}

func TestStreamMalformedDecodeIsFatal(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	sentinel := errors.New("bad packet")
	harness.session.setDecodeErr(sentinel)
	harness.reader.deliver(testFrameBytes(1, 0x01))

	err := harness.stream.Wait()
	if !errors.Is(err, ErrMalformedInbound) {
		t.Fatalf("Wait() error = %v, want ErrMalformedInbound", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("Wait() error = %v, want it to preserve the decode error", err)
	}
}

func TestStreamMalformedFrameIsFatal(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	// A zero-length frame is invalid for the test framer.
	harness.reader.deliver([]byte{0x00})

	if err := harness.stream.Wait(); err == nil {
		t.Fatal("Wait() error = nil, want a framing failure")
	}
}

func TestStreamTransportReadFailureIsFatal(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	sentinel := errors.New("connection reset")
	harness.reader.fail(sentinel)

	if err := harness.stream.Wait(); !errors.Is(err, sentinel) {
		t.Fatalf("Wait() error = %v, want the transport error", err)
	}
}

func TestStreamWriteSendsCompleteFrame(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	packet := Packet{
		State:     harness.session.State(),
		Direction: harness.session.Outbound(),
		ID:        0x11,
		Payload:   []byte{0xaa, 0xbb},
	}
	if err := harness.stream.Write(context.Background(), packet); err != nil {
		t.Fatalf("Write() error = %v", err)
	}

	want := testFrameBytes(0x11, 0xaa, 0xbb)
	if got := harness.writer.bytesWritten(); !bytes.Equal(got, want) {
		t.Fatalf("wrote %x, want %x", got, want)
	}
}

func TestStreamWriteOrderMatchesCallOrder(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	var want []byte
	for id := byte(1); id <= 6; id++ {
		packet := Packet{
			State:     harness.session.State(),
			Direction: harness.session.Outbound(),
			ID:        int32(id),
			Payload:   []byte{id},
		}
		if err := harness.stream.Write(context.Background(), packet); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		want = append(want, testFrameBytes(id, id)...)
	}

	if got := harness.writer.bytesWritten(); !bytes.Equal(got, want) {
		t.Fatalf("wrote %x, want %x", got, want)
	}
}

func TestStreamConcurrentWritesProduceWholeFrames(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	const writers = 12
	var group sync.WaitGroup
	group.Add(writers)
	for id := range writers {
		go func() {
			defer group.Done()

			packet := Packet{
				State:     harness.session.State(),
				Direction: harness.session.Outbound(),
				ID:        int32(id + 1),
				Payload:   []byte{byte(id + 1), byte(id + 1)},
			}
			if err := harness.stream.Write(context.Background(), packet); err != nil {
				t.Errorf("Write() error = %v", err)
			}
		}()
	}
	group.Wait()

	// Every frame must appear exactly once and intact, whatever order the
	// coordinator accepted them in.
	written := harness.writer.bytesWritten()
	seen := map[byte]int{}
	for offset := 0; offset < len(written); {
		length := int(written[offset])
		if offset+1+length > len(written) {
			t.Fatalf("frame at %d is truncated: %x", offset, written[offset:])
		}
		body := written[offset+1 : offset+1+length]
		if len(body) != 3 || body[1] != body[0] || body[2] != body[0] {
			t.Fatalf("frame at %d is interleaved: %x", offset, body)
		}
		seen[body[0]]++
		offset += 1 + length
	}
	if len(seen) != writers {
		t.Fatalf("saw %d distinct frames, want %d", len(seen), writers)
	}
	for id, count := range seen {
		if count != 1 {
			t.Fatalf("frame %d was written %d times", id, count)
		}
	}
}

func TestStreamWriteReturnsOnlyAfterTheCompleteWrite(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, runtimeLimits(t, 8))
	reader := newScriptedReader()
	writer := newBlockingWriter()

	stream, err := NewStream(session, Transport{
		Reader: reader,
		Writer: writer,
		Interrupt: func() error {
			_ = reader.interrupt()
			return writer.interrupt()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	done := make(chan error, 1)
	go func() {
		done <- stream.Write(context.Background(), Packet{
			State:     session.State(),
			Direction: session.Outbound(),
			ID:        0x22,
			Payload:   []byte{0x01},
		})
	}()

	<-writer.entered
	select {
	case err := <-done:
		t.Fatalf("Write() returned %v before the transport accepted the frame", err)
	default:
	}

	writer.release <- nil
	if err := <-done; err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if got, want := writer.bytesWritten(), testFrameBytes(0x22, 0x01); !bytes.Equal(got, want) {
		t.Fatalf("wrote %x, want %x", got, want)
	}
}

func TestStreamWriteRejectsInvalidPacketWithoutStoppingTheStream(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	sentinel := errors.New("unsupported packet")
	harness.session.setEncodeErr(sentinel)

	err := harness.stream.Write(context.Background(), Packet{ID: 1})
	if !errors.Is(err, sentinel) {
		t.Fatalf("Write() error = %v, want the encode error", err)
	}
	if harness.writer.writeCount() != 0 {
		t.Fatalf("an unencodable packet reached the transport %d times", harness.writer.writeCount())
	}

	// The stream keeps running: a later valid packet still goes out.
	harness.session.setEncodeErr(nil)
	if err := harness.stream.Write(context.Background(), Packet{ID: 2, Payload: []byte{0x03}}); err != nil {
		t.Fatalf("Write() after a rejected packet error = %v", err)
	}
	if got, want := harness.writer.bytesWritten(), testFrameBytes(2, 0x03); !bytes.Equal(got, want) {
		t.Fatalf("wrote %x, want %x", got, want)
	}
}

func TestStreamWriteCancellationBeforeDequeueSendsNothing(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, runtimeLimits(t, 8))
	reader := newScriptedReader()
	writer := newBlockingWriter()

	stream, err := NewStream(session, Transport{
		Reader: reader,
		Writer: writer,
		Interrupt: func() error {
			_ = reader.interrupt()
			return writer.interrupt()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	// Occupy the coordinator with a write that is stuck in the transport.
	blocked := make(chan error, 1)
	go func() {
		blocked <- stream.Write(context.Background(), Packet{ID: 1, Payload: []byte{0x01}})
	}()
	<-writer.entered

	// A second write cannot be accepted while the first is in flight, so
	// cancelling it guarantees nothing was sent.
	ctx, cancel := context.WithCancel(context.Background())
	second := make(chan error, 1)
	go func() {
		second <- stream.Write(ctx, Packet{ID: 2, Payload: []byte{0x02}})
	}()

	cancel()
	if err := <-second; !errors.Is(err, context.Canceled) {
		t.Fatalf("Write() error = %v, want context.Canceled", err)
	}

	writer.release <- nil
	if err := <-blocked; err != nil {
		t.Fatalf("first Write() error = %v", err)
	}

	if got, want := writer.bytesWritten(), testFrameBytes(1, 0x01); !bytes.Equal(got, want) {
		t.Fatalf("wrote %x, want only the first frame %x", got, want)
	}

	// The stream survived the cancellation: only the deliberate close ends it.
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := stream.Wait(); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("Wait() error = %v, want ErrStreamClosed", err)
	}
}

func TestStreamWriteCancellationDuringTransportWriteAbortsTheStream(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, runtimeLimits(t, 8))
	reader := newScriptedReader()
	writer := newBlockingWriter()

	stream, err := NewStream(session, Transport{
		Reader: reader,
		Writer: writer,
		Interrupt: func() error {
			_ = reader.interrupt()
			return writer.interrupt()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- stream.Write(ctx, Packet{ID: 1, Payload: []byte{0x01}})
	}()

	<-writer.entered
	cancel()

	err = <-done
	if !errors.Is(err, ErrAmbiguousWrite) {
		t.Fatalf("Write() error = %v, want ErrAmbiguousWrite", err)
	}
	if waitErr := stream.Wait(); !errors.Is(waitErr, ErrAmbiguousWrite) {
		t.Fatalf("Wait() error = %v, want the ambiguous write to be the cause", waitErr)
	}
}

func TestStreamPartialWriteIsFatal(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, runtimeLimits(t, 8))
	reader := newScriptedReader()
	sentinel := errors.New("transport died mid-frame")
	writer := &truncatingWriter{limit: 2, err: sentinel}

	stream, err := NewStream(session, Transport{
		Reader:    reader,
		Writer:    writer,
		Interrupt: reader.interrupt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	writeErr := stream.Write(context.Background(), Packet{ID: 1, Payload: []byte{0x01, 0x02, 0x03}})
	if !errors.Is(writeErr, sentinel) {
		t.Fatalf("Write() error = %v, want the transport error", writeErr)
	}
	if waitErr := stream.Wait(); !errors.Is(waitErr, sentinel) {
		t.Fatalf("Wait() error = %v, want the transport error", waitErr)
	}
}

func TestStreamFirstCauseIsStable(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	sentinel := errors.New("first failure")
	harness.reader.fail(sentinel)

	if err := harness.stream.Wait(); !errors.Is(err, sentinel) {
		t.Fatalf("Wait() error = %v, want the first failure", err)
	}

	// Later failures and closes must not replace it.
	_ = harness.stream.Close()
	for range 3 {
		if err := harness.stream.Wait(); !errors.Is(err, sentinel) {
			t.Fatalf("Wait() error = %v, want the first failure to stay stable", err)
		}
	}
	if err := harness.stream.Write(context.Background(), Packet{ID: 1}); !errors.Is(err, sentinel) {
		t.Fatalf("Write() after termination error = %v, want the first failure", err)
	}
}

func TestStreamAcceptedWriteCompletesWithAFullInboundQueue(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, runtimeLimits(t, 2))
	reader := newScriptedReader()
	writer := newBlockingWriter()

	stream, err := NewStream(session, Transport{
		Reader: reader,
		Writer: writer,
		Interrupt: func() error {
			_ = reader.interrupt()
			return writer.interrupt()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	// The write is accepted and reaches the transport first.
	accepted := make(chan error, 1)
	go func() {
		accepted <- stream.Write(context.Background(), Packet{ID: 0x30, Payload: []byte{0x01}})
	}()
	<-writer.entered

	// Now saturate the inbound side. Nothing reads it.
	for id := byte(1); id <= 4; id++ {
		reader.deliver(testFrameBytes(id, id))
	}

	// The accepted write still finishes.
	writer.release <- nil
	if err := <-accepted; err != nil {
		t.Fatalf("accepted Write() error = %v with a saturated inbound queue", err)
	}
	if got, want := writer.bytesWritten(), testFrameBytes(0x30, 0x01); !bytes.Equal(got, want) {
		t.Fatalf("wrote %x, want %x", got, want)
	}

	// Draining the inbound queue frees the shared budget again, in order.
	for id := byte(1); id <= 4; id++ {
		packet := readWithTimeout(t, stream)
		if packet.ID != int32(id) {
			t.Fatalf("Read() ID = %d, want %d", packet.ID, id)
		}
	}

	writer.release <- nil
	if err := stream.Write(context.Background(), Packet{ID: 0x31, Payload: []byte{0x02}}); err != nil {
		t.Fatalf("Write() after draining error = %v", err)
	}
}

func TestStreamUnreadInboundPacketsApplyBackpressureToWrites(t *testing.T) {
	t.Parallel()

	// A single shared item budget means unread inbound packets eventually stop
	// new writes. That is deliberate backpressure, not a stall: reading frees
	// the capacity again.
	harness := startRuntime(t, 1)
	harness.reader.deliver(testFrameBytes(1, 0x01))

	// Let the inbound packet take the only queue item.
	packet := readWithTimeout(t, harness.stream)
	if packet.ID != 1 {
		t.Fatalf("Read() ID = %d, want 1", packet.ID)
	}

	if err := harness.stream.Write(context.Background(), Packet{ID: 2, Payload: []byte{0x02}}); err != nil {
		t.Fatalf("Write() error = %v once the inbound packet was read", err)
	}
}

func TestStreamWriteAfterCloseIsRejected(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	if err := harness.stream.Close(); err != nil {
		t.Fatal(err)
	}

	if err := harness.stream.Write(context.Background(), Packet{ID: 1}); !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("Write() error = %v, want ErrStreamClosed", err)
	}
}

func TestStreamWriteBeforeStart(t *testing.T) {
	t.Parallel()

	stream, _ := newTestStream(t)
	if err := stream.Write(context.Background(), Packet{ID: 1}); !errors.Is(err, ErrStreamNotStarted) {
		t.Fatalf("Write() error = %v, want ErrStreamNotStarted", err)
	}
}

func TestStreamPreFrameHookClaimEndsTheStreamCleanly(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, runtimeLimits(t, 8))
	reader := newScriptedReader()
	writer := &syncWriter{}

	hook := &stubPreFrameHook{claim: true}
	stream, err := NewStream(session, Transport{
		Reader:    reader,
		Writer:    writer,
		Interrupt: reader.interrupt,
	}, WithPreFrameHook(hook))
	if err != nil {
		t.Fatal(err)
	}

	reader.deliver([]byte{0xfe, 0x01})
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := stream.Wait(); err != nil {
		t.Fatalf("Wait() error = %v, want a clean end after the hook claimed", err)
	}
	if hook.calls != 1 {
		t.Fatalf("hook ran %d times, want 1", hook.calls)
	}
}

func TestStreamPreFrameHookDeclineLeavesFramingIntact(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, runtimeLimits(t, 8))
	reader := newScriptedReader()
	writer := &syncWriter{}

	hook := &stubPreFrameHook{}
	stream, err := NewStream(session, Transport{
		Reader:    reader,
		Writer:    writer,
		Interrupt: reader.interrupt,
	}, WithPreFrameHook(hook))
	if err != nil {
		t.Fatal(err)
	}

	reader.deliver(testFrameBytes(9, 0x05))
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	packet := readWithTimeout(t, stream)
	if packet.ID != 9 || !bytes.Equal(packet.Payload, []byte{0x05}) {
		t.Fatalf("Read() = %+v, want the declined bytes to reach the framer", packet)
	}
}

func TestStreamPreFrameHookFailureIsFatal(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, runtimeLimits(t, 8))
	reader := newScriptedReader()

	sentinel := errors.New("hook failed")
	hook := &stubPreFrameHook{claim: true, err: sentinel}

	stream, err := NewStream(session, Transport{
		Reader:    reader,
		Writer:    &syncWriter{},
		Interrupt: reader.interrupt,
	}, WithPreFrameHook(hook))
	if err != nil {
		t.Fatal(err)
	}

	reader.deliver([]byte{0xfe, 0x01})
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := stream.Wait(); !errors.Is(err, sentinel) {
		t.Fatalf("Wait() error = %v, want the hook error", err)
	}
}
