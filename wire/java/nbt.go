package java

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"slices"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

// NBT tag IDs used by Java 1.8.
const (
	TagEnd byte = iota
	TagByte
	TagShort
	TagInt
	TagLong
	TagFloat
	TagDouble
	TagByteArray
	TagString
	TagList
	TagCompound
	TagIntArray
)

// NBT is one validated, losslessly retained named binary tag value.
type NBT struct {
	data []byte
}

// NewNBT validates exactly one NBT value and owns encoded.
func NewNBT(encoded []byte, limits protocol.Limits) (NBT, error) {
	if err := validateLimits(limits); err != nil {
		return NBT{}, err
	}
	if len(encoded) > limits.NBTBytes() {
		return NBT{}, &SizeError{Value: "NBT", Size: len(encoded), Limit: limits.NBTBytes()}
	}
	consumed, err := validateNBT(encoded, limits)
	if err != nil {
		return NBT{}, err
	}
	if consumed != len(encoded) {
		return NBT{}, fmt.Errorf("%w: %d unread after NBT", ErrTrailingBytes, len(encoded)-consumed)
	}
	return NBT{data: slices.Clone(encoded)}, nil
}

// Bytes returns an owned copy of the lossless NBT encoding.
func (n NBT) Bytes() []byte { return slices.Clone(n.data) }

// ReadNBT reads one required NBT value.
func (b *Buffer) ReadNBT(path string) (NBT, error) {
	if err := b.requireMode(readMode); err != nil {
		return NBT{}, withPath(path, err)
	}
	consumed, err := validateNBT(b.data[b.offset:], b.limits)
	if err != nil {
		return NBT{}, withPath(path, err)
	}
	value := NBT{data: slices.Clone(b.data[b.offset : b.offset+consumed])}
	b.offset += consumed
	return value, nil
}

// WriteNBT writes one required NBT value after validating it against this buffer's limits.
func (b *Buffer) WriteNBT(path string, value NBT) error {
	if err := b.requireMode(writeMode); err != nil {
		return withPath(path, err)
	}
	validated, err := NewNBT(value.data, b.limits)
	if err != nil {
		return withPath(path, err)
	}
	return b.append(path, validated.data)
}

