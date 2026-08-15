package java

import (
	"bytes"
	"math"
	"testing"
)

// TestLPVec3EncodesZeroAsOneByte pins the special case. A vector whose largest
// component is below the minimum magnitude is a single zero byte, not six.
func TestLPVec3EncodesZeroAsOneByte(t *testing.T) {
	limits := bufferLimits(t)

	writer, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteLPVec3("vec", LPVec3{}); err != nil {
		t.Fatalf("WriteLPVec3: %v", err)
	}
	if got := writer.Bytes(); !bytes.Equal(got, []byte{0x00}) {
		t.Fatalf("encoded % x, want 00", got)
	}

	reader, err := NewReadBuffer([]byte{0x00}, limits)
	if err != nil {
		t.Fatal(err)
	}
	value, err := reader.ReadLPVec3("vec")
	if err != nil {
		t.Fatalf("ReadLPVec3: %v", err)
	}
	if value != (LPVec3{}) {
		t.Errorf("value = %+v, want zero", value)
	}
	if reader.Remaining() != 0 {
		t.Errorf("%d bytes left unread", reader.Remaining())
	}
}

// TestLPVec3RoundTripsBytes is the assertion that actually constrains the
// layout. The encoding is lossy, so a float round-trip proves little; encoding
// what was decoded and getting the same bytes back proves the bit positions,
// the little-endian packing, and the scale handling together.
func TestLPVec3RoundTripsBytes(t *testing.T) {
	limits := bufferLimits(t)

	cases := []struct {
		name    string
		encoded []byte
	}{
		{
			// Scale 1, no continuation: flags 0x01 in the low three bits.
			name:    "a unit-scale vector",
			encoded: []byte{0x01, 0x00, 0x00, 0x00, 0x00, 0x00},
		},
		{
			name:    "components spread across all three fields",
			encoded: []byte{0x01, 0x34, 0x12, 0x78, 0x56, 0x9A},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reader, err := NewReadBuffer(tc.encoded, limits)
			if err != nil {
				t.Fatal(err)
			}
			value, err := reader.ReadLPVec3("vec")
			if err != nil {
				t.Fatalf("ReadLPVec3: %v", err)
			}
			if reader.Remaining() != 0 {
				t.Errorf("%d bytes left unread", reader.Remaining())
			}

			writer, err := NewWriteBuffer(limits)
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.WriteLPVec3("vec", value); err != nil {
				t.Fatalf("WriteLPVec3: %v", err)
			}
			if got := writer.Bytes(); !bytes.Equal(got, tc.encoded) {
				t.Errorf("re-encoded % x, want % x", got, tc.encoded)
			}
		})
	}
}

// TestLPVec3UsesAContinuationForALargeScale covers the seven-byte form.
//
// It encodes first rather than asserting a hand-written fixture, because the
// scale is not free: an encoder always writes ceil(max), so a byte string
// carrying any other scale is non-canonical and will not survive a re-encode.
// That is a property of the format, not a defect, and pinning it here keeps
// the next reader from writing the fixture I first wrote.
func TestLPVec3UsesAContinuationForALargeScale(t *testing.T) {
	limits := bufferLimits(t)

	// Scale 13: two bits cannot hold it, so 13 & 3 goes in the first byte with
	// the continuation bit, and 13 / 4 follows as a VarInt.
	value := LPVec3{X: 13, Y: -6.5, Z: 3.25}

	writer, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteLPVec3("vec", value); err != nil {
		t.Fatalf("WriteLPVec3: %v", err)
	}

	encoded := writer.Bytes()
	if len(encoded) != 7 {
		t.Fatalf("encoded %d bytes, want 7", len(encoded))
	}
	if encoded[0]&0x04 != 0x04 {
		t.Errorf("first byte %#02x does not set the continuation bit", encoded[0])
	}
	if encoded[0]&0x03 != 13&0x03 {
		t.Errorf("first byte %#02x carries the wrong low scale bits", encoded[0])
	}
	if encoded[6] != 13/4 {
		t.Errorf("continuation VarInt = %d, want %d", encoded[6], 13/4)
	}

	reader, err := NewReadBuffer(encoded, limits)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := reader.ReadLPVec3("vec")
	if err != nil {
		t.Fatalf("ReadLPVec3: %v", err)
	}
	if reader.Remaining() != 0 {
		t.Errorf("%d bytes left unread", reader.Remaining())
	}

	again, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.WriteLPVec3("vec", decoded); err != nil {
		t.Fatalf("WriteLPVec3: %v", err)
	}
	if got := again.Bytes(); !bytes.Equal(got, encoded) {
		t.Errorf("re-encoded % x, want % x", got, encoded)
	}
}

