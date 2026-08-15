package java

import (
	"bytes"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
)

// TestPositionBitLayoutIsPinnedByValue records the protocol 47 packing as
// observed bytes rather than as agreement between the encoder and the decoder.
//
// Protocol 47 packs x, y, z; protocol 775 packs x, z, y. A generator change
// that swapped the two orders would leave every round-trip test passing and
// every real connection broken, because both sides of a round trip would be
// wrong in the same way. These expectations are hand-computed from the field
// widths — 26 bits, 12 bits, 26 bits, most significant first — so they fail
// when the order changes.
func TestPositionBitLayoutIsPinnedByValue(t *testing.T) {
	t.Parallel()

	limits := positionTestLimits(t)

	cases := []struct {
		name    string
		x, y, z int
		wire    []byte
	}{
		{name: "origin", x: 0, y: 0, z: 0, wire: []byte{0, 0, 0, 0, 0, 0, 0, 0}},
		{name: "one two three", x: 1, y: 2, z: 3, wire: []byte{0, 0, 0, 64, 8, 0, 0, 3}},
		{name: "negative x", x: -1, y: 2, z: 3, wire: []byte{255, 255, 255, 192, 8, 0, 0, 3}},
		{name: "negative y", x: 1, y: -2, z: 3, wire: []byte{0, 0, 0, 127, 248, 0, 0, 3}},
		{name: "negative z", x: 1, y: 2, z: -3, wire: []byte{0, 0, 0, 64, 11, 255, 255, 253}},
		{
			name: "maximum of every field",
			x:    33554431, y: 2047, z: 33554431,
			wire: []byte{127, 255, 255, 223, 253, 255, 255, 255},
		},
		{
			name: "minimum of every field",
			x:    -33554432, y: -2048, z: -33554432,
			wire: []byte{128, 0, 0, 32, 2, 0, 0, 0},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			writer, err := NewWriteBuffer(limits)
			if err != nil {
				t.Fatalf("NewWriteBuffer() error = %v", err)
			}
			position := Position{X: testCase.x, Y: testCase.y, Z: testCase.z}
			if err := writer.WritePosition("position", position); err != nil {
				t.Fatalf("WritePosition() error = %v", err)
			}
			if got := writer.Bytes(); !bytes.Equal(got, testCase.wire) {
				t.Fatalf("wrote %x, want %x", got, testCase.wire)
			}

			reader, err := NewReadBuffer(testCase.wire, limits)
			if err != nil {
				t.Fatalf("NewReadBuffer() error = %v", err)
			}
			decoded, err := reader.ReadPosition("position")
			if err != nil {
				t.Fatalf("ReadPosition() error = %v", err)
			}
			if decoded != position {
				t.Fatalf("read %+v, want %+v", decoded, position)
			}
		})
	}
}

// TestEntityMetadataSequenceIsPinnedByValue records the protocol 47 metadata
// framing, including the terminator.
//
// Protocol 47 terminates at 127 and protocol 775 at 255. A codec that used the
// wrong terminator would read past the end of one packet and into the next,
// which is a desynchronised connection rather than a decode error, so it is
// worth stating the byte outright.
func TestEntityMetadataSequenceIsPinnedByValue(t *testing.T) {
	t.Parallel()

	limits := positionTestLimits(t)

	metadata := EntityMetadata{
		{Index: 0, Type: MetadataByte, Value: int8(5)},
		{Index: 1, Type: MetadataShort, Value: int16(258)},
		{Index: 2, Type: MetadataInt, Value: int32(66051)},
	}

	// header = type<<5 | index, then the value, then the 0x7f terminator.
	want := []byte{
		0x00, 0x05,
		0x21, 0x01, 0x02,
		0x42, 0x00, 0x01, 0x02, 0x03,
		0x7f,
	}

	writer, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatalf("NewWriteBuffer() error = %v", err)
	}
	if err := writer.WriteEntityMetadata("metadata", metadata); err != nil {
		t.Fatalf("WriteEntityMetadata() error = %v", err)
	}
	if got := writer.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("wrote %x, want %x", got, want)
	}

	reader, err := NewReadBuffer(want, limits)
	if err != nil {
		t.Fatalf("NewReadBuffer() error = %v", err)
	}
	decoded, err := reader.ReadEntityMetadata("metadata")
	if err != nil {
		t.Fatalf("ReadEntityMetadata() error = %v", err)
	}
	if len(decoded) != len(metadata) {
		t.Fatalf("read %d entries, want %d", len(decoded), len(metadata))
	}
	for index, entry := range decoded {
		if entry != metadata[index] {
			t.Fatalf("entry %d = %+v, want %+v", index, entry, metadata[index])
		}
	}
	if reader.Remaining() != 0 {
		t.Fatalf("%d bytes left after the terminator", reader.Remaining())
	}
}

func positionTestLimits(t *testing.T) protocol.Limits {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("NewLimits() error = %v", err)
	}

	return limits
}
