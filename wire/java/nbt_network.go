package java

import (
	"fmt"
	"io"
	"slices"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

// NetworkNBT is one validated NBT value in the modern network form, where the
// root compound carries no name.
//
// It is a distinct type from NBT on purpose. The two encodings differ by
// exactly the root name, so a value of one written where the other is expected
// produces bytes that parse as something else rather than failing. Protocol 47
// uses NBT and protocol 775 uses NetworkNBT; the type system is what keeps a
// value from crossing between them.
type NetworkNBT struct {
	data []byte
}

// NewNetworkNBT validates exactly one anonymous-root NBT value and owns encoded.
func NewNetworkNBT(encoded []byte, limits protocol.Limits) (NetworkNBT, error) {
	if err := validateLimits(limits); err != nil {
		return NetworkNBT{}, err
	}
	if len(encoded) > limits.NBTBytes() {
		return NetworkNBT{}, &SizeError{Value: "network NBT", Size: len(encoded), Limit: limits.NBTBytes()}
	}

	consumed, err := validateNetworkNBT(encoded, limits)
	if err != nil {
		return NetworkNBT{}, err
	}
	if consumed != len(encoded) {
		return NetworkNBT{}, fmt.Errorf("%w: %d unread after network NBT", ErrTrailingBytes, len(encoded)-consumed)
	}

	return NetworkNBT{data: slices.Clone(encoded)}, nil
}

// Bytes returns an owned copy of the lossless encoding.
func (n NetworkNBT) Bytes() []byte { return slices.Clone(n.data) }

// ReadAnonymousNBT reads one required NBT value whose root compound has no name.
func (b *Buffer) ReadAnonymousNBT(path string) (NetworkNBT, error) {
	if err := b.requireMode(readMode); err != nil {
		return NetworkNBT{}, withPath(path, err)
	}

	consumed, err := validateNetworkNBT(b.data[b.offset:], b.limits)
	if err != nil {
		return NetworkNBT{}, withPath(path, err)
	}
	if consumed > b.limits.NBTBytes() {
		return NetworkNBT{}, withPath(path, &SizeError{Value: "network NBT", Size: consumed, Limit: b.limits.NBTBytes()})
	}

	value := NetworkNBT{data: slices.Clone(b.data[b.offset : b.offset+consumed])}
	b.offset += consumed

	return value, nil
}

// WriteAnonymousNBT writes one required anonymous-root NBT value.
func (b *Buffer) WriteAnonymousNBT(path string, value NetworkNBT) error {
	if err := b.requireMode(writeMode); err != nil {
		return withPath(path, err)
	}

	validated, err := NewNetworkNBT(value.data, b.limits)
	if err != nil {
		return withPath(path, err)
	}

	return b.append(path, validated.data)
}

// ReadAnonOptionalNBT reads optional anonymous NBT, where TAG_End means absent.
func (b *Buffer) ReadAnonOptionalNBT(path string) (*NetworkNBT, error) {
	if err := b.requireMode(readMode); err != nil {
		return nil, withPath(path, err)
	}
	if b.Remaining() == 0 {
		return nil, withPath(path, io.ErrUnexpectedEOF)
	}
	if b.data[b.offset] == TagEnd {
		b.offset++

		return nil, nil
	}

	value, err := b.ReadAnonymousNBT(path)
	if err != nil {
		return nil, err
	}

	return &value, nil
}

// WriteAnonOptionalNBT writes TAG_End for nil and a complete value otherwise.
func (b *Buffer) WriteAnonOptionalNBT(path string, value *NetworkNBT) error {
	if value == nil {
		return b.WriteU8(path, TagEnd)
	}

	return b.WriteAnonymousNBT(path, *value)
}

// validateNetworkNBT walks one anonymous-root value and returns its length.
//
// It shares every payload rule with the named form and diverges only at the
// root, which is a tag byte followed straight by its payload. A named-root
// value fed to it does not silently pass: the name's length prefix reads as a
// TAG_End, ending the compound early and leaving the rest as trailing bytes.
func validateNetworkNBT(data []byte, limits protocol.Limits) (int, error) {
	cursor := nbtCursor{data: data, limits: limits}

	tag, err := cursor.readByte()
	if err != nil {
		return 0, err
	}
	if tag != TagCompound {
		return 0, fmt.Errorf("%w: network root tag ID %d is not a compound", ErrInvalidNBT, tag)
	}
	if err := cursor.readPayload(tag, 1); err != nil {
		return 0, err
	}

	return cursor.pos, nil
}
