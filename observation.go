package protocol

import (
	"bytes"
	"context"
	"fmt"
)

// ObservationStage names the point in the pipeline that produced a record.
type ObservationStage string

const (
	// ObservationRawFrame is recorded once the stream owns one complete frame
	// and before any decompression or decoding.
	ObservationRawFrame ObservationStage = "raw_frame"
	// ObservationPacket is recorded after decoding and after the packet's own
	// transition has been committed.
	ObservationPacket ObservationStage = "packet"
)

// PacketMetadata describes a decoded packet without exposing its value. A
// generated packet value is mutable, so handing it to an observer would let
// one observer change what a later one sees.
type PacketMetadata struct {
	State     State
	Direction Direction
	ID        int32
	Name      string
}

// Observation is one immutable record of stream activity.
//
// Sequence increases by one across the whole stream, in both directions.
// Frame correlates the records that describe the same frame, so a raw record
// and its packet record can be matched.
type Observation struct {
	Sequence  uint64
	Frame     uint64
	Direction Direction
	Stage     ObservationStage
	// Before and After are the session snapshots either side of this record's
	// commit point. They are equal when the record changed nothing.
	Before Snapshot
	After  Snapshot
	// Packet is present on ObservationPacket records.
	Packet *PacketMetadata
	// Bytes is owned by the record: raw records hold the complete frame
	// including its length prefix, packet records hold the decoded body.
	Bytes []byte
}

// ObservationSink receives observations in order.
//
// Delivery is lossless. A sink that blocks applies backpressure to the whole
// stream, and a sink that fails terminates it, because a capture with holes is
// worse than no capture.
type ObservationSink interface {
	Observe(context.Context, Observation) error
}

// WithObservationSink installs the sink that receives stream observations.
func WithObservationSink(sink ObservationSink) StreamOption {
	return func(stream *Stream) error {
		if sink == nil {
			return fmt.Errorf("%w: nil observation sink", ErrInvalidStream)
		}
		stream.sink = sink

		return nil
	}
}

// observationRecord pairs a record with the budget charge to release once the
// sink has consumed it.
type observationRecord struct {
	observation Observation
	bytes       int
}

// observe submits one record to the dispatcher. It charges the shared budget
// first, so a slow sink applies backpressure instead of growing memory.
func (s *Stream) observe(
	direction Direction,
	stage ObservationStage,
	frame uint64,
	before, after Snapshot,
	metadata *PacketMetadata,
	payload []byte,
) error {
	if s.sink == nil {
		return nil
	}

	charge := len(payload)
	if err := s.queued.acquireUntil(s.stopping, 1, charge); err != nil {
		return err
	}

	s.sequence++
	record := observationRecord{
		observation: Observation{
			Sequence:  s.sequence,
			Frame:     frame,
			Direction: direction,
			Stage:     stage,
			Before:    before.Clone(),
			After:     after.Clone(),
			Packet:    metadata,
			// Owned bytes: a borrowed frame view would change under the
			// observer as soon as the stream reuses the buffer.
			Bytes: bytes.Clone(payload),
		},
		bytes: charge,
	}

	select {
	case s.observations <- record:
		return nil
	case <-s.stopping:
		s.queued.release(1, charge)
		return ErrStreamClosed
	}
}

// dispatchObservations delivers records to the sink one at a time, in order.
func (s *Stream) dispatchObservations(ctx context.Context) {
	if s.sink == nil {
		return
	}

	for {
		select {
		case record := <-s.observations:
			err := s.sink.Observe(ctx, record.observation)
			s.queued.release(1, record.bytes)
			if err != nil {
				s.fail(fmt.Errorf("%w: %w", ErrObservation, err))
				s.stop()

				return
			}
		case <-s.observationsDone:
			// Deliver whatever is still queued before finishing, so a clean
			// shutdown does not truncate the capture.
			s.drainObservations(ctx)

			return
		case <-s.stopping:
			return
		}
	}
}

func (s *Stream) drainObservations(ctx context.Context) {
	for {
		select {
		case record := <-s.observations:
			err := s.sink.Observe(ctx, record.observation)
			s.queued.release(1, record.bytes)
			if err != nil {
				s.fail(fmt.Errorf("%w: %w", ErrObservation, err))
				s.stop()

				return
			}
		default:
			return
		}
	}
}

// SecretDisclosure is implemented by a TransportControl that carries secret
// material a disclosing capture needs in order to be decryptable later.
//
// The two methods are separate because a stream needs them at different times.
// SecretLabel is called on every switch, so a redacted capture still records
// what kind of material was installed and when. DisclosedSecret is called only
// when the developer passed WithSecretDisclosure, so the default path never
// materializes a key it would immediately discard. It must return a copy the
// caller may retain.
//
// The stream interprets neither value. It copies both into the record and
// hands it to the sink.
type SecretDisclosure interface {
	SecretLabel() string
	DisclosedSecret() []byte
}

// packetMetadata copies the identifying fields of a packet.
func packetMetadata(packet Packet) *PacketMetadata {
	return &PacketMetadata{
		State:     packet.State,
		Direction: packet.Direction,
		ID:        packet.ID,
		Name:      packet.Name,
	}
}
