package protocol

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
)

// recordingSink keeps every observation it receives.
type recordingSink struct {
	mu       sync.Mutex
	records  []Observation
	err      error
	block    chan struct{}
	observed chan struct{}
}

func newRecordingSink() *recordingSink {
	return &recordingSink{observed: make(chan struct{}, 1024)}
}

func (s *recordingSink) Observe(_ context.Context, observation Observation) error {
	s.mu.Lock()
	blocker, failure := s.block, s.err
	s.mu.Unlock()

	if blocker != nil {
		<-blocker
	}
	if failure != nil {
		return failure
	}

	s.mu.Lock()
	s.records = append(s.records, observation)
	s.mu.Unlock()

	select {
	case s.observed <- struct{}{}:
	default:
	}

	return nil
}

func (s *recordingSink) all() []Observation {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]Observation(nil), s.records...)
}

func (s *recordingSink) setErr(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.err = err
}

func (s *recordingSink) waitFor(t *testing.T, count int) []Observation {
	t.Helper()

	for range 200 {
		if records := s.all(); len(records) >= count {
			return records
		}
		<-s.observed
	}

	t.Fatalf("sink received %d observations, want %d", len(s.all()), count)
	return nil
}

func TestWithObservationSinkRejectsNil(t *testing.T) {
	t.Parallel()

	stream, _ := newTestStream(t)
	if err := WithObservationSink(nil)(stream); !errors.Is(err, ErrInvalidStream) {
		t.Fatalf("WithObservationSink(nil) error = %v, want ErrInvalidStream", err)
	}
}

func TestStreamObservesInboundFramesAndPackets(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	harness := startRuntime(t, 8, WithObservationSink(sink))

	wire := testFrameBytes(3, 0xaa, 0xbb)
	harness.reader.deliver(wire)

	packet := readWithTimeout(t, harness.stream)
	records := sink.waitFor(t, 2)

	raw, semantic := records[0], records[1]
	if raw.Stage != ObservationRawFrame || semantic.Stage != ObservationPacket {
		t.Fatalf("stages = %q, %q, want raw then packet", raw.Stage, semantic.Stage)
	}
	if raw.Sequence != 1 || semantic.Sequence != 2 {
		t.Fatalf("sequences = %d, %d, want 1 then 2", raw.Sequence, semantic.Sequence)
	}
	if raw.Frame != semantic.Frame {
		t.Fatalf("frame IDs = %d, %d, want them correlated", raw.Frame, semantic.Frame)
	}
	if raw.Direction != DirectionClientbound || semantic.Direction != DirectionClientbound {
		t.Fatal("observations recorded the wrong direction")
	}

	// The raw record keeps the complete frame, including its length prefix.
	if !bytes.Equal(raw.Bytes, wire) {
		t.Fatalf("raw bytes = %x, want the whole frame %x", raw.Bytes, wire)
	}
	if !bytes.Equal(semantic.Bytes, packet.Payload) {
		t.Fatalf("packet bytes = %x, want the packet body %x", semantic.Bytes, packet.Payload)
	}

	if raw.Packet != nil {
		t.Error("a raw frame record carried packet metadata")
	}
	if semantic.Packet == nil || semantic.Packet.ID != 3 {
		t.Fatalf("packet metadata = %+v, want ID 3", semantic.Packet)
	}
}

func TestStreamObservationBytesDoNotAliasStreamBuffers(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	harness := startRuntime(t, 8, WithObservationSink(sink))

	harness.reader.deliver(testFrameBytes(4, 0x01, 0x02))
	packet := readWithTimeout(t, harness.stream)
	records := sink.waitFor(t, 2)

	// Mutating what the reader received must not change the capture.
	packet.Payload[0] = 0xff
	for _, record := range records {
		for _, value := range record.Bytes {
			if value == 0xff {
				t.Fatal("an observation aliased a stream buffer")
			}
		}
	}
}

func TestStreamObservationSnapshotsAreIndependent(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	harness := startRuntime(t, 8, WithObservationSink(sink))
	harness.session.setProposeHook(stateTransition(State("login")))

	harness.reader.deliver(testFrameBytes(1, 0x01))
	readWithTimeout(t, harness.stream)
	records := sink.waitFor(t, 2)

	semantic := records[1]
	if semantic.Before.State != State("play") {
		t.Errorf("Before.State = %q, want the state before the packet", semantic.Before.State)
	}
	if semantic.After.State != State("login") {
		t.Errorf("After.State = %q, want the committed state", semantic.After.State)
	}

	// Mutating one snapshot must not disturb the other, or the session.
	semantic.Before.Pipeline["stage"] = "mutated"
	if semantic.After.Pipeline["stage"] == "mutated" {
		t.Fatal("Before and After share one pipeline map")
	}
	if harness.session.Snapshot().Pipeline["stage"] == "mutated" {
		t.Fatal("an observation snapshot aliases the session pipeline")
	}
}

