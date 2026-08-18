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
