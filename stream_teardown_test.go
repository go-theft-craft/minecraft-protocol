package protocol

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"
)

// TestStreamKeepsTheLastPacketWhenThePeerHangsUpDuringDecode pins the packet a
// peer sends immediately before it leaves.
//
// A server that kicks writes its disconnect and closes, so the frame and the
// EOF behind it arrive together. The read pump hands the frame over and then
// reads the EOF, which stops the stream and closes the shared budget — and the
// coordinator is still holding a decoded packet whose observation and queue
// slot both need that budget. Refusing them cost the packet, which is the one
// that says why the connection ended: the reader saw a bare EOF and a caller
// could only report that the transport went away for no stated reason.
func TestStreamKeepsTheLastPacketWhenThePeerHangsUpDuringDecode(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8, WithObservationSink(newRecordingSink()))

	decoding := make(chan struct{})
	resume := make(chan struct{})
	harness.session.setProposeHook(func(Packet) (Transition, bool, error) {
		close(decoding)
		<-resume

		return Transition{}, false, nil
	})

	harness.reader.deliver(testFrameBytes(9, 0x2a))
	<-decoding

	// The peer is gone before the packet it sent has been queued.
	harness.reader.fail(io.EOF)
	waitUntilEnding(t, harness.stream)
	close(resume)

	packet := readWithTimeout(t, harness.stream)
	if packet.ID != 9 {
		t.Fatalf("Read() ID = %d, want 9 — the packet that arrived before the EOF", packet.ID)
	}

	if _, err := harness.stream.Read(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("Read() after the last packet = %v, want the EOF that ended it", err)
	}

	// A packet delivered around the reservation path still has to balance the
	// release the reader performs, or the budget underflows and panics.
	if items, bytes := harness.stream.queued.usage(); items != 0 || bytes != 0 {
		t.Errorf("budget usage after the last packet = %d items, %d bytes, want 0 and 0", items, bytes)
	}
}

// TestStreamHandsOverAPacketWhoseReservationTheClosingBudgetRefused covers the
// same loss one step further along the queue.
//
// A packet decoded while the queue is full waits for capacity. If the stream
// ends while it waits, the budget refuses every waiter it is holding — and the
// packet behind that reservation has still arrived. It goes to the reader, and
// the capacity it takes is charged so that the reader's release still balances.
func TestStreamHandsOverAPacketWhoseReservationTheClosingBudgetRefused(t *testing.T) {
	t.Parallel()

	stream, _ := newTestStream(t)

	// Fill the budget, so the reservation behind it can only wait.
	full, err := stream.queued.reserve(1, 0)
	if err != nil {
		t.Fatalf("reserve() error = %v", err)
	}
	for {
		items, _ := stream.queued.usage()
		if items >= stream.queued.maxItems {
			break
		}
		if _, err := stream.queued.reserve(1, 0); err != nil {
			t.Fatalf("reserve() error = %v", err)
		}
	}
	_ = full

	waiting, err := stream.queued.reserve(1, 4)
	if err != nil {
		t.Fatalf("reserve() while full error = %v", err)
	}
	if waiting.failure() == nil {
		t.Fatal("the reservation was granted; this test needs one that waits")
	}

	stream.queued.close(io.EOF)

	stream.handOff(&pendingInbound{
		item:   inboundItem{packet: Packet{ID: 7, Payload: []byte{1, 2, 3, 4}}, bytes: 4},
		waiter: waiting,
	})

	select {
	case item := <-stream.inboundPackets:
		if item.packet.ID != 7 {
			t.Fatalf("handed over packet ID = %d, want 7", item.packet.ID)
		}
	default:
		t.Fatal("the packet was dropped, not handed to the reader")
	}
}

// waitUntilEnding blocks until stop has been called, which is what closes the
// budget the coordinator is about to ask.
func waitUntilEnding(t *testing.T, stream *Stream) {
	t.Helper()

	deadline := time.After(5 * time.Second)
	for !stream.ending() {
		select {
		case <-deadline:
			t.Fatal("the stream never stopped after the peer hung up")
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// TestStreamReportsAWriteThePeerAlreadyHasAsWritten pins the outbound half of
// the same rule.
//
// A frame the transport has taken is written, whatever the connection does a
// moment later. The stream reported one as failed in two ways: its observation
// could not be charged to a budget that closes with the transport, and the stop
// and the write pump's result become ready in the same instant, where a select
// picks between ready cases at random. Both told a caller to send again
// something the peer already had — and a client acknowledging its placement to a
// server that then hung up read the acknowledgement it had just delivered as a
// connection that ended before it was placed.
func TestStreamReportsAWriteThePeerAlreadyHasAsWritten(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	blocker := make(chan struct{})
	sink.mu.Lock()
	sink.block = blocker
	sink.mu.Unlock()
	defer close(blocker)

	// A sink that never returns holds the budget item of the record it is
	// stuck on, so the write below reaches the transport and then has to wait
	// for room to record itself. That is the window the peer closes in.
	harness := startRuntime(t, 2, WithObservationSink(sink))

	written := make(chan error, 1)
	go func() {
		written <- harness.stream.Write(context.Background(), Packet{ID: 2, Payload: []byte{0x02}})
	}()
	waitForFrames(t, harness.writer, 1)

	harness.reader.fail(io.EOF)

	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("Write() = %v, want nil: the peer already has the frame", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Write never returned after the peer hung up")
	}
}

// waitForFrames blocks until the transport has taken at least count frames.
func waitForFrames(t *testing.T, writer *syncWriter, count int) {
	t.Helper()

	deadline := time.After(5 * time.Second)
	for writer.writeCount() < count {
		select {
		case <-deadline:
			t.Fatalf("the transport took %d frames, want %d", writer.writeCount(), count)
		default:
			time.Sleep(time.Millisecond)
		}
	}
}

// TestStreamReportsAWriteThatWasInFlightWhenThePeerLeft covers the window the
// one above does not: the frame has not finished leaving when the peer closes.
//
// Bytes reach a peer before the transport call returns, so "the pump has not
// reported yet" never meant "nothing was sent". Giving up on the stop reported
// a delivered frame as refused, and told the caller to send again something the
// peer already had. The pump is the only witness, and the stop it races
// interrupts the transport, so waiting for it is bounded.
func TestStreamReportsAWriteThatWasInFlightWhenThePeerLeft(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)

	entered, release := harness.writer.hold()
	defer release()

	written := make(chan error, 1)
	go func() {
		written <- harness.stream.Write(context.Background(), Packet{ID: 3, Payload: []byte{0x03}})
	}()

	// The write is inside the transport when the peer goes.
	<-entered
	harness.reader.fail(io.EOF)
	waitUntilEnding(t, harness.stream)
	release()

	select {
	case err := <-written:
		if err != nil {
			t.Fatalf("Write() = %v, want nil: the transport took the frame", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Write never returned")
	}

	if count := harness.writer.writeCount(); count != 1 {
		t.Errorf("the transport took %d frames, want 1", count)
	}
}
