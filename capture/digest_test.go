package capture_test

import (
	"bytes"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/capture"
)

// digestFixture is the record sequence every digest test starts from.
func digestFixture() []capture.Record {
	return []capture.Record{
		{
			Kind:      capture.KindRawFrame,
			Sequence:  1,
			Direction: protocol.DirectionClientbound,
			State:     protocol.State("login"),
			Payload:   []byte{0x00, 0x01, 0x02},
		},
		{
			Kind:      capture.KindPacket,
			Sequence:  2,
			Direction: protocol.DirectionClientbound,
			State:     protocol.State("login"),
			PacketID:  0x02,
			Payload:   []byte{0xaa, 0xbb},
		},
		{
			Kind:      capture.KindPacket,
			Sequence:  3,
			Direction: protocol.DirectionServerbound,
			State:     protocol.State("play"),
			PacketID:  0x11,
			Payload:   nil,
		},
	}
}

func digestOf(records []capture.Record) string {
	digester := capture.NewDigester()
	for _, record := range records {
		digester.Add(record)
	}

	return digester.Sum()
}

// TestTheDigestOfAFixedSequenceIsPinned is the point of checking a literal in.
// The digest is a promise that two runs over the same bytes agree, and a
// change to what goes into it silently breaks that promise for every capture
// already written. Changing this constant is that decision, made on purpose.
func TestTheDigestOfAFixedSequenceIsPinned(t *testing.T) {
	t.Parallel()

	const want = "1acf554075fc09e77d0cb4a808a7e09c94d0d96931ca57d29f0fafd0ba64b1c1"

	if got := digestOf(digestFixture()); got != want {
		t.Fatalf("digest = %q, want %q", got, want)
	}
}

func TestReorderingRecordsChangesTheDigest(t *testing.T) {
	t.Parallel()

	records := digestFixture()
	reordered := []capture.Record{records[1], records[0], records[2]}

	if digestOf(reordered) == digestOf(records) {
		t.Fatal("two orderings of the same records digest the same")
	}
}

func TestFlippingOnePayloadByteChangesTheDigest(t *testing.T) {
	t.Parallel()

	records := digestFixture()
	before := digestOf(records)

	mutated := digestFixture()
	mutated[0].Payload = bytes.Clone(mutated[0].Payload)
	mutated[0].Payload[1] ^= 0xff

	if digestOf(mutated) == before {
		t.Fatal("a changed payload byte did not change the digest")
	}
}

// TestRecordsThatDidNotCrossTheWireAreExcluded keeps the digest a statement
// about the connection. A rejected write happened inside one process, so two
// captures that differ only there describe the same connection.
func TestRecordsThatDidNotCrossTheWireAreExcluded(t *testing.T) {
	t.Parallel()

	records := digestFixture()
	withRejection := append(digestFixture(), capture.Record{
		Kind:      capture.KindRejected,
		Sequence:  4,
		Direction: protocol.DirectionServerbound,
		State:     protocol.State("play"),
		Reason:    "encode failed",
	})

	if digestOf(withRejection) != digestOf(records) {
		t.Fatal("a rejected write changed the digest of the connection")
	}
}

func TestACaptureCarriesItsOwnDigestInTheTrailer(t *testing.T) {
	t.Parallel()

	reader, records := readAll(t, writeCapture(t, 12))

	trailer, ok := reader.Trailer()
	if !ok {
		t.Fatal("a closed capture must carry a trailer")
	}
	if !trailer.Comparable() {
		t.Fatalf("trailer digest %+v cannot be compared with a freshly computed one", trailer)
	}

	if got := digestOf(records); got != trailer.Digest {
		t.Fatalf("recomputed digest %q, trailer holds %q", got, trailer.Digest)
	}
}

func TestATrailerFromAnotherDigestVersionIsNotCompared(t *testing.T) {
	t.Parallel()

	trailer := capture.Trailer{Digest: "abc", DigestAlgorithm: capture.DigestVersion + 1}
	if trailer.Comparable() {
		t.Fatal("a digest computed under another rule reported itself as comparable")
	}

	empty := capture.Trailer{DigestAlgorithm: capture.DigestVersion}
	if empty.Comparable() {
		t.Fatal("an absent digest reported itself as comparable")
	}
}
