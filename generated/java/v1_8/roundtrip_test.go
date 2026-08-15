package v1_8

import (
	"bytes"
	"fmt"
	"sort"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

// TestProtocol47EveryPacketRoundTripsItsBytes pins the wire format of every
// packet in the protocol, not only the ones with a hand-written fixture.
//
// It works backwards from bytes rather than forwards from values. A decoder
// that accepts an input has already chosen every switch branch and every
// collection length consistently, so re-encoding the result must reproduce the
// bytes it consumed. Building a value first and encoding it would require the
// test to know which branch of each switch is live, which is exactly the
// knowledge the generator owns and the test should not duplicate.
//
// This is the contract M2.5 must not break: the generator changes completely,
// and every byte stays the same.
func TestProtocol47EveryPacketRoundTripsItsBytes(t *testing.T) {
	t.Parallel()

	limits := roundTripLimits(t)

	for _, key := range sortedPacketKeys() {
		name := packetNames[key]
		t.Run(fmt.Sprintf("%s/%d/%s", key.State, key.Direction, name), func(t *testing.T) {
			t.Parallel()

			if _, explicit := explicitlyPinned[name]; explicit {
				t.Skipf("%s is pinned by hand in TestProtocol47PinsPacketsAnInputStreamCannotReach", name)
			}
			if !roundTripPacket(t, key, limits) {
				t.Fatalf(
					"no candidate input decoded as %s; this packet is unpinned, "+
						"so add a case for it rather than leaving coverage to shrink",
					name,
				)
			}
		})
	}
}

// roundTripPacket tries candidate inputs until one decodes, then asserts the
// re-encoded bytes match the consumed prefix exactly. It reports whether any
// candidate decoded.
func roundTripPacket(t *testing.T, key packetKey, limits protocol.Limits) bool {
	t.Helper()

	decoded := false
	for attempt := range roundTripAttempts {
		input := candidateBytes(key, attempt)

		value, known := newPacket(key.State, key.Direction, key.ID)
		if !known {
			t.Fatalf("packet %s has a name but no value", packetNames[key])
		}

		reader, err := java.NewReadBuffer(input, limits)
		if err != nil {
			t.Fatalf("NewReadBuffer() error = %v", err)
		}
		if err := value.Decode(reader); err != nil {
			continue
		}
		consumed := input[:len(input)-reader.Remaining()]

		writer, err := java.NewWriteBuffer(limits)
		if err != nil {
			t.Fatalf("NewWriteBuffer() error = %v", err)
		}
		if err := value.Encode(writer); err != nil {
			t.Fatalf("re-encoding a decoded value failed: %v", err)
		}
		if got := writer.Bytes(); !bytes.Equal(got, consumed) {
			t.Fatalf("re-encoded %x, want the consumed bytes %x", got, consumed)
		}

		decoded = true
	}

	return decoded
}

// roundTripAttempts is how many candidate inputs each packet gets. Every one
// that decodes is checked, so a higher count is more coverage rather than an
// earlier exit.
const roundTripAttempts = 24

// candidateBytes builds a deterministic input for one packet and attempt.
//
// The alphabet is exactly {0x00, 0x01}. That is not laziness: it keeps every
// VarInt, collection count, and switch selector canonical, so a decoded value
// re-encodes to the bytes it came from. A wider alphabet fails for a reason
// that has nothing to do with the wire format -- a bool read from 0x03 writes
// back as 0x01 -- and that noise would hide a real difference. Realistic field
// values are covered by the hand-written byte vectors in codec_test.go and by
// the bit-layout assertions in wire/java.
func candidateBytes(key packetKey, attempt int) []byte {
	// A tiny deterministic generator, seeded by the packet's identity so a
	// failure is reproducible from the test name alone.
	state := uint64(attempt+1) * 0x9e3779b97f4a7c15
	for _, character := range string(key.State) + packetNames[key] {
		state = (state ^ uint64(character)) * 0x100000001b3
	}
	state ^= uint64(key.ID) * 0xff51afd7ed558ccd

	input := make([]byte, 512)
	for index := range input {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17

		input[index] = byte(state & 1)
	}

	return input
}

func sortedPacketKeys() []packetKey {
	keys := make([]packetKey, 0, len(packetNames))
	for key := range packetNames {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].State != keys[j].State {
			return keys[i].State < keys[j].State
		}
		if keys[i].Direction != keys[j].Direction {
			return keys[i].Direction < keys[j].Direction
		}

		return keys[i].ID < keys[j].ID
	})

	return keys
}

