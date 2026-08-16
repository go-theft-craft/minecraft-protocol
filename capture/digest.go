package capture

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
)

// DigestVersion identifies what the digest is computed over. It is stored
// beside the digest so that a mismatch against a capture written under an
// older rule reads as "computed differently" rather than "the bytes changed".
const DigestVersion = 1

// Digester accumulates the replay digest of a capture.
//
// The digest exists to make determinism testable: replaying a capture twice
// must produce the same digest, and any difference in what crossed the wire
// must change it. It covers the records that describe the wire — raw frames
// and packets — and excludes rejected writes and the trailer, which describe
// the consumer and the file.
//
// Every field is length-prefixed before hashing, so no two different record
// sequences can produce the same input by running together at a boundary.
type Digester struct {
	hash hash.Hash
}

// NewDigester returns an empty digester.
func NewDigester() *Digester {
	digester := &Digester{hash: sha256.New()}
	digester.writeUint64(uint64(DigestVersion))

	return digester
}

// Add folds one record in. A record that did not cross the wire is ignored,
// so a caller may hand it every record without filtering first.
func (d *Digester) Add(record Record) {
	if !record.Replayable() {
		return
	}

	d.writeUint64(record.Sequence)
	d.writeUint64(uint64(record.Direction))
	d.writeString(string(record.State))
	d.writeUint64(uint64(uint32(record.PacketID)))

	// The payload enters as its own digest rather than inline, so one huge
	// frame cannot dominate the cost of hashing and the input stays fixed
	// width per record.
	sum := sha256.Sum256(record.Payload)
	d.writeUint64(uint64(len(record.Payload)))
	d.hash.Write(sum[:])
}

// Sum returns the digest as hex.
func (d *Digester) Sum() string { return hex.EncodeToString(d.hash.Sum(nil)) }

func (d *Digester) writeUint64(value uint64) {
	var buffer [8]byte
	binary.BigEndian.PutUint64(buffer[:], value)
	d.hash.Write(buffer[:])
}

func (d *Digester) writeString(value string) {
	d.writeUint64(uint64(len(value)))
	d.hash.Write([]byte(value))
}
