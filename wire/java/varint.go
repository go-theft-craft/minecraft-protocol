package java

import (
	"fmt"
	"io"
)

// ReadVarInt reads a Java Edition VarInt.
func ReadVarInt(r io.Reader) (int32, int, error) {
	var result uint32
	var buffer [1]byte

	for read := 0; ; read++ {
		if _, err := io.ReadFull(r, buffer[:]); err != nil {
			return 0, read, fmt.Errorf("read VarInt byte %d: %w", read+1, err)
		}

		result |= uint32(buffer[0]&0x7f) << (7 * read)
		if buffer[0]&0x80 == 0 {
			return int32(result), read + 1, nil
		}
		if read == 4 {
			return 0, read + 1, fmt.Errorf("read VarInt: %w", ErrVarIntTooLong)
		}
	}
}

// WriteVarInt writes a Java Edition VarInt.
func WriteVarInt(w io.Writer, value int32) (int, error) {
	var buffer [5]byte
	return writeFull(w, buffer[:PutVarInt(buffer[:], value)])
}

// PutVarInt encodes value into buffer and returns its encoded size.
// The caller must provide a buffer with room for five bytes.
func PutVarInt(buffer []byte, value int32) int {
	remaining := uint32(value)
	for index := 0; ; index++ {
		byteValue := byte(remaining & 0x7f)
		remaining >>= 7
		if remaining != 0 {
			byteValue |= 0x80
		}
		buffer[index] = byteValue
		if remaining == 0 {
			return index + 1
		}
	}
}

// VarIntSize returns the encoded size of value.
func VarIntSize(value int32) int {
	remaining := uint32(value)
	for size := 1; ; size++ {
		remaining >>= 7
		if remaining == 0 {
			return size
		}
	}
}

// ReadVarLong reads a Java Edition VarLong.
func ReadVarLong(r io.Reader) (int64, int, error) {
	var result uint64
	var buffer [1]byte

	for read := 0; ; read++ {
		if _, err := io.ReadFull(r, buffer[:]); err != nil {
			return 0, read, fmt.Errorf("read VarLong byte %d: %w", read+1, err)
		}

		result |= uint64(buffer[0]&0x7f) << (7 * read)
		if buffer[0]&0x80 == 0 {
			return int64(result), read + 1, nil
		}
		if read == 9 {
			return 0, read + 1, fmt.Errorf("read VarLong: %w", ErrVarLongTooLong)
		}
	}
}

// WriteVarLong writes a Java Edition VarLong.
func WriteVarLong(w io.Writer, value int64) (int, error) {
	var buffer [10]byte
	remaining := uint64(value)
	for index := range buffer {
		byteValue := byte(remaining & 0x7f)
		remaining >>= 7
		if remaining != 0 {
			byteValue |= 0x80
		}
		buffer[index] = byteValue
		if remaining == 0 {
			return writeFull(w, buffer[:index+1])
		}
	}

	panic("unreachable")
}