func roundTripLimits(t *testing.T) protocol.Limits {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("NewLimits() error = %v", err)
	}

	return limits
}

// explicitlyPinned are the packets a canonical byte stream cannot reach, so
// they carry hand-written wire bytes instead.
//
// Entity metadata terminates at 0x7f and NBT needs a structurally valid tag;
// neither appears in a stream of 0x00 and 0x01, so the generated candidates
// never decode these four. Listing them by name means adding a packet with the
// same shape fails the coverage check rather than silently going unpinned.
var explicitlyPinned = map[string]struct{}{
	"entity_metadata":     {},
	"named_entity_spawn":  {},
	"spawn_entity_living": {},
	"update_entity_nbt":   {},
}

// pinnedMetadataBytes is the metadata sequence every case below carries: a
// byte-typed entry at index 0, a short-typed entry at index 1, and the
// protocol 47 terminator. The header packs type<<5|key.
var pinnedMetadataBytes = []byte{0x00, 0x05, 0x21, 0x01, 0x02, 0x7f}

// pinnedMetadata is the same value for every packet that carries metadata,
// because the element type is now shared rather than generated per packet.
func pinnedMetadata() EntityMetadata {
	return EntityMetadata{
		{
			AnonymousBitField1: EntityMetadataItemAnonymousBitField1Bits{Type: 0, Key: 0},
			Value:              EntityMetadataItemValueSwitch{Case0: 5},
		},
		{
			AnonymousBitField1: EntityMetadataItemAnonymousBitField1Bits{Type: 1, Key: 1},
			Value:              EntityMetadataItemValueSwitch{Case1: 258},
		},
	}
}

func TestProtocol47PinsPacketsAnInputStreamCannotReach(t *testing.T) {
	t.Parallel()

	limits := roundTripLimits(t)

	emptyCompound, err := java.NewNBT([]byte{0x0a, 0x00, 0x00, 0x00}, limits)
	if err != nil {
		t.Fatalf("NewNBT() error = %v", err)
	}

	cases := []struct {
		name  string
		value packetCodec
		wire  []byte
	}{
		{
			name:  "entity_metadata",
			value: &PlayClientboundEntityMetadata{EntityID: 1, Metadata: pinnedMetadata()},
			wire:  append([]byte{0x01}, pinnedMetadataBytes...),
		},
		{
			name: "named_entity_spawn",
			value: &PlayClientboundNamedEntitySpawn{
				EntityID:    1,
				PlayerUUID:  java.UUID{0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10},
				X:           2,
				Y:           3,
				Z:           4,
				Yaw:         5,
				Pitch:       6,
				CurrentItem: 7,
				Metadata:    pinnedMetadata(),
			},
			wire: append([]byte{
				0x01,
				0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
				0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
				0x00, 0x00, 0x00, 0x02,
				0x00, 0x00, 0x00, 0x03,
				0x00, 0x00, 0x00, 0x04,
				0x05,
				0x06,
				0x00, 0x07,
			}, pinnedMetadataBytes...),
		},
		{
			name: "spawn_entity_living",
			value: &PlayClientboundSpawnEntityLiving{
				EntityID:  1,
				Type:      2,
				X:         3,
				Y:         4,
				Z:         5,
				Yaw:       6,
				Pitch:     7,
				HeadPitch: 8,
				VelocityX: 9,
				VelocityY: 10,
				VelocityZ: 11,
				Metadata:  pinnedMetadata(),
			},
			wire: append([]byte{
				0x01,
				0x02,
				0x00, 0x00, 0x00, 0x03,
				0x00, 0x00, 0x00, 0x04,
				0x00, 0x00, 0x00, 0x05,
				0x06,
				0x07,
				0x08,
				0x00, 0x09,
				0x00, 0x0a,
				0x00, 0x0b,
			}, pinnedMetadataBytes...),
		},
		{
			name:  "update_entity_nbt",
			value: &PlayClientboundUpdateEntityNBT{EntityID: 1, Tag: emptyCompound},
			wire:  []byte{0x01, 0x0a, 0x00, 0x00, 0x00},
		},
	}

	if len(cases) != len(explicitlyPinned) {
		t.Fatalf("%d cases for %d packets that need one", len(cases), len(explicitlyPinned))
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			if _, listed := explicitlyPinned[testCase.name]; !listed {
				t.Fatalf("%s is pinned here but not listed as needing it", testCase.name)
			}

			writer, err := java.NewWriteBuffer(limits)
			if err != nil {
				t.Fatalf("NewWriteBuffer() error = %v", err)
			}
			if err := testCase.value.Encode(writer); err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			if got := writer.Bytes(); !bytes.Equal(got, testCase.wire) {
				t.Fatalf("encoded %x, want %x", got, testCase.wire)
			}

			reader, err := java.NewReadBuffer(testCase.wire, limits)
			if err != nil {
				t.Fatalf("NewReadBuffer() error = %v", err)
			}
			if err := testCase.value.Decode(reader); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if err := reader.RequireEmpty(testCase.name); err != nil {
				t.Fatalf("decode left bytes unread: %v", err)
			}

			again, err := java.NewWriteBuffer(limits)
			if err != nil {
				t.Fatalf("NewWriteBuffer() error = %v", err)
			}
			if err := testCase.value.Encode(again); err != nil {
				t.Fatalf("re-encode error = %v", err)
			}
			if got := again.Bytes(); !bytes.Equal(got, testCase.wire) {
				t.Fatalf("re-encoded %x, want %x", got, testCase.wire)
			}
		})
	}
}

