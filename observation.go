package protocol

import (
	"bytes"
	"context"
	"fmt"
	"time"
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
	// ObservationRejected is recorded when a write the stream accepted never
	// reached the transport. It is the one stage that describes the consumer
	// rather than the connection: nothing crossed the wire, so a replay skips
	// it and a digest excludes it.
	//
	// A write abandoned before it reached the coordinator produces no record,
	// because nothing in the stream ever saw it.
	ObservationRejected ObservationStage = "rejected"
	// ObservationSecret is recorded when secret material is installed on the
	// conduit. It carries the key only under WithSecretDisclosure; otherwise
	// it marks the switch point and nothing more, so a capture always shows
	// when encryption began.
	ObservationSecret ObservationStage = "secret"
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
	// Elapsed is how long after the stream started this record was taken. It
	// is stamped where the record is made rather than where it is delivered,
	// so a sink that falls behind cannot rewrite the timing of a connection
	// it was only watching.
	Elapsed time.Duration
	// Before and After are the session snapshots either side of this record's
	// commit point. They are equal when the record changed nothing.
	Before Snapshot
	After  Snapshot
	// Packet is present on ObservationPacket records.
	Packet *PacketMetadata
	// Bytes is owned by the record: raw records hold the complete frame
	// including its length prefix, packet records hold the decoded body.
	Bytes []byte
	// Redacted reports that Bytes was withheld. It is set per record rather
	// than inferred from stream configuration, so a sink never has to guess
	// whether it holds a real body or a placeholder.
	Redacted bool
	// OriginalLen is how many bytes the record describes, whether or not it
	// carries them. A redacted record drops its payload before a sink sees
	// it, and without this a capture could not report the size it withheld —
	// which is the one thing about a withheld body that is safe to state.
	//
	// It is zero on a redacted ObservationSecret record, and that is not an
	// omission: the material is never read at all unless disclosure was
	// asked for, so there is no length to report without materializing the
	// key in order to measure it.
	OriginalLen int
	// Secret is present on ObservationSecret records and names the kind of
	// material the record describes.
	Secret *SecretMetadata
	// Rejected is present on ObservationRejected records and says why the
	// write stopped.
	Rejected *RejectionMetadata
}

// RejectionMetadata says why a write never reached the transport.
//
// The reason is text rather than an error, because an observation is a record
// rather than a control path: a sink writes it to a file or a log, and a
// caller that needs to act on the failure already has the error returned from
// Write.
type RejectionMetadata struct {
	Reason string
}

// SecretMetadata names the kind of secret material a record carries. A capture
// with more than one kind of secret in it is ambiguous without this, and a
// discriminator cannot be added retroactively to captures already written.
type SecretMetadata struct {
	Label string
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

// observationInput is what one call to observe describes. It is a struct
// rather than a parameter list because a nine-argument call whose arguments
// are three snapshots, two pointers, and two booleans is unreadable at the
// call site and easy to transpose.
type observationInput struct {
	direction Direction
	stage     ObservationStage
	frame     uint64
	before    Snapshot
	after     Snapshot
	packet    *PacketMetadata
	secret    *SecretMetadata
	rejected  *RejectionMetadata
	payload   []byte
	redacted  bool
}

// observe submits one record to the dispatcher. It charges the shared budget
// first, so a slow sink applies backpressure instead of growing memory.
func (s *Stream) observe(input observationInput) error {
	if s.sink == nil {
		return nil
	}

	// Stamped before the budget is charged. Charging can block on a slow
	// sink, and a timestamp taken after it would describe the consumer.
	elapsed := time.Since(s.start)

	body := input.payload
	if input.redacted {
		body = nil
	}
	charge := len(body)
	if err := s.queued.acquireUntil(s.stopping, 1, charge); err != nil {
		return err
	}

	s.sequence++
	record := observationRecord{
		observation: Observation{
			Sequence:  s.sequence,
			Frame:     input.frame,
			Elapsed:   elapsed,
			Direction: input.direction,
			Stage:     input.stage,
			Before:    input.before.Clone(),
			After:     input.after.Clone(),
			Packet:    input.packet,
			Secret:    input.secret,
			Rejected:  input.rejected,
			// Owned bytes: a borrowed frame view would change under the
			// observer as soon as the stream reuses the buffer.
			Bytes:       bytes.Clone(body),
			Redacted:    input.redacted,
			OriginalLen: len(input.payload),
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

// sensitive reports whether the session withholds this packet's body.
func (s *Stream) sensitive(packet Packet) bool {
	if s.disclosureReason != "" {
		return false
	}

	marker, ok := s.session.(SensitivePackets)

	return ok && marker.Sensitive(packet)
}

// sensitiveFrame reports whether the session withholds this frame's wire
// bytes. It is the raw record's half of the same question, asked before the
// frame is decoded.
func (s *Stream) sensitiveFrame(direction Direction, framePayload []byte) bool {
	if s.disclosureReason != "" {
		return false
	}

	marker, ok := s.session.(SensitiveFrames)

	return ok && marker.SensitiveFrame(direction, framePayload)
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
