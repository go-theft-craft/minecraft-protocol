package java

import (
	"bytes"
	"fmt"
	"io"
	"slices"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

type bufferMode uint8

const (
	readMode bufferMode = iota
	writeMode
)

// Buffer reads or writes one bounded packet payload.
type Buffer struct {
	data   []byte
	offset int
	limits protocol.Limits
	mode   bufferMode
	// depth counts nested decodes in progress. See depth.go.
	depth int
}

// NewReadBuffer returns a bounded buffer that owns payload.
func NewReadBuffer(payload []byte, limits protocol.Limits) (*Buffer, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}
	if len(payload) > limits.FrameBytes() {
		return nil, fmt.Errorf("%w: payload size %d exceeds limit %d", ErrFrameTooLarge, len(payload), limits.FrameBytes())
	}
	return &Buffer{data: slices.Clone(payload), limits: limits, mode: readMode}, nil
}

// NewWriteBuffer returns an empty bounded payload buffer.
func NewWriteBuffer(limits protocol.Limits) (*Buffer, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}
	return &Buffer{limits: limits, mode: writeMode}, nil
}

// Bytes returns an owned copy of the complete payload.
func (b *Buffer) Bytes() []byte { return slices.Clone(b.data) }

// Remaining returns the unread byte count. A write buffer has no unread bytes.
func (b *Buffer) Remaining() int {
	if b.mode == writeMode {
		return 0
	}
	return len(b.data) - b.offset
}

// RequireEmpty rejects trailing packet payload bytes.
func (b *Buffer) RequireEmpty(path string) error {
	if err := b.requireMode(readMode); err != nil {
		return withPath(path, err)
	}
	if remaining := b.Remaining(); remaining != 0 {
		return withPath(path, fmt.Errorf("%w: %d unread", ErrTrailingBytes, remaining))
	}
	return nil
}

// ValidateCollection validates a generated fixed, referenced, or prefixed count.
func (b *Buffer) ValidateCollection(path string, count int) error {
	if b == nil || !b.limits.Valid() {
		return withPath(path, ErrInvalidLimits)
	}
	if count < 0 {
		return withPath(path, fmt.Errorf("%w: %d", ErrNegativeLength, count))
	}
	if count > b.limits.CollectionItems() {
		return withPath(path, &SizeError{Value: "collection", Size: count, Limit: b.limits.CollectionItems()})
	}
	return nil
}

// ReadCollectionLength reads and validates a VarInt collection count.
func (b *Buffer) ReadCollectionLength(path string) (int, error) {
	value, err := b.ReadVarInt(path)
	if err != nil {
		return 0, err
	}
	if err := b.ValidateCollection(path, int(value)); err != nil {
		return 0, err
	}
	return int(value), nil
}

// WriteCollectionLength validates and writes a VarInt collection count.
func (b *Buffer) WriteCollectionLength(path string, count int) error {
	if err := b.ValidateCollection(path, count); err != nil {
		return err
	}
	if int64(count) > int64(^uint32(0)>>1) {
		return withPath(path, fmt.Errorf("%w: collection count %d", ErrValueOutOfRange, count))
	}
	return b.WriteVarInt(path, int32(count))
}

// ReadVarInt reads a Java Edition VarInt.
func (b *Buffer) ReadVarInt(path string) (int32, error) {
	var value int32
	err := b.readValue(path, func(r io.Reader) error {
		var err error
		value, _, err = ReadVarInt(r)
		return err
	})
	return value, err
}

// WriteVarInt writes a Java Edition VarInt.
func (b *Buffer) WriteVarInt(path string, value int32) error {
	return b.writeValue(path, func(w io.Writer) error {
		_, err := WriteVarInt(w, value)
		return err
	})
}

// ReadVarLong reads a Java Edition VarLong.
func (b *Buffer) ReadVarLong(path string) (int64, error) {
	var value int64
	err := b.readValue(path, func(r io.Reader) error {
		var err error
		value, _, err = ReadVarLong(r)
		return err
	})
	return value, err
}

// WriteVarLong writes a Java Edition VarLong.
func (b *Buffer) WriteVarLong(path string, value int64) error {
	return b.writeValue(path, func(w io.Writer) error {
		_, err := WriteVarLong(w, value)
		return err
	})
}

// ReadI8 reads a signed byte.
func (b *Buffer) ReadI8(path string) (int8, error) {
	return readWith(b, path, ReadI8)
}

// WriteI8 writes a signed byte.
func (b *Buffer) WriteI8(path string, value int8) error {
	return b.writeValue(path, discardCount(value, WriteI8))
}

// ReadU8 reads an unsigned byte.
func (b *Buffer) ReadU8(path string) (uint8, error) {
	return readWith(b, path, ReadU8)
}

