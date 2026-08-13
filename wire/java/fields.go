package java

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"

	"github.com/go-theft-craft/minecraft-protocol"
)

// EncodePosition packs Java Edition block coordinates into a signed 64-bit value.
func EncodePosition(x, y, z int) int64 {
	return (int64(x)&0x3ffffff)<<38 | (int64(y)&0xfff)<<26 | int64(z)&0x3ffffff
}

// DecodePosition unpacks Java Edition block coordinates from a signed 64-bit value.
func DecodePosition(value int64) (x, y, z int) {
	x = int(value >> 38)
	y = int((value >> 26) & 0xfff)
	z = int(value & 0x3ffffff)

	if x >= 1<<25 {
		x -= 1 << 26
	}
	if y >= 1<<11 {
		y -= 1 << 12
	}
	if z >= 1<<25 {
		z -= 1 << 26
	}
	return x, y, z
}

// ReadPosition reads packed Java Edition block coordinates.
func ReadPosition(r io.Reader) (x, y, z int, err error) {
	value, err := ReadI64(r)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("read position: %w", err)
	}
	x, y, z = DecodePosition(value)
	return x, y, z, nil
}

// WritePosition writes packed Java Edition block coordinates.
func WritePosition(w io.Writer, x, y, z int) (int, error) {
	return WriteI64(w, EncodePosition(x, y, z))
}

// ReadString reads a bounded string byte sequence prefixed by a VarInt length.
func ReadString(r io.Reader, limits protocol.Limits) (string, error) {
	if err := validateLimits(limits); err != nil {
		return "", err
	}

	length, _, err := ReadVarInt(r)
	if err != nil {
		return "", fmt.Errorf("read string length: %w", err)
	}
	if length < 0 {
		return "", fmt.Errorf("read string length %d: %w", length, ErrNegativeLength)
	}
	if int64(length) > int64(limits.StringBytes()) {
		return "", &SizeError{Value: "string", Size: int(length), Limit: limits.StringBytes()}
	}

	data := make([]byte, int(length))
	if _, err := io.ReadFull(r, data); err != nil {
		return "", fmt.Errorf("read string data: %w", err)
	}
	return string(data), nil
}

// WriteString writes a bounded string byte sequence prefixed by a VarInt length.
func WriteString(w io.Writer, limits protocol.Limits, value string) (int, error) {
	if err := validateLimits(limits); err != nil {
		return 0, err
	}
	if len(value) > limits.StringBytes() {
		return 0, &SizeError{Value: "string", Size: len(value), Limit: limits.StringBytes()}
	}

	written, err := WriteVarInt(w, int32(len(value)))
	if err != nil {
		return written, fmt.Errorf("write string length: %w", err)
	}
	dataWritten, err := writeFull(w, []byte(value))
	if err != nil {
		return written + dataWritten, fmt.Errorf("write string data: %w", err)
	}
	return written + dataWritten, nil
}

// ReadByteArray reads a bounded byte sequence prefixed by a VarInt length.
func ReadByteArray(r io.Reader, limits protocol.Limits) ([]byte, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}

	length, _, err := ReadVarInt(r)
	if err != nil {
		return nil, fmt.Errorf("read byte array length: %w", err)
	}
	if length < 0 {
		return nil, fmt.Errorf("read byte array length %d: %w", length, ErrNegativeLength)
	}
	if int64(length) > int64(limits.CollectionItems()) {
		return nil, &SizeError{Value: "byte array", Size: int(length), Limit: limits.CollectionItems()}
	}

	data := make([]byte, int(length))
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("read byte array data: %w", err)
	}
	return data, nil
}

// WriteByteArray writes a bounded byte sequence prefixed by a VarInt length.
func WriteByteArray(w io.Writer, limits protocol.Limits, value []byte) (int, error) {
	if err := validateLimits(limits); err != nil {
		return 0, err
	}
	if len(value) > limits.CollectionItems() {
		return 0, &SizeError{Value: "byte array", Size: len(value), Limit: limits.CollectionItems()}
	}

	written, err := WriteVarInt(w, int32(len(value)))
	if err != nil {
		return written, fmt.Errorf("write byte array length: %w", err)
	}
	dataWritten, err := writeFull(w, value)
	if err != nil {
		return written + dataWritten, fmt.Errorf("write byte array data: %w", err)
	}
	return written + dataWritten, nil
}

// ReadUUID reads a 16-byte UUID.
func ReadUUID(r io.Reader) ([16]byte, error) {
	var value [16]byte
	if _, err := io.ReadFull(r, value[:]); err != nil {
		return value, fmt.Errorf("read UUID: %w", err)
	}
	return value, nil
}

// WriteUUID writes a 16-byte UUID.
func WriteUUID(w io.Writer, value [16]byte) (int, error) {
	return writeFull(w, value[:])
}

// ReadI8 reads a signed byte.
func ReadI8(r io.Reader) (int8, error) {
	value, err := ReadU8(r)
	return int8(value), err
}

