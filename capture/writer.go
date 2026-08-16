package capture

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"hash/crc32"
	"io"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

// Writer streams observations into a capture.
//
// It implements protocol.ObservationSink, so a stream writes to it directly.
// Nothing is buffered beyond the record being assembled: a capture of a
// connection that is still running is readable up to its last complete record,
// which is the property that makes a killed process leave a usable file.
//
// A Writer is not safe for concurrent use. The observation path delivers
// records one at a time, in order, from one goroutine.
type Writer struct {
	sink       io.Writer
	header     Header
	strings    map[string]uint32
	scratch    []byte
	body       []byte
	records    uint64
	last       uint64
	digest     *Digester
	closed     bool
	disclosing bool
}

// WriterOption configures a writer at construction.
type WriterOption func(*Writer) error

// WithDisclosure builds a writer that stores secret material, for the stated
// reason.
//
// It exists as an explicit constructor argument rather than a field on the
// header, because a capture holding a session key should be the result of
// someone writing down why they wanted one.
func WithDisclosure(reason string) WriterOption {
	return func(w *Writer) error {
		if reason == "" {
			return fmt.Errorf("%w: disclosure needs a stated reason", ErrInvalidCapture)
		}
		w.disclosing = true
		w.header.Redaction = RedactionDisclosed
		w.header.Disclosure = reason

		return nil
	}
}

// NewWriter writes the header and returns a writer for the records.
func NewWriter(sink io.Writer, header Header, options ...WriterOption) (*Writer, error) {
	if sink == nil {
		return nil, fmt.Errorf("%w: nil writer", ErrInvalidCapture)
	}
	if header.Protocol == "" {
		return nil, fmt.Errorf("%w: header names no protocol", ErrInvalidCapture)
	}
	if header.FrameBytes <= 0 {
		return nil, fmt.Errorf("%w: header declares no frame limit", ErrInvalidCapture)
	}

	header.Format = FormatVersion
	header.Redaction = RedactionEnforced
	header.Disclosure = ""

	writer := &Writer{
		sink:    sink,
		header:  header,
		strings: make(map[string]uint32),
		digest:  NewDigester(),
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil writer option", ErrInvalidCapture)
		}
		if err := option(writer); err != nil {
			return nil, err
		}
	}

	if err := writer.writeHeader(); err != nil {
		return nil, err
	}

	return writer, nil
}

func (w *Writer) writeHeader() error {
	encoded, err := json.Marshal(w.header)
	if err != nil {
		return fmt.Errorf("encode capture header: %w", err)
	}

	prefix := make([]byte, 0, len(Magic)+6)
	prefix = append(prefix, Magic...)
	prefix = binary.BigEndian.AppendUint16(prefix, uint16(FormatVersion))
	prefix = binary.BigEndian.AppendUint32(prefix, uint32(len(encoded)))

	if _, err := w.sink.Write(prefix); err != nil {
		return fmt.Errorf("write capture header: %w", err)
	}
	if _, err := w.sink.Write(encoded); err != nil {
		return fmt.Errorf("write capture header: %w", err)
	}

	return nil
}

// Header returns the header this writer wrote.
func (w *Writer) Header() Header { return w.header }

// Observe implements protocol.ObservationSink.
//
// A record is assembled in full and checked in full before any of it is
// written, so a refused record leaves no partial bytes behind.
func (w *Writer) Observe(_ context.Context, observation protocol.Observation) error {
	if w.closed {
		return fmt.Errorf("%w: writer is closed", ErrInvalidCapture)
	}

	kind, known := kindFor(observation.Stage)
	if !known {
		return fmt.Errorf("%w: unknown observation stage %q", ErrInvalidCapture, observation.Stage)
	}

	if kind == KindSecret && !observation.Redacted && !w.disclosing {
		return fmt.Errorf(
			"%w: a %q record carries material and this writer does not disclose",
			ErrUndisclosedSecret,
			observation.Stage,
		)
	}
	if len(observation.Bytes) > w.header.FrameBytes {
		return fmt.Errorf(
			"%w: %d bytes against a limit of %d",
			ErrRecordTooLarge,
			len(observation.Bytes),
			w.header.FrameBytes,
		)
	}

	record := w.buildRecord(kind, observation)
	if err := w.writeRecord(record); err != nil {
		return err
	}

	w.records++
	w.last = observation.Sequence
	if kind == KindRawFrame || kind == KindPacket {
		w.digest.Add(Record{
			Kind:      kind,
			Sequence:  observation.Sequence,
			Direction: observation.Direction,
			State:     observation.After.State,
			PacketID:  packetID(observation),
			Payload:   observation.Bytes,
		})
	}

	return nil
}

