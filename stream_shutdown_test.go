package protocol

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"
)

func disconnectPacket() *Packet {
	return &Packet{ID: 0x40, Name: "disconnect", Payload: []byte{}}
}

func TestStreamShutdownWithoutDisconnectPacket(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	if err := harness.stream.Shutdown(context.Background(), "bye"); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := harness.stream.Wait(); err != nil {
		t.Fatalf("Wait() error = %v, want a clean shutdown", err)
	}
	if harness.writer.writeCount() != 0 {
		t.Fatalf("a session with no disconnect packet wrote %d times", harness.writer.writeCount())
	}
}

func TestStreamShutdownSendsDisconnectAsTheFinalFrame(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	harness.session.setDisconnect(disconnectPacket(), nil)

	if err := harness.stream.Write(context.Background(), Packet{ID: 1, Payload: []byte{0x01}}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := harness.stream.Shutdown(context.Background(), "bye"); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	want := append(
		testFrameBytes(1, 0x01),
		testFrameBytes(0x40, 'b', 'y', 'e')...,
	)
	if got := harness.writer.bytesWritten(); !bytes.Equal(got, want) {
		t.Fatalf("wrote %x, want the disconnect last: %x", got, want)
	}
	if err := harness.stream.Wait(); err != nil {
		t.Fatalf("Wait() error = %v, want a clean shutdown", err)
	}
}

func TestStreamShutdownStopsAcceptingWrites(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	harness.session.setDisconnect(disconnectPacket(), nil)

	if err := harness.stream.Shutdown(context.Background(), "bye"); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	err := harness.stream.Write(context.Background(), Packet{ID: 2, Payload: []byte{0x02}})
	if err == nil {
		t.Fatal("Write() after Shutdown error = nil, want a rejection")
	}
	if !errors.Is(err, ErrStreamClosing) && !errors.Is(err, ErrStreamClosed) {
		t.Fatalf("Write() error = %v, want ErrStreamClosing or ErrStreamClosed", err)
	}

	// Only the disconnect reached the wire.
	if got, want := harness.writer.bytesWritten(), testFrameBytes(0x40, 'b', 'y', 'e'); !bytes.Equal(got, want) {
		t.Fatalf("wrote %x, want %x", got, want)
	}
}

func TestStreamShutdownDrainsTheWriteInFlight(t *testing.T) {
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

	session.setDisconnect(disconnectPacket(), nil)

	accepted := make(chan error, 1)
	go func() {
		accepted <- stream.Write(context.Background(), Packet{ID: 1, Payload: []byte{0x01}})
	}()
	<-writer.entered

	shutdownDone := make(chan error, 1)
	go func() {
		shutdownDone <- stream.Shutdown(context.Background(), "bye")
	}()

	// Release the in-flight write, then the disconnect.
	writer.release <- nil
	if err := <-accepted; err != nil {
		t.Fatalf("the accepted Write() error = %v, want it drained", err)
	}
	writer.release <- nil

	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	want := append(
		testFrameBytes(1, 0x01),
		testFrameBytes(0x40, 'b', 'y', 'e')...,
	)
	if got := writer.bytesWritten(); !bytes.Equal(got, want) {
		t.Fatalf("wrote %x, want %x", got, want)
	}
}

func TestStreamShutdownInterruptsExactlyOnce(t *testing.T) {
	t.Parallel()

	transport := &countingTransport{}
	stream, err := NewStream(newTestSession(t, testStreamLimits(t)), transport.transport())
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Start(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := stream.Shutdown(context.Background(), "bye"); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := stream.Shutdown(context.Background(), "again"); err != nil {
		t.Fatalf("repeated Shutdown() error = %v", err)
	}
	if err := stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if transport.interrupts.Load() != 1 {
		t.Fatalf("interrupted %d times, want 1", transport.interrupts.Load())
	}
	if err := stream.Wait(); err != nil {
		t.Fatalf("Wait() error = %v, want the graceful result to survive Close", err)
	}
}

func TestStreamShutdownDrainsTheInboundQueue(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	harness.reader.deliver(testFrameBytes(5, 0x05))

	// Let the packet reach the queue.
	packet := readWithTimeout(t, harness.stream)
	if packet.ID != 5 {
		t.Fatalf("Read() ID = %d, want 5", packet.ID)
	}

	harness.reader.deliver(testFrameBytes(6, 0x06))
	queued := readWithTimeout(t, harness.stream)
	if queued.ID != 6 {
		t.Fatalf("Read() ID = %d, want 6", queued.ID)
	}

	if err := harness.stream.Shutdown(context.Background(), "bye"); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if _, err := harness.stream.Read(context.Background()); err == nil {
		t.Fatal("Read() after a drained shutdown error = nil, want the end of the stream")
	}
}

func TestStreamShutdownReportsDisconnectEncodingFailure(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	harness.session.setDisconnect(disconnectPacket(), nil)

	sentinel := errors.New("cannot encode the disconnect")
	harness.session.setEncodeErr(sentinel)

	if err := harness.stream.Shutdown(context.Background(), "bye"); !errors.Is(err, sentinel) {
		t.Fatalf("Shutdown() error = %v, want the encode error", err)
	}
	if err := harness.stream.Wait(); !errors.Is(err, sentinel) {
		t.Fatalf("Wait() error = %v, want the encode error", err)
	}
}

func TestStreamShutdownReportsDisconnectConstructionFailure(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	sentinel := errors.New("no disconnect available")
	harness.session.setDisconnect(nil, sentinel)

	if err := harness.stream.Shutdown(context.Background(), "bye"); !errors.Is(err, sentinel) {
		t.Fatalf("Shutdown() error = %v, want the construction error", err)
	}
}

func TestStreamShutdownReportsDisconnectWriteFailure(t *testing.T) {
	t.Parallel()

	session := newTestSession(t, runtimeLimits(t, 8))
	reader := newScriptedReader()
	sentinel := errors.New("transport died")
	writer := &truncatingWriter{limit: 1, err: sentinel}

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

	session.setDisconnect(disconnectPacket(), nil)

	if err := stream.Shutdown(context.Background(), "bye"); !errors.Is(err, sentinel) {
		t.Fatalf("Shutdown() error = %v, want the transport error", err)
	}
	if err := stream.Wait(); !errors.Is(err, sentinel) {
		t.Fatalf("Wait() error = %v, want the transport error", err)
	}
}

func TestStreamShutdownTimeoutBecomesAbortiveClose(t *testing.T) {
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

	session.setDisconnect(disconnectPacket(), nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// The disconnect write never completes, so the shutdown context expires.
	err = stream.Shutdown(ctx, "bye")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown() error = %v, want context.DeadlineExceeded", err)
	}
	if waitErr := stream.Wait(); !errors.Is(waitErr, context.DeadlineExceeded) {
		t.Fatalf("Wait() error = %v, want the expired shutdown as the cause", waitErr)
	}
}

func TestStreamShutdownAfterPeerFailure(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	harness.session.setDisconnect(disconnectPacket(), nil)

	sentinel := errors.New("peer vanished")
	harness.reader.fail(sentinel)

	// Wait until the read failure has terminated the stream.
	if err := harness.stream.Wait(); !errors.Is(err, sentinel) {
		t.Fatalf("Wait() error = %v, want the peer failure", err)
	}
	if err := harness.stream.Shutdown(context.Background(), "bye"); !errors.Is(err, sentinel) {
		t.Fatalf("Shutdown() error = %v, want the first cause", err)
	}
}

func TestStreamShutdownBeforeStart(t *testing.T) {
	t.Parallel()

	stream, _ := newTestStream(t)
	if err := stream.Shutdown(context.Background(), "bye"); !errors.Is(err, ErrStreamNotStarted) {
		t.Fatalf("Shutdown() error = %v, want ErrStreamNotStarted", err)
	}
}

func TestStreamShutdownObservesTheDisconnect(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	harness := startRuntime(t, 8, WithObservationSink(sink))
	harness.session.setDisconnect(disconnectPacket(), nil)

	if err := harness.stream.Shutdown(context.Background(), "bye"); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}

	records := sink.all()
	if len(records) != 2 {
		t.Fatalf("recorded %d observations, want the disconnect frame and packet", len(records))
	}
	if records[1].Packet == nil || records[1].Packet.Name != "disconnect" {
		t.Fatalf("packet metadata = %+v, want the disconnect", records[1].Packet)
	}
}

func TestStreamCloseAfterShutdownKeepsTheGracefulResult(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	harness.session.setDisconnect(disconnectPacket(), nil)

	if err := harness.stream.Shutdown(context.Background(), "bye"); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if err := harness.stream.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := harness.stream.Wait(); err != nil {
		t.Fatalf("Wait() error = %v, want the graceful shutdown to stand", err)
	}
}
