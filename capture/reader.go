package capture

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

// maxHeaderBytes bounds the JSON header. It is generous for a document of a
// dozen short fields and small enough that a corrupt length cannot ask for an
// allocation worth caring about.
const maxHeaderBytes = 1 << 20

// Reader streams records out of a capture.
//
// It reads forward only and holds one record at a time. A capture is often
// larger than memory and is written by a process that may be killed, so
// neither reading it whole nor trusting it to be complete is an option.
type Reader struct {
	source   *bufio.Reader
	header   Header
	strings  []string
	trailer  Trailer
	complete bool
	done     bool
	body     []byte
}

// NewReader reads and validates a capture header.
func NewReader(source io.Reader) (*Reader, error) {
	if source == nil {
		return nil, fmt.Errorf("%w: nil reader", ErrInvalidCapture)
	}

	reader := &Reader{source: bufio.NewReader(source)}
	if err := reader.readHeader(); err != nil {
		return nil, err
	}

	return reader, nil
}

func (r *Reader) readHeader() error {
	prefix := make([]byte, len(Magic)+6)
	if _, err := io.ReadFull(r.source, prefix); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%w: file ends inside the header", ErrTruncated)
		}

		return fmt.Errorf("read capture header: %w", err)
	}
	if string(prefix[:len(Magic)]) != Magic {
		return fmt.Errorf("%w: wrong magic", ErrNotACapture)
	}

	version := binary.BigEndian.Uint16(prefix[len(Magic):])
	if version != FormatVersion {
		return fmt.Errorf("%w: file is version %d, this reads version %d", ErrUnsupportedFormat, version, FormatVersion)
	}

	length := binary.BigEndian.Uint32(prefix[len(Magic)+2:])
	if length > maxHeaderBytes {
		return fmt.Errorf("%w: header declares %d bytes", ErrRecordTooLarge, length)
	}

	encoded := make([]byte, length)
	if _, err := io.ReadFull(r.source, encoded); err != nil {
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%w: header declares %d bytes and the file is shorter", ErrTruncated, length)
		}

		return fmt.Errorf("read capture header: %w", err)
	}
	if err := json.Unmarshal(encoded, &r.header); err != nil {
		return fmt.Errorf("%w: header is not readable JSON: %w", ErrNotACapture, err)
	}
	if r.header.FrameBytes <= 0 {
		return fmt.Errorf("%w: header declares no frame limit", ErrInvalidCapture)
	}

	return nil
}

// Header returns the capture's header.
func (r *Reader) Header() Header { return r.header }

// Complete reports whether the capture ended with a trailer. A capture written
// by a process that was killed reports false, and every record before the cut
// is still readable.
func (r *Reader) Complete() bool { return r.complete }

// Trailer returns the capture's totals, when it has them.
func (r *Reader) Trailer() (Trailer, bool) { return r.trailer, r.complete }

// Next returns the next record, or ErrEndOfCapture at the end.
//
// A short read at a record boundary is a clean end. A short read inside a
// record is ErrTruncated, and the records before it stay valid.
func (r *Reader) Next() (Record, error) {
	if r.done {
		return Record{}, ErrEndOfCapture
	}

	var lengthBytes [4]byte
	if _, err := io.ReadFull(r.source, lengthBytes[:]); err != nil {
		r.done = true
		if errors.Is(err, io.EOF) {
			return Record{}, ErrEndOfCapture
		}
		if errors.Is(err, io.ErrUnexpectedEOF) {
			return Record{}, fmt.Errorf("%w: file ends inside a record length", ErrTruncated)
		}

		return Record{}, fmt.Errorf("read capture record: %w", err)
	}

	length := binary.BigEndian.Uint32(lengthBytes[:])
	if int(length) > r.maxRecordBytes() {
		r.done = true

		return Record{}, fmt.Errorf(
			"%w: record declares %d bytes against a limit of %d",
			ErrRecordTooLarge, length, r.maxRecordBytes(),
		)
	}

	if cap(r.body) < int(length) {
		r.body = make([]byte, length)
	}
	body := r.body[:length]

	if _, err := io.ReadFull(r.source, body); err != nil {
		r.done = true
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Record{}, fmt.Errorf("%w: file ends inside a record body", ErrTruncated)
		}

		return Record{}, fmt.Errorf("read capture record: %w", err)
	}

	var checkBytes [4]byte
	if _, err := io.ReadFull(r.source, checkBytes[:]); err != nil {
		r.done = true
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return Record{}, fmt.Errorf("%w: file ends before a record checksum", ErrTruncated)
		}

		return Record{}, fmt.Errorf("read capture record: %w", err)
	}
	if want := binary.BigEndian.Uint32(checkBytes[:]); want != checksum(body) {
		r.done = true

		return Record{}, fmt.Errorf("%w: checksum mismatch", ErrCorruptRecord)
	}

	record, err := r.decode(body)
	if err != nil {
		r.done = true

		return Record{}, err
	}
	if record.Kind == KindTrailer {
		r.complete = true
		r.done = true

		return Record{}, ErrEndOfCapture
	}

	return record, nil
}

