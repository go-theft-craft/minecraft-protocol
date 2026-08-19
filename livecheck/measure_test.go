//go:build livecheck

package livecheck

import (
	"context"
	"fmt"
	"maps"
	"sort"
	"sync"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

// sizeRecord is the largest thing seen at one stage, and what it belonged to.
type sizeRecord struct {
	bytes  int
	packet string
}

// stateTotals is what one session state produced.
//
// The totals are per state because the question the play check asks is not the
// one the login check asks. Login is bounded — a fixed sequence of registry
// data — and play is not, so a single largest-frame number across the whole
// connection would let login's registry data stand in for play's chunk data,
// or the reverse, and neither would say which state the limit has to hold for.
type stateTotals struct {
	largestFrame   sizeRecord
	largestDecoded sizeRecord
	frames         int
	packets        int
	frameBytes     int64
	decodedBytes   int64
	// byPacket is the largest decoded body per packet name, so a report can
	// name the packets that come closest to the ceiling rather than only the
	// one that reached it.
	byPacket map[string]int
	// countByPacket pairs with byPacket: a packet that is large once and a
	// packet that is large a thousand times are different facts about a
	// connection.
	countByPacket map[string]int
	// framesByPacket is the largest raw frame per packet name. A raw frame is
	// recorded before anything has decoded it, so it arrives without a name;
	// the name comes from the packet record for the same frame.
	framesByPacket map[string]int
}

func newStateTotals() *stateTotals {
	return &stateTotals{
		byPacket:       map[string]int{},
		countByPacket:  map[string]int{},
		framesByPacket: map[string]int{},
	}
}

// sizeSink records the largest raw frame and the largest decoded body a real
// connection produces, bucketed by the state it produced them in.
//
// The measurement is the point of the exercise. Every limit in this repository
// was chosen from the specification rather than from traffic, and a real
// server's registry data and chunk data are the first things large enough to
// test that choice.
type sizeSink struct {
	mutex  sync.Mutex
	states map[protocol.State]*stateTotals
	order  []protocol.State
	// pending holds each raw frame's size until its packet record names it.
	// Observations for one frame arrive in order and adjacently, so this map
	// holds one entry at a time in practice; it is a map rather than a field
	// so that a frame which never decodes cannot rename the next one.
	pending map[uint64]pendingFrame
}

// pendingFrame is a raw frame waiting for the packet record that names it.
type pendingFrame struct {
	bytes int
	state protocol.State
}

func newSizeSink() *sizeSink {
	return &sizeSink{
		states:  map[protocol.State]*stateTotals{},
		pending: map[uint64]pendingFrame{},
	}
}

func (s *sizeSink) Observe(_ context.Context, observation protocol.Observation) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	name := "unnamed"
	// The state a raw frame belongs to is the state the stream was in when it
	// arrived, not the one it may have moved to as a result: a login success
	// frame is a login frame however it leaves the session.
	state := observation.Before.State
	if observation.Packet != nil {
		// A packet this check wrote carries whatever name the writer gave it,
		// and the negotiator's own packets are written without one. An
		// unnamed record still counts; it is only its label that is missing.
		name = observation.Packet.Name
		if name == "" {
			name = fmt.Sprintf("unnamed %#x", observation.Packet.ID)
		}
		state = observation.Packet.State
	}

	totals := s.totalsFor(state)

	switch observation.Stage {
	case protocol.ObservationRawFrame:
		totals.frames++
		totals.frameBytes += int64(observation.OriginalLen)
		if observation.OriginalLen > totals.largestFrame.bytes {
			totals.largestFrame = sizeRecord{bytes: observation.OriginalLen, packet: name}
		}
		s.pending[observation.Frame] = pendingFrame{bytes: observation.OriginalLen, state: state}
	case protocol.ObservationPacket:
		totals.packets++
		totals.decodedBytes += int64(observation.OriginalLen)
		totals.countByPacket[name]++
		if observation.OriginalLen > totals.byPacket[name] {
			totals.byPacket[name] = observation.OriginalLen
		}
		if observation.OriginalLen > totals.largestDecoded.bytes {
			totals.largestDecoded = sizeRecord{bytes: observation.OriginalLen, packet: name}
		}

		if raw, waiting := s.pending[observation.Frame]; waiting {
			delete(s.pending, observation.Frame)
			// The raw record was counted against the state the frame arrived
			// in, so its name is recorded there too even when the packet moved
			// the session on.
			rawTotals := s.totalsFor(raw.state)
			if raw.bytes > rawTotals.framesByPacket[name] {
				rawTotals.framesByPacket[name] = raw.bytes
			}
			if raw.bytes >= rawTotals.largestFrame.bytes {
				rawTotals.largestFrame = sizeRecord{bytes: raw.bytes, packet: name}
			}
		}
	case protocol.ObservationRejected, protocol.ObservationSecret:
	}

	return nil
}

// totalsFor returns the bucket for a state, creating it on first sight. The
// caller holds the lock.
func (s *sizeSink) totalsFor(state protocol.State) *stateTotals {
	totals, known := s.states[state]
	if !known {
		totals = newStateTotals()
		s.states[state] = totals
		s.order = append(s.order, state)
	}

	return totals
}

// snapshot copies the totals for one state, so a caller reports what it read
// without holding the sink's lock while it formats.
func (s *sizeSink) snapshot(state protocol.State) stateTotals {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	totals, known := s.states[state]
	if !known {
		return *newStateTotals()
	}

	copied := *totals
	copied.byPacket = maps.Clone(totals.byPacket)
	copied.countByPacket = maps.Clone(totals.countByPacket)
	copied.framesByPacket = maps.Clone(totals.framesByPacket)

	return copied
}

// states returns the states seen, in the order they first produced a record.
func (s *sizeSink) seenStates() []protocol.State {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return append([]protocol.State(nil), s.order...)
}

// largestPackets returns the count largest packets by decoded body, largest
// first, formatted for a log line.
func (t stateTotals) largestPackets(count int) []string {
	names := make([]string, 0, len(t.byPacket))
	for name := range t.byPacket {
		names = append(names, name)
	}
	sort.Slice(names, func(i, j int) bool {
		if t.byPacket[names[i]] != t.byPacket[names[j]] {
			return t.byPacket[names[i]] > t.byPacket[names[j]]
		}

		return names[i] < names[j]
	})
	if len(names) > count {
		names = names[:count]
	}

	lines := make([]string, 0, len(names))
	for _, name := range names {
		lines = append(lines, fmt.Sprintf(
			"%s: %d bytes decoded, %d bytes on the wire, %d seen",
			name, t.byPacket[name], t.framesByPacket[name], t.countByPacket[name],
		))
	}

	return lines
}