// ReadOptionalNBT reads Java 1.8 optional NBT, where TAG_End means absent.
func (b *Buffer) ReadOptionalNBT(path string) (*NBT, error) {
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
	value, err := b.ReadNBT(path)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

// WriteOptionalNBT writes TAG_End for nil and a complete NBT value otherwise.
func (b *Buffer) WriteOptionalNBT(path string, value *NBT) error {
	if value == nil {
		return b.WriteU8(path, TagEnd)
	}
	return b.WriteNBT(path, *value)
}

type nbtCursor struct {
	data   []byte
	pos    int
	limits protocol.Limits
}

func validateNBT(data []byte, limits protocol.Limits) (int, error) {
	cursor := nbtCursor{data: data, limits: limits}
	tag, err := cursor.readByte()
	if err != nil {
		return 0, err
	}
	if tag != TagCompound {
		return 0, fmt.Errorf("%w: root tag ID %d is not a named compound", ErrInvalidNBT, tag)
	}
	if _, err := cursor.readString("root name"); err != nil {
		return 0, err
	}
	if err := cursor.readPayload(tag, 1); err != nil {
		return 0, err
	}
	return cursor.pos, nil
}

func (c *nbtCursor) readPayload(tag byte, depth int) error {
	if depth > c.limits.RecursionDepth() {
		return fmt.Errorf("%w: depth %d exceeds limit %d", ErrRecursionLimit, depth, c.limits.RecursionDepth())
	}
	switch tag {
	case TagByte:
		_, err := c.take(1)
		return err
	case TagShort:
		_, err := c.take(2)
		return err
	case TagInt, TagFloat:
		_, err := c.take(4)
		return err
	case TagLong, TagDouble:
		_, err := c.take(8)
		return err
	case TagByteArray:
		return c.readArray(1, "NBT byte array")
	case TagString:
		_, err := c.readString("NBT string")
		return err
	case TagList:
		return c.readList(depth)
	case TagCompound:
		return c.readCompound(depth)
	case TagIntArray:
		return c.readArray(4, "NBT int array")
	default:
		return fmt.Errorf("%w: tag ID %d", ErrInvalidNBT, tag)
	}
}

func (c *nbtCursor) readList(depth int) error {
	tag, err := c.readByte()
	if err != nil {
		return err
	}
	if tag > TagIntArray {
		return fmt.Errorf("%w: list tag ID %d", ErrInvalidNBT, tag)
	}
	count, err := c.readCount("NBT list")
	if err != nil {
		return err
	}
	if tag == TagEnd && count != 0 {
		return fmt.Errorf("%w: TAG_End list has %d items", ErrInvalidNBT, count)
	}
	for range count {
		if err := c.readPayload(tag, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (c *nbtCursor) readCompound(depth int) error {
	keys := make(map[string]struct{})
	for count := 0; ; count++ {
		tag, err := c.readByte()
		if err != nil {
			return err
		}
		if tag == TagEnd {
			return nil
		}
		if tag > TagIntArray {
			return fmt.Errorf("%w: compound tag ID %d", ErrInvalidNBT, tag)
		}
		if count >= c.limits.CollectionItems() {
			return &SizeError{Value: "NBT compound", Size: count + 1, Limit: c.limits.CollectionItems()}
		}
		name, err := c.readString("NBT compound key")
		if err != nil {
			return err
		}
		if _, duplicate := keys[name]; duplicate {
			return fmt.Errorf("%w: %q", ErrDuplicateNBTKey, name)
		}
		keys[name] = struct{}{}
		if err := c.readPayload(tag, depth+1); err != nil {
			return err
		}
	}
}

func (c *nbtCursor) readArray(itemBytes int, name string) error {
	count, err := c.readCount(name)
	if err != nil {
		return err
	}
	if count > math.MaxInt/itemBytes {
		return &SizeError{Value: name, Size: count, Limit: c.limits.CollectionItems()}
	}
	_, err = c.take(count * itemBytes)
	return err
}

func (c *nbtCursor) readCount(name string) (int, error) {
	data, err := c.take(4)
	if err != nil {
		return 0, err
	}
	count := int32(binary.BigEndian.Uint32(data))
	if count < 0 {
		return 0, fmt.Errorf("%w: %s count %d", ErrNegativeLength, name, count)
	}
	if int64(count) > int64(c.limits.CollectionItems()) {
		return 0, &SizeError{Value: name, Size: int(count), Limit: c.limits.CollectionItems()}
	}
	return int(count), nil
}

func (c *nbtCursor) readString(name string) (string, error) {
	lengthData, err := c.take(2)
	if err != nil {
		return "", err
	}
	length := int(binary.BigEndian.Uint16(lengthData))
	if length > c.limits.StringBytes() {
		return "", &SizeError{Value: name, Size: length, Limit: c.limits.StringBytes()}
	}
	data, err := c.take(length)
	if err != nil {
		return "", err
	}
	return decodeModifiedUTF8(data, name)
}

// decodeModifiedUTF8 returns a canonical byte key for the decoded Java UTF-16
// code units. NBT retains the original encoding separately for lossless writes.
func decodeModifiedUTF8(data []byte, name string) (string, error) {
	decoded := make([]byte, 0, len(data)*2)
	for offset := 0; offset < len(data); {
		first := data[offset]
		var value uint16

		switch {
		case first&0x80 == 0:
			value = uint16(first)
			offset++
		case first&0xe0 == 0xc0:
			if offset+1 >= len(data) {
				return "", modifiedUTF8Error(name, offset)
			}
			second := data[offset+1]
			if second&0xc0 != 0x80 {
				return "", modifiedUTF8Error(name, offset)
			}
			value = uint16(first&0x1f)<<6 | uint16(second&0x3f)
			offset += 2
		case first&0xf0 == 0xe0:
			if offset+2 >= len(data) {
				return "", modifiedUTF8Error(name, offset)
			}
			second := data[offset+1]
			third := data[offset+2]
			if second&0xc0 != 0x80 || third&0xc0 != 0x80 {
				return "", modifiedUTF8Error(name, offset)
			}
			value = uint16(first&0x0f)<<12 | uint16(second&0x3f)<<6 | uint16(third&0x3f)
			offset += 3
		default:
			return "", modifiedUTF8Error(name, offset)
		}

		decoded = append(decoded, byte(value>>8), byte(value))
	}
	return string(decoded), nil
}

func modifiedUTF8Error(name string, offset int) error {
	return fmt.Errorf("%w: %s has malformed modified UTF-8 at byte %d", ErrInvalidNBT, name, offset)
}

func (c *nbtCursor) readByte() (byte, error) {
	data, err := c.take(1)
	if err != nil {
		return 0, err
	}
	return data[0], nil
}

func (c *nbtCursor) take(count int) ([]byte, error) {
	if count < 0 {
		return nil, ErrNegativeLength
	}
	if count > c.limits.NBTBytes()-c.pos {
		return nil, &SizeError{Value: "NBT", Size: c.pos + count, Limit: c.limits.NBTBytes()}
	}
	if count > len(c.data)-c.pos {
		return nil, io.ErrUnexpectedEOF
	}
	data := c.data[c.pos : c.pos+count]
	c.pos += count
	return data, nil
}
