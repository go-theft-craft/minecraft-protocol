package history_test

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/history"
)

func observation(sequence uint64, payload []byte) protocol.Observation {
	return protocol.Observation{
		Sequence: sequence,
		Stage:    protocol.ObservationPacket,
		Bytes:    payload,
	}
}

func newRing(t *testing.T, maxRecords, maxBytes int) *history.Ring {
	t.Helper()

	ring, err := history.NewRing(maxRecords, maxBytes)
	if err != nil {
		t.Fatalf("NewRing: %v", err)
	}

	return ring
}

func TestAFullRingEvictsTheOldestFirst(t *testing.T) {
	t.Parallel()

	ring := newRing(t, 3, 1<<20)
	for sequence := uint64(1); sequence <= 5; sequence++ {
		if err := ring.Observe(t.Context(), observation(sequence, []byte{byte(sequence)})); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}

	records := ring.Snapshot()
	if len(records) != 3 {
		t.Fatalf("ring holds %d records, want 3", len(records))
	}
	for index, want := range []uint64{3, 4, 5} {
		if records[index].Sequence != want {
			t.Fatalf("record %d has sequence %d, want %d", index, records[index].Sequence, want)
		}
	}
	if dropped := ring.Dropped(); dropped != 2 {
		t.Fatalf("Dropped = %d, want 2", dropped)
	}
}

func TestTheByteBoundEvictsBeforeTheRecordBound(t *testing.T) {
	t.Parallel()

	ring := newRing(t, 100, 300)
	for sequence := uint64(1); sequence <= 5; sequence++ {
		if err := ring.Observe(t.Context(), observation(sequence, make([]byte, 100))); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}

	if got := ring.Len(); got != 3 {
		t.Fatalf("ring holds %d records, want 3 under a 300-byte bound", got)
	}
	if dropped := ring.Dropped(); dropped != 2 {
		t.Fatalf("Dropped = %d, want 2", dropped)
	}
}

// TestARecordLargerThanTheBoundIsKeptAlone covers the case a naive eviction
// loop turns into an empty ring: the one record somebody most wants to see is
// the one too big to fit beside anything else.
func TestARecordLargerThanTheBoundIsKeptAlone(t *testing.T) {
	t.Parallel()

	ring := newRing(t, 100, 64)
	if err := ring.Observe(t.Context(), observation(1, make([]byte, 4096))); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	records := ring.Snapshot()
	if len(records) != 1 || records[0].Sequence != 1 {
		t.Fatalf("ring holds %d records, want the oversized one alone", len(records))
	}
	if len(records[0].Bytes) != 4096 {
		t.Fatalf("the retained record carries %d bytes, want all 4096", len(records[0].Bytes))
	}

	// And the next record pushes it out rather than being refused itself.
	if err := ring.Observe(t.Context(), observation(2, []byte{0x01})); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	records = ring.Snapshot()
	if len(records) != 1 || records[0].Sequence != 2 {
		t.Fatalf("ring holds %+v, want only the newer record", records)
	}
}

func TestSnapshotPayloadsDoNotAliasTheRing(t *testing.T) {
	t.Parallel()

	ring := newRing(t, 4, 1<<20)
	payload := []byte{0x01, 0x02}
	if err := ring.Observe(t.Context(), observation(1, payload)); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	// Neither the caller's slice nor a snapshot may be a window onto what the
	// ring holds.
	payload[0] = 0xff
	first := ring.Snapshot()
	first[0].Bytes[1] = 0xff

	second := ring.Snapshot()
	if !bytes.Equal(second[0].Bytes, []byte{0x01, 0x02}) {
		t.Fatalf("the ring holds %x, want the bytes it was given", second[0].Bytes)
	}
}

func TestNewRingRejectsNonPositiveBounds(t *testing.T) {
	t.Parallel()

	if _, err := history.NewRing(0, 1024); !errors.Is(err, history.ErrInvalidRing) {
		t.Fatalf("NewRing(0, …) error = %v, want ErrInvalidRing", err)
	}
	if _, err := history.NewRing(16, 0); !errors.Is(err, history.ErrInvalidRing) {
		t.Fatalf("NewRing(…, 0) error = %v, want ErrInvalidRing", err)
	}
}

func TestSnapshotIsSafeWhileObserving(t *testing.T) {
	t.Parallel()

	ring := newRing(t, 32, 1<<20)

	var waiting sync.WaitGroup
	waiting.Add(2)

	go func() {
		defer waiting.Done()
		for sequence := uint64(1); sequence <= 500; sequence++ {
			_ = ring.Observe(t.Context(), observation(sequence, []byte{byte(sequence)}))
		}
	}()
	go func() {
		defer waiting.Done()
		for range 500 {
			for _, record := range ring.Snapshot() {
				_ = record.Sequence
			}
			_ = ring.Dropped()
			_ = ring.Len()
		}
	}()

	waiting.Wait()

	if ring.Len() > 32 {
		t.Fatalf("ring holds %d records, want at most its bound", ring.Len())
	}
}

func TestRingIsAnObservationSink(t *testing.T) {
	t.Parallel()

	var sink protocol.ObservationSink = newRing(t, 4, 1024)
	if err := sink.Observe(t.Context(), observation(1, nil)); err != nil {
		t.Fatalf("Observe: %v", err)
	}
}