// TestGeneratedPositionBitLayoutIsPinnedByValue is the assertion that outlives
// the hand-written position codec.
//
// Protocol 47 packs x, y, z into 26, 12, and 26 bits, most significant first;
// protocol 775 packs x, z, y. Nothing about a round trip can tell the two
// apart, because both sides would be wrong together, so the expected bytes are
// computed by hand from the field widths.
func TestGeneratedPositionBitLayoutIsPinnedByValue(t *testing.T) {
	t.Parallel()

	limits := roundTripLimits(t)

	cases := []struct {
		name string
		x, z int32
		y    int16
		wire []byte
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

			position := Position{X: testCase.x, Y: testCase.y, Z: testCase.z}

			writer, err := java.NewWriteBuffer(limits)
			if err != nil {
				t.Fatalf("NewWriteBuffer() error = %v", err)
			}
			if err := position.Encode(writer); err != nil {
				t.Fatalf("Encode() error = %v", err)
			}
			if got := writer.Bytes(); !bytes.Equal(got, testCase.wire) {
				t.Fatalf("encoded %x, want %x", got, testCase.wire)
			}

			reader, err := java.NewReadBuffer(testCase.wire, limits)
			if err != nil {
				t.Fatalf("NewReadBuffer() error = %v", err)
			}
			var decoded Position
			if err := decoded.Decode(reader); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if decoded != position {
				t.Fatalf("decoded %+v, want %+v", decoded, position)
			}
		})
	}
}

// TestGeneratedMetadataTerminatorIsPinnedByValue states the sentinel outright.
// Protocol 47 ends metadata at 127 and protocol 775 at 255; the wrong one
// reads past the end of a packet and desynchronises the connection rather than
// failing to decode.
func TestGeneratedMetadataTerminatorIsPinnedByValue(t *testing.T) {
	t.Parallel()

	limits := roundTripLimits(t)

	writer, err := java.NewWriteBuffer(limits)
	if err != nil {
		t.Fatalf("NewWriteBuffer() error = %v", err)
	}
	metadata := pinnedMetadata()
	if err := metadata.Encode(writer); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}

	got := writer.Bytes()
	if !bytes.Equal(got, pinnedMetadataBytes) {
		t.Fatalf("encoded %x, want %x", got, pinnedMetadataBytes)
	}
	if got[len(got)-1] != 0x7f {
		t.Fatalf("terminator = %#x, want 0x7f", got[len(got)-1])
	}
}