// WriteI8 writes a signed byte.
func WriteI8(w io.Writer, value int8) (int, error) {
	return writeFull(w, []byte{byte(value)})
}

// ReadU8 reads an unsigned byte.
func ReadU8(r io.Reader) (uint8, error) {
	var data [1]byte
	if _, err := io.ReadFull(r, data[:]); err != nil {
		return 0, fmt.Errorf("read unsigned byte: %w", err)
	}
	return data[0], nil
}

// WriteU8 writes an unsigned byte.
func WriteU8(w io.Writer, value uint8) (int, error) {
	return writeFull(w, []byte{value})
}

// ReadI16 reads a big-endian signed 16-bit integer.
func ReadI16(r io.Reader) (int16, error) {
	value, err := ReadU16(r)
	return int16(value), err
}

// WriteI16 writes a big-endian signed 16-bit integer.
func WriteI16(w io.Writer, value int16) (int, error) {
	return WriteU16(w, uint16(value))
}

// ReadU16 reads a big-endian unsigned 16-bit integer.
func ReadU16(r io.Reader) (uint16, error) {
	var data [2]byte
	if _, err := io.ReadFull(r, data[:]); err != nil {
		return 0, fmt.Errorf("read unsigned 16-bit integer: %w", err)
	}
	return binary.BigEndian.Uint16(data[:]), nil
}

// WriteU16 writes a big-endian unsigned 16-bit integer.
func WriteU16(w io.Writer, value uint16) (int, error) {
	var data [2]byte
	binary.BigEndian.PutUint16(data[:], value)
	return writeFull(w, data[:])
}

// ReadI32 reads a big-endian signed 32-bit integer.
func ReadI32(r io.Reader) (int32, error) {
	value, err := ReadU32(r)
	return int32(value), err
}

// WriteI32 writes a big-endian signed 32-bit integer.
func WriteI32(w io.Writer, value int32) (int, error) {
	return WriteU32(w, uint32(value))
}

// ReadU32 reads a big-endian unsigned 32-bit integer.
func ReadU32(r io.Reader) (uint32, error) {
	var data [4]byte
	if _, err := io.ReadFull(r, data[:]); err != nil {
		return 0, fmt.Errorf("read unsigned 32-bit integer: %w", err)
	}
	return binary.BigEndian.Uint32(data[:]), nil
}

// WriteU32 writes a big-endian unsigned 32-bit integer.
func WriteU32(w io.Writer, value uint32) (int, error) {
	var data [4]byte
	binary.BigEndian.PutUint32(data[:], value)
	return writeFull(w, data[:])
}

// ReadI64 reads a big-endian signed 64-bit integer.
func ReadI64(r io.Reader) (int64, error) {
	value, err := ReadU64(r)
	return int64(value), err
}

// WriteI64 writes a big-endian signed 64-bit integer.
func WriteI64(w io.Writer, value int64) (int, error) {
	return WriteU64(w, uint64(value))
}

// ReadU64 reads a big-endian unsigned 64-bit integer.
func ReadU64(r io.Reader) (uint64, error) {
	var data [8]byte
	if _, err := io.ReadFull(r, data[:]); err != nil {
		return 0, fmt.Errorf("read unsigned 64-bit integer: %w", err)
	}
	return binary.BigEndian.Uint64(data[:]), nil
}

// WriteU64 writes a big-endian unsigned 64-bit integer.
func WriteU64(w io.Writer, value uint64) (int, error) {
	var data [8]byte
	binary.BigEndian.PutUint64(data[:], value)
	return writeFull(w, data[:])
}

// ReadF32 reads a big-endian IEEE 754 single-precision number.
func ReadF32(r io.Reader) (float32, error) {
	value, err := ReadU32(r)
	return math.Float32frombits(value), err
}

// WriteF32 writes a big-endian IEEE 754 single-precision number.
func WriteF32(w io.Writer, value float32) (int, error) {
	return WriteU32(w, math.Float32bits(value))
}

// ReadF64 reads a big-endian IEEE 754 double-precision number.
func ReadF64(r io.Reader) (float64, error) {
	value, err := ReadU64(r)
	return math.Float64frombits(value), err
}

// WriteF64 writes a big-endian IEEE 754 double-precision number.
func WriteF64(w io.Writer, value float64) (int, error) {
	return WriteU64(w, math.Float64bits(value))
}

// ReadBool reads a Java Edition boolean.
func ReadBool(r io.Reader) (bool, error) {
	value, err := ReadU8(r)
	return value != 0, err
}

// WriteBool writes a Java Edition boolean.
func WriteBool(w io.Writer, value bool) (int, error) {
	if value {
		return WriteU8(w, 1)
	}
	return WriteU8(w, 0)
}

func validateLimits(limits protocol.Limits) error {
	if !limits.Valid() {
		return fmt.Errorf("variable-length field: %w", ErrInvalidLimits)
	}
	return nil
}
