package java

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
)

// LPVec3 is a quantized three-component vector.
//
// The encoding is lossy by design: each component is stored in fifteen bits as
// a fraction of a shared integer scale, so a decoded value is close to what was
// written rather than equal to it. Round-tripping bytes is exact; round-tripping
// floats is not, and nothing here pretends otherwise.
type LPVec3 struct {
	X float64
	Y float64
	Z float64
}

const (
	// lpVec3DataBits masks one packed component.
	lpVec3DataBits = 0x7FFF
	// lpVec3MaxQuantized is the largest quantized component value.
	lpVec3MaxQuantized = 32766.0
	// lpVec3AbsMin is the magnitude below which the whole vector encodes as a
	// single zero byte.
	lpVec3AbsMin = 3.051944088384301e-5
	// lpVec3AbsMax clamps a component before packing.
	lpVec3AbsMax = 1.7179869183e10

	// lpVec3ScaleMask is the part of the scale carried in the first byte.
	lpVec3ScaleMask = 0x3
	// lpVec3ContinuationBit marks a scale too large for two bits, with the rest
	// following as a VarInt.
	lpVec3ContinuationBit = 0x4

	// lpVec3PackedBytes is the fixed part of the encoding: a 48-bit integer
	// holding three flag bits and three fifteen-bit components.
	//
	// It is not one integer in one byte order. The two low bytes are written
	// low first, and the remaining thirty-two bits are written big endian, in
	// that order — which is what a Netty writeByte, writeByte, writeInt
	// produces and is the layout a vanilla server puts on the wire.
	lpVec3PackedBytes = 6

	lpVec3ShiftX = 3
	lpVec3ShiftY = 18
	lpVec3ShiftZ = 33
)

// ReadLPVec3 reads one quantized vector.
func (b *Buffer) ReadLPVec3(path string) (LPVec3, error) {
	if err := b.requireMode(readMode); err != nil {
		return LPVec3{}, withPath(path, err)
	}
	if b.Remaining() == 0 {
		return LPVec3{}, withPath(path, io.ErrUnexpectedEOF)
	}

	// A leading zero byte is the whole encoding: every component is zero, and
	// no scale or packed data follows.
	if b.data[b.offset] == 0 {
		b.offset++

		return LPVec3{}, nil
	}

	if b.Remaining() < lpVec3PackedBytes {
		return LPVec3{}, withPath(path, io.ErrUnexpectedEOF)
	}

	raw := b.data[b.offset : b.offset+lpVec3PackedBytes]
	packed := uint64(binary.BigEndian.Uint32(raw[2:6]))<<16 |
		uint64(raw[1])<<8 |
		uint64(raw[0])
	b.offset += lpVec3PackedBytes

	scale := int64(raw[0] & lpVec3ScaleMask)
	if raw[0]&lpVec3ContinuationBit == lpVec3ContinuationBit {
		high, err := b.ReadVarInt(path)
		if err != nil {
			return LPVec3{}, err
		}
		if high < 0 {
			return LPVec3{}, withPath(path, fmt.Errorf("%w: lpVec3 scale continuation %d is negative", ErrValueOutOfRange, high))
		}
		scale += int64(high) * 4
	}

	return LPVec3{
		X: unpackLPVec3(packed, lpVec3ShiftX) * float64(scale),
		Y: unpackLPVec3(packed, lpVec3ShiftY) * float64(scale),
		Z: unpackLPVec3(packed, lpVec3ShiftZ) * float64(scale),
	}, nil
}