// WriteU8 writes an unsigned byte.
func (b *Buffer) WriteU8(path string, value uint8) error {
	return b.writeValue(path, discardCount(value, WriteU8))
}

// ReadI16 reads a signed 16-bit integer.
func (b *Buffer) ReadI16(path string) (int16, error) {
	return readWith(b, path, ReadI16)
}

// WriteI16 writes a signed 16-bit integer.
func (b *Buffer) WriteI16(path string, value int16) error {
	return b.writeValue(path, discardCount(value, WriteI16))
}

// ReadU16 reads an unsigned 16-bit integer.
func (b *Buffer) ReadU16(path string) (uint16, error) {
	return readWith(b, path, ReadU16)
}

// WriteU16 writes an unsigned 16-bit integer.
func (b *Buffer) WriteU16(path string, value uint16) error {
	return b.writeValue(path, discardCount(value, WriteU16))
}

// ReadI32 reads a signed 32-bit integer.
func (b *Buffer) ReadI32(path string) (int32, error) {
	return readWith(b, path, ReadI32)
}

// WriteI32 writes a signed 32-bit integer.
func (b *Buffer) WriteI32(path string, value int32) error {
	return b.writeValue(path, discardCount(value, WriteI32))
}

// ReadU32 reads an unsigned 32-bit integer.
func (b *Buffer) ReadU32(path string) (uint32, error) {
	return readWith(b, path, ReadU32)
}

// WriteU32 writes an unsigned 32-bit integer.
func (b *Buffer) WriteU32(path string, value uint32) error {
	return b.writeValue(path, discardCount(value, WriteU32))
}

// ReadI64 reads a signed 64-bit integer.
func (b *Buffer) ReadI64(path string) (int64, error) {
	return readWith(b, path, ReadI64)
}

// WriteI64 writes a signed 64-bit integer.
func (b *Buffer) WriteI64(path string, value int64) error {
	return b.writeValue(path, discardCount(value, WriteI64))
}

// ReadU64 reads an unsigned 64-bit integer.
func (b *Buffer) ReadU64(path string) (uint64, error) {
	return readWith(b, path, ReadU64)
}

// WriteU64 writes an unsigned 64-bit integer.
func (b *Buffer) WriteU64(path string, value uint64) error {
	return b.writeValue(path, discardCount(value, WriteU64))
}

// ReadF32 reads a 32-bit floating-point value.
func (b *Buffer) ReadF32(path string) (float32, error) {
	return readWith(b, path, ReadF32)
}

// WriteF32 writes a 32-bit floating-point value.
func (b *Buffer) WriteF32(path string, value float32) error {
	return b.writeValue(path, discardCount(value, WriteF32))
}

// ReadF64 reads a 64-bit floating-point value.
func (b *Buffer) ReadF64(path string) (float64, error) {
	return readWith(b, path, ReadF64)
}

// WriteF64 writes a 64-bit floating-point value.
func (b *Buffer) WriteF64(path string, value float64) error {
	return b.writeValue(path, discardCount(value, WriteF64))
}

// ReadBool reads a Java Edition boolean.
func (b *Buffer) ReadBool(path string) (bool, error) {
	return readWith(b, path, ReadBool)
}

// WriteBool writes a Java Edition boolean.
func (b *Buffer) WriteBool(path string, value bool) error {
	return b.writeValue(path, discardCount(value, WriteBool))
}

// ReadUUID reads a 16-byte UUID.
func (b *Buffer) ReadUUID(path string) (UUID, error) {
	value, err := readWith(b, path, ReadUUID)
	return UUID(value), err
}

// WriteUUID writes a 16-byte UUID.
func (b *Buffer) WriteUUID(path string, value UUID) error {
	return b.writeValue(path, discardCount([16]byte(value), WriteUUID))
}

// ReadString reads a VarInt-prefixed bounded string.
func (b *Buffer) ReadString(path string) (string, error) {
	return readWithLimits(b, path, ReadString)
}

// WriteString writes a VarInt-prefixed bounded string.
func (b *Buffer) WriteString(path, value string) error {
	return b.writeValue(path, func(w io.Writer) error {
		_, err := WriteString(w, b.limits, value)
		return err
	})
}

// ReadByteArray reads a VarInt-prefixed bounded byte buffer.
func (b *Buffer) ReadByteArray(path string) ([]byte, error) {
	return readWithLimits(b, path, ReadByteArray)
}

// WriteByteArray writes a VarInt-prefixed bounded byte buffer.
func (b *Buffer) WriteByteArray(path string, value []byte) error {
	return b.writeValue(path, func(w io.Writer) error {
		_, err := WriteByteArray(w, b.limits, value)
		return err
	})
}

