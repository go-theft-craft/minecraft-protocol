// Package capture writes and reads a durable record of one connection.
//
// A capture is a JSON header followed by length-prefixed, CRC-checked binary
// records. The header is JSON so that a person or a tool can read what a file
// is without knowing the record encoding; the records are binary because there
// are hundreds of thousands of them and they hold arbitrary bytes.
//
// # What a capture holds
//
// Session content. A capture of a real connection contains chat, positions,
// and everything else the peers exchanged, and it is not encrypted. Secret
// material is withheld unless a writer was explicitly constructed to disclose
// it, and a writer refuses to store an undisclosed secret rather than trusting
// its caller to have redacted one.
//
// # Truncation
//
// Every prefix of a capture is a valid input, because the process writing one
// can be killed at any byte. A reader reports a clean end at a record
// boundary, ErrTruncated inside a record, and never allocates on a length it
// has not first checked against the header's own limits.
package capture

import (
	"errors"
	"hash/crc32"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

// Magic opens every capture file.
const Magic = "MCPROCAP"

// FormatVersion is the record layout this package writes. A reader refuses a
// version it does not know rather than guessing at a layout.
const FormatVersion = 1

// Redaction modes named in a header.
const (
	// RedactionEnforced means no secret material was written.
	RedactionEnforced = "enforced"
	// RedactionDisclosed means the writer was constructed to store it, and
	// Header.Disclosure says why.
	RedactionDisclosed = "disclosed"
)

var (
	// ErrNotACapture reports a file that does not begin with the magic.
	ErrNotACapture = errors.New("not a capture file")
	// ErrUnsupportedFormat reports a format version this package cannot read.
	ErrUnsupportedFormat = errors.New("unsupported capture format")
	// ErrTruncated reports a capture that ends inside a header or a record.
	ErrTruncated = errors.New("capture is truncated")
	// ErrCorruptRecord reports a record whose CRC does not match its body.
	ErrCorruptRecord = errors.New("corrupt capture record")
	// ErrRecordTooLarge reports a payload beyond the header's declared limit.
	// It is checked before allocation, so a corrupt length cannot ask a
	// reader for a gigabyte.
	ErrRecordTooLarge = errors.New("capture record exceeds the declared limit")
	// ErrUndisclosedSecret reports an attempt to store secret material
	// through a writer that was not constructed to disclose it.
	ErrUndisclosedSecret = errors.New("refusing to write an undisclosed secret")
	// ErrInvalidCapture reports a writer or reader that cannot be built.
	ErrInvalidCapture = errors.New("invalid capture")
	// ErrEndOfCapture reports that there are no more records. It is returned
	// at a clean record boundary, whether or not a trailer followed.
	ErrEndOfCapture = errors.New("end of capture")
)

// Kind names what one record describes. The values are part of the file
// format and never change meaning.
type Kind uint8

const (
	// KindTrailer closes a capture and carries its totals.
	KindTrailer Kind = 0
	// KindRawFrame is one complete wire frame.
	KindRawFrame Kind = 1
	// KindPacket is one decoded packet body.
	KindPacket Kind = 2
	// KindSecret marks where secret material was installed. It carries the
	// material only in a disclosed capture.
	KindSecret Kind = 3
	// KindRejected is a write that never reached the transport. It describes
	// the consumer rather than the connection, so replay skips it.
	KindRejected Kind = 4
)

// record flag bits.
const flagRedacted uint8 = 1 << 0

// Header describes a whole capture. It is stored as JSON so that a reader can
// learn a file's protocol, limits, and redaction mode without decoding a
// single record.
type Header struct {
	// Format is the record layout version. NewWriter sets it.
	Format int `json:"format"`
	// Protocol is the protocol ID a replay resolves, such as "java/26.1".
	Protocol string `json:"protocol"`
	// Role is the endpoint the capture was taken from: "client" or "server".
	Role string `json:"role,omitempty"`
	// Redaction is RedactionEnforced or RedactionDisclosed.
	Redaction string `json:"redaction"`
	// Disclosure is the stated reason a disclosing capture exists. It is
	// required when Redaction is RedactionDisclosed and absent otherwise.
	Disclosure string `json:"disclosure,omitempty"`
	// FrameBytes and DecompressedBytes are the limits in force while the
	// capture was taken. A reader uses FrameBytes as its allocation bound.
	FrameBytes        int `json:"frameBytes"`
	DecompressedBytes int `json:"decompressedBytes"`
	// Created is when the capture began, in RFC 3339.
	Created string `json:"created,omitempty"`
	// Note is free text for whoever takes the capture.
	Note string `json:"note,omitempty"`
}

// Trailer closes a capture. A file without one was not closed, which is not an
// error: it is what a capture of a process that was killed looks like.
type Trailer struct {
	Records      uint64
	LastSequence uint64
	// Digest is the replay digest over every replayable record.
	Digest string
	// DigestAlgorithm is the rule the digest was computed under. A capture
	// written under a different rule is reported rather than compared: an
	// unequal digest computed differently says nothing about whether the
	// bytes changed, which is the only question a digest is asked.
	DigestAlgorithm int
}

// Comparable reports whether this trailer's digest can be compared with one
// this package computes.
func (t Trailer) Comparable() bool {
	return t.Digest != "" && t.DigestAlgorithm == DigestVersion
}

// Record is one decoded capture entry.
//
// It is deliberately flat. A capture is read by tools that filter and print,
// and a shape with one field per thing a record can say is easier to filter
// than a union of stage-specific structs.
type Record struct {
	Kind      Kind
	Sequence  uint64
	Frame     uint64
	Elapsed   time.Duration
	Direction protocol.Direction
	// State is the session state after this record's commit point, and
	// BeforeState the one before it. They differ only where a record changed
	// the state, which is what lets a replay apply the recorded transition
	// rather than infer one.
	BeforeState protocol.State
	State       protocol.State
	// PacketID and Name identify a packet record. Name is empty for a packet
	// the capturing session could not name.
	PacketID int32
	Name     string
	// Redacted reports a body the capture withheld; OriginalLen is how many
	// bytes it withheld.
	Redacted    bool
	OriginalLen int
	Payload     []byte
	// SecretLabel names the material a KindSecret record describes.
	SecretLabel string
	// Reason says why a KindRejected write stopped.
	Reason string
}

// Replayable reports whether a record describes something that crossed the
// wire. A rejected write did not, and a trailer is bookkeeping, so neither is
// replayed and neither enters the digest.
func (r Record) Replayable() bool {
	return r.Kind == KindRawFrame || r.Kind == KindPacket
}

// crcTable is Castagnoli, which has hardware support on the architectures this
// runs on and better error detection than IEEE at these record sizes.
var crcTable = crc32.MakeTable(crc32.Castagnoli)

// kindFor maps an observation stage to its record kind.
func kindFor(stage protocol.ObservationStage) (Kind, bool) {
	switch stage {
	case protocol.ObservationRawFrame:
		return KindRawFrame, true
	case protocol.ObservationPacket:
		return KindPacket, true
	case protocol.ObservationSecret:
		return KindSecret, true
	case protocol.ObservationRejected:
		return KindRejected, true
	default:
		return 0, false
	}
}