// WriteLPVec3 writes one quantized vector.
func (b *Buffer) WriteLPVec3(path string, value LPVec3) error {
	if err := b.requireMode(writeMode); err != nil {
		return withPath(path, err)
	}

	x := sanitizeLPVec3(value.X)
	y := sanitizeLPVec3(value.Y)
	z := sanitizeLPVec3(value.Z)

	largest := math.Max(math.Abs(x), math.Max(math.Abs(y), math.Abs(z)))
	if largest < lpVec3AbsMin {
		return b.WriteU8(path, 0)
	}

	scale := math.Ceil(largest)
	scaleInt := int64(scale)
	continuation := scaleInt&lpVec3ScaleMask != scaleInt

	first := byte(scaleInt & lpVec3ScaleMask)
	if continuation {
		first |= lpVec3ContinuationBit
	}

	packedX := packLPVec3(x / scale)
	packedY := packLPVec3(y / scale)
	packedZ := packLPVec3(z / scale)
	packed := uint64(first) | packedX<<lpVec3ShiftX | packedY<<lpVec3ShiftY | packedZ<<lpVec3ShiftZ

	raw := make([]byte, lpVec3PackedBytes)
	raw[0] = byte(packed)
	raw[1] = byte(packed >> 8)
	binary.BigEndian.PutUint32(raw[2:6], uint32(packed>>16))
	if err := b.append(path, raw); err != nil {
		return err
	}

	if continuation {
		return b.WriteVarInt(path, int32(scaleInt/4))
	}

	return nil
}

func sanitizeLPVec3(value float64) float64 {
	if math.IsNaN(value) {
		return 0
	}

	return math.Max(-lpVec3AbsMax, math.Min(value, lpVec3AbsMax))
}

// packLPVec3 maps a component in [-1, 1] onto the quantized range.
func packLPVec3(value float64) uint64 {
	return uint64(math.Round((value*0.5 + 0.5) * lpVec3MaxQuantized))
}

// unpackLPVec3 reverses packLPVec3 for one component of the packed integer.
func unpackLPVec3(packed uint64, shift int) float64 {
	value := (packed >> shift) & lpVec3DataBits
	if value > uint64(lpVec3MaxQuantized) {
		value = uint64(lpVec3MaxQuantized)
	}

	return float64(value)*2.0/lpVec3MaxQuantized - 1.0
}

// PeekTopBitSetContinues reports whether the element about to be read is
// followed by another, and clears the bit so the element decodes normally.
//
// A top-bit-terminated array carries no count. Each element's first byte holds
// a continuation bit in its top position, so a decoder reads elements until one
// arrives with that bit clear. The bit manipulation lives here, beside the
// format it belongs to, rather than being inlined into generated code.
func (b *Buffer) PeekTopBitSetContinues(path string) (bool, error) {
	if err := b.requireMode(readMode); err != nil {
		return false, withPath(path, err)
	}
	if b.Remaining() == 0 {
		return false, withPath(path, io.ErrUnexpectedEOF)
	}

	first := b.data[b.offset]
	// The bit is stolen from the element's own first byte, so it has to be
	// cleared before the element is decoded. The buffer owns its bytes, so
	// clearing in place is safe and keeps the element codec unaware.
	b.data[b.offset] = first &^ topBitSetContinuationBit

	return first&topBitSetContinuationBit == topBitSetContinuationBit, nil
}

// SetTopBitSetContinues sets the continuation bit on the element that starts at
// offset, which the caller recorded before writing it.
func (b *Buffer) SetTopBitSetContinues(path string, elementOffset int) error {
	if err := b.requireMode(writeMode); err != nil {
		return withPath(path, err)
	}
	if elementOffset < 0 || elementOffset >= len(b.data) {
		return withPath(path, fmt.Errorf("%w: element offset %d is outside the buffer", ErrValueOutOfRange, elementOffset))
	}

	b.data[elementOffset] |= topBitSetContinuationBit

	return nil
}

// Offset reports how many bytes have been written or read so far. A
// top-bit-terminated array needs it to find each element's first byte again.
func (b *Buffer) Offset() int {
	if b.mode == writeMode {
		return len(b.data)
	}

	return b.offset
}

// topBitSetContinuationBit marks an element of a top-bit-terminated array as
// having a successor.
const topBitSetContinuationBit = 0x80
