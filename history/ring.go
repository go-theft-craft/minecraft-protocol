// Package history keeps the most recent observations of a live connection in
// memory.
//
// This is the one sink allowed to lose data, and it is the only one that
// should be. A capture exists so that what happened can be reconstructed
// afterwards, and a capture with holes is worse than none: it looks complete
// and is not. A history ring exists so that somebody looking at a running
// process can ask what just happened, and forgetting what happened an hour ago
// is what makes that possible in bounded memory.
//
// It never blocks and never fails, so installing one cannot slow or stop the
// connection it is watching.
package history

import (
	"context"
	"errors"
	"fmt"
	"sync"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

// ErrInvalidRing reports bounds a ring cannot be built with.
var ErrInvalidRing = errors.New("invalid history ring")

// Ring holds the most recent observations, bounded by count and by bytes.
//
// Both bounds matter. A count alone lets a thousand chunk packets hold half a
// gigabyte; a byte bound alone lets a flood of empty keepalives grow the slice
// without limit.
type Ring struct {
	mutex      sync.Mutex
	records    []protocol.Observation
	bytes      int
	maxRecords int
	maxBytes   int
	dropped    uint64
}

// NewRing returns an empty ring.
func NewRing(maxRecords, maxBytes int) (*Ring, error) {
	if maxRecords <= 0 {
		return nil, fmt.Errorf("%w: record bound must be positive, got %d", ErrInvalidRing, maxRecords)
	}
	if maxBytes <= 0 {
		return nil, fmt.Errorf("%w: byte bound must be positive, got %d", ErrInvalidRing, maxBytes)
	}

	return &Ring{maxRecords: maxRecords, maxBytes: maxBytes}, nil
}

// Observe implements protocol.ObservationSink.
//
// It never blocks and never returns an error. A ring that could fail would
// terminate the stream it is watching, and a ring that could block would make
// a debugging aid into a source of latency.
func (r *Ring) Observe(_ context.Context, observation protocol.Observation) error {
	// Owned: the ring outlives the call, and the caller's slice does not.
	observation.Bytes = append([]byte(nil), observation.Bytes...)

	r.mutex.Lock()
	defer r.mutex.Unlock()

	r.records = append(r.records, observation)
	r.bytes += len(observation.Bytes)

	// A record larger than the whole byte bound is kept alone rather than
	// rejected. Dropping the one record somebody is most likely looking for —
	// the outsized one — to protect a bound it exceeds on its own would be
	// backwards.
	for len(r.records) > r.maxRecords || (r.bytes > r.maxBytes && len(r.records) > 1) {
		r.bytes -= len(r.records[0].Bytes)
		r.records[0] = protocol.Observation{}
		r.records = r.records[1:]
		r.dropped++
	}

	return nil
}

// Snapshot returns the retained observations, oldest first.
//
// The payloads are copies. A caller printing a snapshot while the connection
// runs would otherwise be reading bytes the ring may evict underneath it.
func (r *Ring) Snapshot() []protocol.Observation {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	copied := make([]protocol.Observation, len(r.records))
	for index, record := range r.records {
		record.Bytes = append([]byte(nil), record.Bytes...)
		copied[index] = record
	}

	return copied
}

// Dropped reports how many observations have been evicted since the ring was
// created. It is the number that says whether a snapshot is the whole story.
func (r *Ring) Dropped() uint64 {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	return r.dropped
}

// Len reports how many observations the ring currently holds.
func (r *Ring) Len() int {
	r.mutex.Lock()
	defer r.mutex.Unlock()

	return len(r.records)
}

var _ protocol.ObservationSink = (*Ring)(nil)
