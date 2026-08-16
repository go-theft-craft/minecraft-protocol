package capture_test

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"testing"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/capture"
)

func testHeader() capture.Header {
	return capture.Header{
		Protocol:          "java/26.1",
		Role:              "client",
		FrameBytes:        2 << 20,
		DecompressedBytes: 8 << 20,
		Note:              "format test",
	}
}

// observation builds one plausible record. The state and name repeat on
// purpose, so a test can check that the string table stores them once.
func observation(sequence uint64, payload []byte) protocol.Observation {
	return protocol.Observation{
		Sequence:    sequence,
		Frame:       sequence,
		Elapsed:     time.Duration(sequence) * time.Millisecond,
		Direction:   protocol.DirectionClientbound,
		Stage:       protocol.ObservationPacket,
		Before:      protocol.NewSnapshot(protocol.State("play"), nil),
		After:       protocol.NewSnapshot(protocol.State("play"), nil),
		Packet:      &protocol.PacketMetadata{State: "play", Direction: protocol.DirectionClientbound, ID: 0x21, Name: "keep_alive"},
		Bytes:       payload,
		OriginalLen: len(payload),
	}
}

// writeCapture writes count observations and returns the finished file.
func writeCapture(t *testing.T, count int, options ...capture.WriterOption) []byte {
	t.Helper()

	var buffer bytes.Buffer
	writer, err := capture.NewWriter(&buffer, testHeader(), options...)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	for sequence := 1; sequence <= count; sequence++ {
		payload := []byte{byte(sequence), 0xaa, 0xbb}
		if err := writer.Observe(t.Context(), observation(uint64(sequence), payload)); err != nil {
			t.Fatalf("Observe(%d): %v", sequence, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	return buffer.Bytes()
}

func readAll(t *testing.T, file []byte) (*capture.Reader, []capture.Record) {
	t.Helper()

	reader, err := capture.NewReader(bytes.NewReader(file))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	var records []capture.Record
	for {
		record, err := reader.Next()
		if errors.Is(err, capture.ErrEndOfCapture) {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		records = append(records, record)
	}

	return reader, records
}

func TestHeaderRoundTrips(t *testing.T) {
	t.Parallel()

	reader, _ := readAll(t, writeCapture(t, 1))

	got := reader.Header()
	want := testHeader()
	if got.Protocol != want.Protocol || got.Role != want.Role || got.Note != want.Note {
		t.Fatalf("header = %+v, want the one written", got)
	}
	if got.FrameBytes != want.FrameBytes || got.DecompressedBytes != want.DecompressedBytes {
		t.Fatalf("header limits = %d/%d, want %d/%d",
			got.FrameBytes, got.DecompressedBytes, want.FrameBytes, want.DecompressedBytes)
	}
	if got.Redaction != capture.RedactionEnforced {
		t.Fatalf("redaction = %q, want %q", got.Redaction, capture.RedactionEnforced)
	}
	if got.Format != capture.FormatVersion {
		t.Fatalf("format = %d, want %d", got.Format, capture.FormatVersion)
	}
}

func TestABadMagicIsNamed(t *testing.T) {
	t.Parallel()

	file := writeCapture(t, 1)
	file[0] = 'X'

	_, err := capture.NewReader(bytes.NewReader(file))
	if !errors.Is(err, capture.ErrNotACapture) {
		t.Fatalf("NewReader error = %v, want ErrNotACapture", err)
	}
}

func TestAnUnknownFormatVersionIsNamed(t *testing.T) {
	t.Parallel()

	file := writeCapture(t, 1)
	binary.BigEndian.PutUint16(file[len(capture.Magic):], 0xffff)

	_, err := capture.NewReader(bytes.NewReader(file))
	if !errors.Is(err, capture.ErrUnsupportedFormat) {
		t.Fatalf("NewReader error = %v, want ErrUnsupportedFormat", err)
	}
}

func TestAHeaderLengthBeyondTheFileIsNamed(t *testing.T) {
	t.Parallel()

	file := writeCapture(t, 1)
	offset := len(capture.Magic) + 2
	binary.BigEndian.PutUint32(file[offset:], 1<<20)

	_, err := capture.NewReader(bytes.NewReader(file))
	if !errors.Is(err, capture.ErrTruncated) {
		t.Fatalf("NewReader error = %v, want ErrTruncated", err)
	}
}

func TestAHundredObservationsRoundTrip(t *testing.T) {
	t.Parallel()

	const count = 100
	reader, records := readAll(t, writeCapture(t, count))

	if len(records) != count {
		t.Fatalf("read %d records, want %d", len(records), count)
	}
	for index, record := range records {
		sequence := uint64(index + 1)
		if record.Sequence != sequence || record.Frame != sequence {
			t.Fatalf("record %d has sequence %d frame %d", index, record.Sequence, record.Frame)
		}
		if record.Elapsed != time.Duration(sequence)*time.Millisecond {
			t.Fatalf("record %d has Elapsed %v", index, record.Elapsed)
		}
		if record.Direction != protocol.DirectionClientbound {
			t.Fatalf("record %d has direction %d", index, record.Direction)
		}
		if record.State != protocol.State("play") || record.Name != "keep_alive" || record.PacketID != 0x21 {
			t.Fatalf("record %d = %+v, want the packet identity written", index, record)
		}
		if want := []byte{byte(sequence), 0xaa, 0xbb}; !bytes.Equal(record.Payload, want) {
			t.Fatalf("record %d payload = %x, want %x", index, record.Payload, want)
		}
	}

	if !reader.Complete() {
		t.Fatal("a capture that was closed must read as complete")
	}
	trailer, ok := reader.Trailer()
	if !ok {
		t.Fatal("a complete capture must carry a trailer")
	}
	if trailer.Records != count || trailer.LastSequence != count {
		t.Fatalf("trailer = %+v, want %d records ending at %d", trailer, count, count)
	}
}

func TestRepeatedStringsAreStoredOnce(t *testing.T) {
	t.Parallel()

	file := writeCapture(t, 50)

	// Fifty records name the same state and the same packet. Each string's
	// bytes belong in the file once; every later use is a four-byte
	// reference.
	if count := bytes.Count(file, []byte("keep_alive")); count != 1 {
		t.Fatalf("the packet name appears %d times, want it stored once", count)
	}
	if count := bytes.Count(file, []byte("play")); count != 1 {
		t.Fatalf("the state name appears %d times, want it stored once", count)
	}

	// And the per-record cost is then the fixed part alone, which is what
	// makes the table worth having.
	one := writeCapture(t, 1)
	perRecord := float64(len(file)-len(one)) / 49
	if perRecord > 70 {
		t.Fatalf("each repeated record costs %.1f bytes, want only the fixed fields", perRecord)
	}
}

func TestAFlippedCRCIsReported(t *testing.T) {
	t.Parallel()

	file := writeCapture(t, 3)

	// Flip a byte inside the last record's body. The trailing bytes are the
	// trailer, so walk back from the end far enough to land in a record.
	file[len(file)/2] ^= 0xff

	reader, err := capture.NewReader(bytes.NewReader(file))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	var corrupt error
	for {
		_, err := reader.Next()
		if err == nil {
			continue
		}
		corrupt = err

		break
	}
	if !errors.Is(corrupt, capture.ErrCorruptRecord) && !errors.Is(corrupt, capture.ErrTruncated) {
		t.Fatalf("reading a corrupted capture returned %v, want ErrCorruptRecord or ErrTruncated", corrupt)
	}
}

// TestTruncationAtEveryOffsetIsSafe is the test the format exists to survive.
// A capture is written to a file that a process may be killed in the middle
// of, so every prefix of one is a real input.
func TestTruncationAtEveryOffsetIsSafe(t *testing.T) {
	t.Parallel()

	file := writeCapture(t, 20)

	for offset := range len(file) {
		truncated := file[:offset]

		reader, err := capture.NewReader(bytes.NewReader(truncated))
		if err != nil {
			// A header that is not all there is the only construction
			// failure a prefix can produce.
			if !errors.Is(err, capture.ErrTruncated) && !errors.Is(err, capture.ErrNotACapture) {
				t.Fatalf("offset %d: NewReader error = %v", offset, err)
			}

			continue
		}

		var last uint64
		for {
			record, err := reader.Next()
			if err != nil {
				if !errors.Is(err, capture.ErrEndOfCapture) && !errors.Is(err, capture.ErrTruncated) {
					t.Fatalf("offset %d: Next error = %v", offset, err)
				}

				break
			}
			if record.Sequence <= last {
				t.Fatalf("offset %d: sequence went from %d to %d", offset, last, record.Sequence)
			}
			last = record.Sequence
		}

		if offset < len(file) && reader.Complete() {
			t.Fatalf("offset %d: a truncated capture read as complete", offset)
		}
	}
}

func TestAPayloadBeyondTheHeaderLimitIsRejected(t *testing.T) {
	t.Parallel()

	header := testHeader()
	header.FrameBytes = 8

	var buffer bytes.Buffer
	writer, err := capture.NewWriter(&buffer, header)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	oversize := observation(1, bytes.Repeat([]byte{0x01}, 64))
	if err := writer.Observe(t.Context(), oversize); !errors.Is(err, capture.ErrRecordTooLarge) {
		t.Fatalf("Observe error = %v, want ErrRecordTooLarge", err)
	}
}

func TestARedactedRecordStoresItsSizeAndNoBytes(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	writer, err := capture.NewWriter(&buffer, testHeader())
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	withheld := observation(1, nil)
	withheld.Redacted = true
	withheld.OriginalLen = 512

	if err := writer.Observe(t.Context(), withheld); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, records := readAll(t, buffer.Bytes())
	if len(records) != 1 {
		t.Fatalf("read %d records, want 1", len(records))
	}
	if !records[0].Redacted {
		t.Fatal("the redacted flag did not survive the round trip")
	}
	if len(records[0].Payload) != 0 {
		t.Fatalf("a redacted record carries %d payload bytes", len(records[0].Payload))
	}
	if records[0].OriginalLen != 512 {
		t.Fatalf("OriginalLen = %d, want 512", records[0].OriginalLen)
	}
}

func TestAnUndisclosedSecretIsRefused(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	writer, err := capture.NewWriter(&buffer, testHeader())
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	disclosed := protocol.Observation{
		Sequence:  1,
		Stage:     protocol.ObservationSecret,
		Direction: protocol.DirectionServerbound,
		Secret:    &protocol.SecretMetadata{Label: "java.session-key"},
		Bytes:     []byte("0123456789abcdef"),
	}

	err = writer.Observe(t.Context(), disclosed)
	if !errors.Is(err, capture.ErrUndisclosedSecret) {
		t.Fatalf("Observe error = %v, want ErrUndisclosedSecret", err)
	}

	// Nothing of the record may have been written.
	if bytes.Contains(buffer.Bytes(), []byte("0123456789abcdef")) {
		t.Fatal("the refused record reached the file anyway")
	}
}

func TestADisclosingWriterStatesItInTheHeader(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	writer, err := capture.NewWriter(
		&buffer,
		testHeader(),
		capture.WithDisclosure("interoperability debugging"),
	)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	disclosed := protocol.Observation{
		Sequence:    1,
		Stage:       protocol.ObservationSecret,
		Direction:   protocol.DirectionServerbound,
		Secret:      &protocol.SecretMetadata{Label: "java.session-key"},
		Bytes:       []byte("0123456789abcdef"),
		OriginalLen: 16,
	}
	if err := writer.Observe(t.Context(), disclosed); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reader, records := readAll(t, buffer.Bytes())
	if reader.Header().Redaction != capture.RedactionDisclosed {
		t.Fatalf("redaction = %q, want %q", reader.Header().Redaction, capture.RedactionDisclosed)
	}
	if reader.Header().Disclosure == "" {
		t.Fatal("a disclosing capture must record why")
	}
	if len(records) != 1 || records[0].SecretLabel != "java.session-key" {
		t.Fatalf("records = %+v, want the secret record with its label", records)
	}
}

func TestADisclosureReasonIsRequired(t *testing.T) {
	t.Parallel()

	_, err := capture.NewWriter(&bytes.Buffer{}, testHeader(), capture.WithDisclosure(""))
	if !errors.Is(err, capture.ErrInvalidCapture) {
		t.Fatalf("NewWriter error = %v, want ErrInvalidCapture", err)
	}
}

func TestARejectedRecordKeepsItsReason(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	writer, err := capture.NewWriter(&buffer, testHeader())
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	rejected := protocol.Observation{
		Sequence:  1,
		Stage:     protocol.ObservationRejected,
		Direction: protocol.DirectionServerbound,
		Packet:    &protocol.PacketMetadata{State: "play", ID: 3, Name: "chat"},
		Rejected:  &protocol.RejectionMetadata{Reason: "encode failed"},
	}
	if err := writer.Observe(t.Context(), rejected); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	_, records := readAll(t, buffer.Bytes())
	if len(records) != 1 || records[0].Kind != capture.KindRejected {
		t.Fatalf("records = %+v, want one rejected record", records)
	}
	if records[0].Reason != "encode failed" {
		t.Fatalf("reason = %q, want the reason written", records[0].Reason)
	}
}

func TestACaptureKilledWithoutCloseReadsUpToItsLastRecord(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	writer, err := capture.NewWriter(&buffer, testHeader())
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for sequence := 1; sequence <= 5; sequence++ {
		if err := writer.Observe(t.Context(), observation(uint64(sequence), []byte{0x01})); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	// No Close: the process died.

	reader, err := capture.NewReader(bytes.NewReader(buffer.Bytes()))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	var count int
	for {
		if _, err := reader.Next(); err != nil {
			if !errors.Is(err, capture.ErrEndOfCapture) {
				t.Fatalf("Next: %v", err)
			}

			break
		}
		count++
	}
	if count != 5 {
		t.Fatalf("read %d records, want all 5 that were written", count)
	}
	if reader.Complete() {
		t.Fatal("a capture with no trailer must not read as complete")
	}
}

func TestWriterRejectsAHeaderWithoutAProtocol(t *testing.T) {
	t.Parallel()

	header := testHeader()
	header.Protocol = ""

	if _, err := capture.NewWriter(&bytes.Buffer{}, header); !errors.Is(err, capture.ErrInvalidCapture) {
		t.Fatalf("NewWriter error = %v, want ErrInvalidCapture", err)
	}
}

func TestWriterIsAnObservationSink(t *testing.T) {
	t.Parallel()

	var sink protocol.ObservationSink
	writer, err := capture.NewWriter(&bytes.Buffer{}, testHeader())
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	sink = writer

	if err := sink.Observe(context.Background(), observation(1, []byte{0x01})); err != nil {
		t.Fatalf("Observe: %v", err)
	}
}