func TestStreamObservesOutboundFramesAndPackets(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	harness := startRuntime(t, 8, WithObservationSink(sink))

	if err := harness.stream.Write(context.Background(), Packet{ID: 9, Payload: []byte{0x07}}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	records := sink.waitFor(t, 2)

	if records[0].Stage != ObservationRawFrame || records[1].Stage != ObservationPacket {
		t.Fatalf("stages = %q, %q, want raw then packet", records[0].Stage, records[1].Stage)
	}
	for _, record := range records {
		if record.Direction != DirectionServerbound {
			t.Fatalf("direction = %d, want serverbound", record.Direction)
		}
	}
	if want := testFrameBytes(9, 0x07); !bytes.Equal(records[0].Bytes, want) {
		t.Fatalf("raw bytes = %x, want %x", records[0].Bytes, want)
	}
}

func TestStreamObservationSequenceIsGlobal(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	harness := startRuntime(t, 8, WithObservationSink(sink))

	harness.reader.deliver(testFrameBytes(1, 0x01))
	readWithTimeout(t, harness.stream)
	if err := harness.stream.Write(context.Background(), Packet{ID: 2, Payload: []byte{0x02}}); err != nil {
		t.Fatal(err)
	}
	harness.reader.deliver(testFrameBytes(3, 0x03))
	readWithTimeout(t, harness.stream)

	records := sink.waitFor(t, 6)
	for index, record := range records {
		if record.Sequence != uint64(index+1) {
			t.Fatalf("record %d has sequence %d, want %d", index, record.Sequence, index+1)
		}
	}

	// Frame IDs increase across directions too, and correlate in pairs.
	for index := 0; index < len(records); index += 2 {
		if records[index].Frame != records[index+1].Frame {
			t.Fatalf("records %d and %d have frame IDs %d and %d", index, index+1, records[index].Frame, records[index+1].Frame)
		}
	}
}

func TestStreamObservationSinkFailureIsFatal(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	sentinel := errors.New("sink failed")
	sink.setErr(sentinel)

	harness := startRuntime(t, 8, WithObservationSink(sink))
	harness.reader.deliver(testFrameBytes(1, 0x01))

	err := harness.stream.Wait()
	if !errors.Is(err, ErrObservation) {
		t.Fatalf("Wait() error = %v, want ErrObservation", err)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("Wait() error = %v, want it to preserve the sink error", err)
	}
}

func TestStreamObservationBackpressureUsesTheSharedBudget(t *testing.T) {
	t.Parallel()

	sink := newRecordingSink()
	blocker := make(chan struct{})
	sink.mu.Lock()
	sink.block = blocker
	sink.mu.Unlock()

	// Two queue items: the inbound packet and its observations compete for the
	// same budget, so a stuck sink must stop the stream rather than grow.
	harness := startRuntime(t, 2, WithObservationSink(sink))

	for id := byte(1); id <= 4; id++ {
		harness.reader.deliver(testFrameBytes(id, id))
	}

	// Nothing is delivered while the sink is stuck.
	if records := sink.all(); len(records) != 0 {
		t.Fatalf("sink recorded %d observations while blocked", len(records))
	}

	// Releasing the sink lets the stream drain.
	close(blocker)
	sink.mu.Lock()
	sink.block = nil
	sink.mu.Unlock()

	for id := byte(1); id <= 4; id++ {
		packet := readWithTimeout(t, harness.stream)
		if packet.ID != int32(id) {
			t.Fatalf("Read() ID = %d, want %d", packet.ID, id)
		}
	}
}

func TestStreamWithoutSinkRecordsNothing(t *testing.T) {
	t.Parallel()

	harness := startRuntime(t, 8)
	harness.reader.deliver(testFrameBytes(1, 0x01))

	packet := readWithTimeout(t, harness.stream)
	if packet.ID != 1 {
		t.Fatalf("Read() ID = %d, want 1", packet.ID)
	}
	if harness.stream.sequence != 0 {
		t.Fatalf("stream assigned %d observation sequences without a sink", harness.stream.sequence)
	}
}