// buildRecord assembles one record body into the writer's scratch buffer.
//
// The string table is emitted inline: the first use of a string carries its
// bytes and takes the next index, and later uses carry the index alone. That
// keeps the writer streaming — there is no table to seek back and patch — and
// costs one length-prefixed copy per distinct string in the whole file.
func (w *Writer) buildRecord(kind Kind, observation protocol.Observation) []byte {
	body := w.body[:0]
	body = append(body, byte(kind))
	body = binary.BigEndian.AppendUint64(body, observation.Sequence)
	body = binary.BigEndian.AppendUint64(body, observation.Frame)
	body = binary.BigEndian.AppendUint64(body, uint64(observation.Elapsed.Nanoseconds()))
	body = append(body, byte(observation.Direction))

	var flags uint8
	if observation.Redacted {
		flags |= flagRedacted
	}
	body = append(body, flags)

	body = w.appendString(body, string(observation.Before.State))
	body = w.appendString(body, string(observation.After.State))
	body = binary.BigEndian.AppendUint32(body, uint32(observation.OriginalLen))

	switch kind {
	case KindPacket, KindRejected:
		body = binary.BigEndian.AppendUint32(body, uint32(packetID(observation)))
		body = w.appendString(body, packetName(observation))
	case KindSecret:
		body = w.appendString(body, secretLabel(observation))
	case KindRawFrame, KindTrailer:
	}
	if kind == KindRejected {
		body = w.appendString(body, rejectionReason(observation))
	}

	body = binary.BigEndian.AppendUint32(body, uint32(len(observation.Bytes)))
	body = append(body, observation.Bytes...)

	w.body = body

	return body
}

// appendString writes a string as an inline definition or a back reference.
func (w *Writer) appendString(body []byte, value string) []byte {
	if index, known := w.strings[value]; known {
		return binary.BigEndian.AppendUint32(body, index+1)
	}

	w.strings[value] = uint32(len(w.strings))
	body = binary.BigEndian.AppendUint32(body, 0)
	body = binary.BigEndian.AppendUint32(body, uint32(len(value)))

	return append(body, value...)
}

// writeRecord frames one assembled body: length, body, CRC over the body.
func (w *Writer) writeRecord(body []byte) error {
	w.scratch = binary.BigEndian.AppendUint32(w.scratch[:0], uint32(len(body)))
	if _, err := w.sink.Write(w.scratch); err != nil {
		return fmt.Errorf("write capture record: %w", err)
	}
	if _, err := w.sink.Write(body); err != nil {
		return fmt.Errorf("write capture record: %w", err)
	}

	w.scratch = binary.BigEndian.AppendUint32(w.scratch[:0], checksum(body))
	if _, err := w.sink.Write(w.scratch); err != nil {
		return fmt.Errorf("write capture record: %w", err)
	}

	return nil
}

// Close writes the trailer. It is idempotent, so a deferred Close beside an
// explicit one is not an error.
func (w *Writer) Close() error {
	if w.closed {
		return nil
	}
	w.closed = true

	body := w.body[:0]
	body = append(body, byte(KindTrailer))
	body = binary.BigEndian.AppendUint64(body, w.records)
	body = binary.BigEndian.AppendUint64(body, w.last)
	body = w.appendString(body, w.digest.Sum())
	w.body = body

	return w.writeRecord(body)
}

func checksum(body []byte) uint32 { return crc32.Checksum(body, crcTable) }

func packetID(observation protocol.Observation) int32 {
	if observation.Packet == nil {
		return 0
	}

	return observation.Packet.ID
}

func packetName(observation protocol.Observation) string {
	if observation.Packet == nil {
		return ""
	}

	return observation.Packet.Name
}

func secretLabel(observation protocol.Observation) string {
	if observation.Secret == nil {
		return ""
	}

	return observation.Secret.Label
}

func rejectionReason(observation protocol.Observation) string {
	if observation.Rejected == nil {
		return ""
	}

	return observation.Rejected.Reason
}

var _ protocol.ObservationSink = (*Writer)(nil)