// TestLPVec3PreservesComponentsWithinQuantizationError states the accuracy
// claim explicitly rather than leaving it implied. Fifteen bits across a range
// of twice the scale is roughly scale/16384 per step.
func TestLPVec3PreservesComponentsWithinQuantizationError(t *testing.T) {
	limits := bufferLimits(t)

	cases := []LPVec3{
		{X: 1, Y: 0, Z: 0},
		{X: -1, Y: 0.5, Z: 0.25},
		{X: 0.1, Y: -0.9, Z: 0.75},
		{X: 3.5, Y: -2.25, Z: 1.125},
	}

	for _, want := range cases {
		writer, err := NewWriteBuffer(limits)
		if err != nil {
			t.Fatal(err)
		}
		if err := writer.WriteLPVec3("vec", want); err != nil {
			t.Fatalf("WriteLPVec3(%+v): %v", want, err)
		}

		reader, err := NewReadBuffer(writer.Bytes(), limits)
		if err != nil {
			t.Fatal(err)
		}
		got, err := reader.ReadLPVec3("vec")
		if err != nil {
			t.Fatalf("ReadLPVec3(%+v): %v", want, err)
		}

		scale := math.Ceil(math.Max(math.Abs(want.X), math.Max(math.Abs(want.Y), math.Abs(want.Z))))
		tolerance := scale / 16384.0
		for _, component := range []struct {
			axis string
			want float64
			got  float64
		}{
			{"x", want.X, got.X},
			{"y", want.Y, got.Y},
			{"z", want.Z, got.Z},
		} {
			if math.Abs(component.want-component.got) > tolerance {
				t.Errorf("%+v: %s = %v, want %v within %v", want, component.axis, component.got, component.want, tolerance)
			}
		}
	}
}

func TestLPVec3RejectsATruncatedPayload(t *testing.T) {
	limits := bufferLimits(t)

	// A non-zero first byte promises six bytes; only four follow.
	reader, err := NewReadBuffer([]byte{0x01, 0x00, 0x00, 0x00}, limits)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := reader.ReadLPVec3("vec"); err == nil {
		t.Fatal("a truncated vector was accepted")
	}
}

// TestTopBitSetTerminationTracksTheContinuationBit covers the array's framing
// without a full element codec: the bit is stolen from each element's own first
// byte, so reading has to clear it and writing has to set it afterwards.
func TestTopBitSetTerminationTracksTheContinuationBit(t *testing.T) {
	limits := bufferLimits(t)

	// Two elements of one byte each. The first has its top bit set.
	encoded := []byte{0x82, 0x03}

	reader, err := NewReadBuffer(encoded, limits)
	if err != nil {
		t.Fatal(err)
	}

	var slots []int8
	for {
		more, err := reader.PeekTopBitSetContinues("equipment")
		if err != nil {
			t.Fatalf("PeekTopBitSetContinues: %v", err)
		}
		slot, err := reader.ReadI8("equipment.slot")
		if err != nil {
			t.Fatalf("ReadI8: %v", err)
		}
		slots = append(slots, slot)
		if !more {
			break
		}
	}

	if len(slots) != 2 || slots[0] != 0x02 || slots[1] != 0x03 {
		t.Fatalf("slots = %v, want [2 3] with the continuation bit cleared", slots)
	}

	writer, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatal(err)
	}
	for index, slot := range slots {
		start := writer.Offset()
		if err := writer.WriteI8("equipment.slot", slot); err != nil {
			t.Fatalf("WriteI8: %v", err)
		}
		if index != len(slots)-1 {
			if err := writer.SetTopBitSetContinues("equipment", start); err != nil {
				t.Fatalf("SetTopBitSetContinues: %v", err)
			}
		}
	}

	if got := writer.Bytes(); !bytes.Equal(got, encoded) {
		t.Errorf("re-encoded % x, want % x", got, encoded)
	}
}

// TestTopBitSetTerminationRejectsAnEmptyBuffer pins the one case the format
// cannot express: the array has no count, so it cannot be empty either.
func TestTopBitSetTerminationRejectsAnEmptyBuffer(t *testing.T) {
	reader, err := NewReadBuffer(nil, bufferLimits(t))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := reader.PeekTopBitSetContinues("equipment"); err == nil {
		t.Fatal("an empty payload was accepted")
	}
}