// maxRecordBytes bounds a record body against the header's own frame limit,
// plus room for the fixed fields and the strings a record can define.
func (r *Reader) maxRecordBytes() int {
	return r.header.FrameBytes + maxHeaderBytes
}

// cursor reads fields out of a record body, refusing to read past its end.
// Every read is bounds-checked, because the body came off a disk that may have
// been written by a process that died mid-record.
type cursor struct {
	body []byte
	at   int
}

func (c *cursor) take(count int) ([]byte, error) {
	if count < 0 || c.at+count > len(c.body) {
		return nil, fmt.Errorf("%w: record ends early", ErrCorruptRecord)
	}
	slice := c.body[c.at : c.at+count]
	c.at += count

	return slice, nil
}

func (c *cursor) uint8() (uint8, error) {
	slice, err := c.take(1)
	if err != nil {
		return 0, err
	}

	return slice[0], nil
}

func (c *cursor) uint32() (uint32, error) {
	slice, err := c.take(4)
	if err != nil {
		return 0, err
	}

	return binary.BigEndian.Uint32(slice), nil
}

func (c *cursor) uint64() (uint64, error) {
	slice, err := c.take(8)
	if err != nil {
		return 0, err
	}

	return binary.BigEndian.Uint64(slice), nil
}

// string resolves one string field against the table built so far, defining a
// new entry when the record carries one inline.
func (r *Reader) string(c *cursor) (string, error) {
	code, err := c.uint32()
	if err != nil {
		return "", err
	}
	if code > 0 {
		index := int(code - 1)
		if index >= len(r.strings) {
			return "", fmt.Errorf("%w: string reference %d is not defined", ErrCorruptRecord, index)
		}

		return r.strings[index], nil
	}

	length, err := c.uint32()
	if err != nil {
		return "", err
	}
	slice, err := c.take(int(length))
	if err != nil {
		return "", err
	}
	value := string(slice)
	r.strings = append(r.strings, value)

	return value, nil
}

func (r *Reader) decode(body []byte) (Record, error) {
	c := &cursor{body: body}

	rawKind, err := c.uint8()
	if err != nil {
		return Record{}, err
	}
	kind := Kind(rawKind)

	if kind == KindTrailer {
		return r.decodeTrailer(c)
	}

	var record Record
	record.Kind = kind

	if record.Sequence, err = c.uint64(); err != nil {
		return Record{}, err
	}
	if record.Frame, err = c.uint64(); err != nil {
		return Record{}, err
	}
	elapsed, err := c.uint64()
	if err != nil {
		return Record{}, err
	}
	record.Elapsed = time.Duration(elapsed)

	direction, err := c.uint8()
	if err != nil {
		return Record{}, err
	}
	record.Direction = protocol.Direction(direction)

	flags, err := c.uint8()
	if err != nil {
		return Record{}, err
	}
	record.Redacted = flags&flagRedacted != 0

	before, err := r.string(c)
	if err != nil {
		return Record{}, err
	}
	record.BeforeState = protocol.State(before)

	after, err := r.string(c)
	if err != nil {
		return Record{}, err
	}
	record.State = protocol.State(after)

	originalLen, err := c.uint32()
	if err != nil {
		return Record{}, err
	}
	record.OriginalLen = int(originalLen)

	switch kind {
	case KindPacket, KindRejected:
		id, err := c.uint32()
		if err != nil {
			return Record{}, err
		}
		record.PacketID = int32(id)

		if record.Name, err = r.string(c); err != nil {
			return Record{}, err
		}
	case KindSecret:
		if record.SecretLabel, err = r.string(c); err != nil {
			return Record{}, err
		}
	case KindRawFrame:
	case KindTrailer:
	default:
		return Record{}, fmt.Errorf("%w: unknown record kind %d", ErrCorruptRecord, kind)
	}

	if kind == KindRejected {
		if record.Reason, err = r.string(c); err != nil {
			return Record{}, err
		}
	}

	payloadLen, err := c.uint32()
	if err != nil {
		return Record{}, err
	}
	payload, err := c.take(int(payloadLen))
	if err != nil {
		return Record{}, err
	}
	// Owned: the reader reuses its body buffer for the next record.
	record.Payload = append([]byte(nil), payload...)

	return record, nil
}

func (r *Reader) decodeTrailer(c *cursor) (Record, error) {
	records, err := c.uint64()
	if err != nil {
		return Record{}, err
	}
	last, err := c.uint64()
	if err != nil {
		return Record{}, err
	}
	digest, err := r.string(c)
	if err != nil {
		return Record{}, err
	}

	r.trailer = Trailer{Records: records, LastSequence: last, Digest: digest}

	return Record{Kind: KindTrailer}, nil
}