// ReadBuffer reads a fixed or externally counted bounded byte buffer.
func (b *Buffer) ReadBuffer(path string, count int) ([]byte, error) {
	if err := b.ValidateCollection(path, count); err != nil {
		return nil, err
	}
	return b.readBytes(path, count)
}

// WriteBuffer writes a fixed or externally counted bounded byte buffer.
func (b *Buffer) WriteBuffer(path string, value []byte) error {
	if err := b.ValidateCollection(path, len(value)); err != nil {
		return err
	}
	return b.append(path, value)
}

// ReadRestBuffer reads the rest of a packet payload.
func (b *Buffer) ReadRestBuffer(path string) ([]byte, error) {
	if err := b.requireMode(readMode); err != nil {
		return nil, withPath(path, err)
	}
	return b.readBytes(path, b.Remaining())
}

// WriteRestBuffer writes an unprefixed packet payload suffix.
func (b *Buffer) WriteRestBuffer(path string, value []byte) error {
	return b.append(path, value)
}

// ReadPluginPayload reads the rest of a packet under the plugin payload limit.
func (b *Buffer) ReadPluginPayload(path string) ([]byte, error) {
	if err := b.requireMode(readMode); err != nil {
		return nil, withPath(path, err)
	}
	if remaining := b.Remaining(); remaining > b.limits.PluginBytes() {
		return nil, withPath(path, &SizeError{Value: "plugin payload", Size: remaining, Limit: b.limits.PluginBytes()})
	}
	return b.readBytes(path, b.Remaining())
}

// WritePluginPayload writes an unprefixed plugin payload.
func (b *Buffer) WritePluginPayload(path string, value []byte) error {
	if err := b.requireMode(writeMode); err != nil {
		return withPath(path, err)
	}
	if len(value) > b.limits.PluginBytes() {
		return withPath(path, &SizeError{Value: "plugin payload", Size: len(value), Limit: b.limits.PluginBytes()})
	}
	return b.append(path, value)
}

func readWith[T any](b *Buffer, path string, read func(io.Reader) (T, error)) (T, error) {
	var value T
	err := b.readValue(path, func(r io.Reader) error {
		var err error
		value, err = read(r)
		return err
	})
	return value, err
}

func readWithLimits[T any](b *Buffer, path string, read func(io.Reader, protocol.Limits) (T, error)) (T, error) {
	var value T
	err := b.readValue(path, func(r io.Reader) error {
		var err error
		value, err = read(r, b.limits)
		return err
	})
	return value, err
}

func discardCount[T any](value T, write func(io.Writer, T) (int, error)) func(io.Writer) error {
	return func(w io.Writer) error {
		_, err := write(w, value)
		return err
	}
}

func (b *Buffer) readValue(path string, read func(io.Reader) error) error {
	if err := b.requireMode(readMode); err != nil {
		return withPath(path, err)
	}
	reader := bytes.NewReader(b.data[b.offset:])
	if err := read(reader); err != nil {
		return withPath(path, err)
	}
	b.offset += b.Remaining() - reader.Len()
	return nil
}

func (b *Buffer) writeValue(path string, write func(io.Writer) error) error {
	if err := b.requireMode(writeMode); err != nil {
		return withPath(path, err)
	}
	var encoded bytes.Buffer
	if err := write(&encoded); err != nil {
		return withPath(path, err)
	}
	return b.append(path, encoded.Bytes())
}

func (b *Buffer) readBytes(path string, count int) ([]byte, error) {
	if err := b.requireMode(readMode); err != nil {
		return nil, withPath(path, err)
	}
	if count < 0 {
		return nil, withPath(path, fmt.Errorf("%w: %d", ErrNegativeLength, count))
	}
	if count > b.Remaining() {
		return nil, withPath(path, io.ErrUnexpectedEOF)
	}
	result := slices.Clone(b.data[b.offset : b.offset+count])
	b.offset += count
	return result, nil
}

func (b *Buffer) append(path string, value []byte) error {
	if err := b.requireMode(writeMode); err != nil {
		return withPath(path, err)
	}
	if len(value) > b.limits.FrameBytes()-len(b.data) {
		return withPath(path, fmt.Errorf("%w: payload size %d exceeds limit %d", ErrFrameTooLarge, len(b.data)+len(value), b.limits.FrameBytes()))
	}
	b.data = append(b.data, value...)
	return nil
}

func (b *Buffer) requireMode(mode bufferMode) error {
	if b == nil {
		return fmt.Errorf("%w: nil buffer", ErrWrongBufferMode)
	}
	if !b.limits.Valid() {
		return ErrInvalidLimits
	}
	if b.mode != mode {
		return ErrWrongBufferMode
	}
	return nil
}
