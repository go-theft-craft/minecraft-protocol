package java

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

func TestNBTAllTagShapesRoundTripAndOwnership(t *testing.T) {
	t.Parallel()

	encoded := allNBTTagShapes()
	nbt, err := NewNBT(encoded, bufferLimits(t))
	if err != nil {
		t.Fatal(err)
	}
	encoded[0] = TagByte
	if got := nbt.Bytes(); !bytes.Equal(got, allNBTTagShapes()) {
		t.Fatalf("NBT.Bytes() = %x, want %x", got, allNBTTagShapes())
	}
	first := nbt.Bytes()
	first[0] = TagByte
	if got := nbt.Bytes(); got[0] != TagCompound {
		t.Fatalf("second NBT.Bytes()[0] = %d, want TagCompound", got[0])
	}

	w, err := NewWriteBuffer(bufferLimits(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteNBT("packet.nbt", nbt); err != nil {
		t.Fatal(err)
	}
	r, err := NewReadBuffer(w.Bytes(), bufferLimits(t))
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.ReadNBT("packet.nbt")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), allNBTTagShapes()) {
		t.Errorf("ReadNBT().Bytes() = %x, want %x", got.Bytes(), allNBTTagShapes())
	}
}

func TestNBTRequiresNamedCompoundRoot(t *testing.T) {
	t.Parallel()

	for tag, payload := range map[byte][]byte{
		TagByte:      {1},
		TagShort:     {0, 1},
		TagInt:       {0, 0, 0, 1},
		TagLong:      {0, 0, 0, 0, 0, 0, 0, 1},
		TagFloat:     {0x3f, 0x80, 0, 0},
		TagDouble:    {0x3f, 0xf0, 0, 0, 0, 0, 0, 0},
		TagByteArray: {0, 0, 0, 1, 1},
		TagString:    {0, 1, 'x'},
		TagList:      {TagByte, 0, 0, 0, 1, 1},
		TagIntArray:  {0, 0, 0, 1, 0, 0, 0, 1},
	} {
		t.Run(string(rune('A'+tag)), func(t *testing.T) {
			encoded := append([]byte{tag, 0, 0}, payload...)
			if _, err := NewNBT(encoded, bufferLimits(t)); !errors.Is(err, ErrInvalidNBT) {
				t.Fatalf("NewNBT(tag %d) error = %v, want ErrInvalidNBT", tag, err)
			}
		})
	}

	if _, err := NewNBT([]byte{TagCompound, 0, 0, TagEnd}, bufferLimits(t)); err != nil {
		t.Fatalf("NewNBT(compound root) error = %v", err)
	}
}

func TestNBTRejectsJava18LongArrayTag(t *testing.T) {
	t.Parallel()

	encoded := []byte{
		TagCompound, 0, 0,
		12, 0, 1, 'x', 0, 0, 0, 0,
		TagEnd,
	}
	if _, err := NewNBT(encoded, bufferLimits(t)); !errors.Is(err, ErrInvalidNBT) {
		t.Fatalf("NewNBT(long array) error = %v, want ErrInvalidNBT", err)
	}
}

func TestNBTRejectsMalformedModifiedUTF8(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		data []byte
	}{
		{"standalone continuation", []byte{0x80}},
		{"four byte group", []byte{0xf0, 0x90, 0x80, 0x80}},
		{"truncated two byte group", []byte{0xc2}},
		{"bad two byte continuation", []byte{0xc2, 0x20}},
		{"truncated three byte group", []byte{0xe0, 0x80}},
		{"bad three byte continuation", []byte{0xe0, 0x80, 0x20}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NewNBT(nbtWithStringBytes(test.data), bufferLimits(t)); !errors.Is(err, ErrInvalidNBT) {
				t.Fatalf("NewNBT() error = %v, want ErrInvalidNBT", err)
			}
		})
	}

	malformedRootName := []byte{TagCompound, 0, 1, 0x80, TagEnd}
	if _, err := NewNBT(malformedRootName, bufferLimits(t)); !errors.Is(err, ErrInvalidNBT) {
		t.Errorf("NewNBT(malformed root name) error = %v, want ErrInvalidNBT", err)
	}
	malformedKey := []byte{TagCompound, 0, 0, TagByte, 0, 1, 0x80, 1, TagEnd}
	if _, err := NewNBT(malformedKey, bufferLimits(t)); !errors.Is(err, ErrInvalidNBT) {
		t.Errorf("NewNBT(malformed compound key) error = %v, want ErrInvalidNBT", err)
	}
}

func TestNBTRejectsSemanticallyDuplicateModifiedUTF8Keys(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		first  []byte
		second []byte
	}{
		{"null", []byte{0x00}, []byte{0xc0, 0x80}},
		{"overlong ASCII", []byte{'A'}, []byte{0xc1, 0x81}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			encoded := compoundWithByteKeys(test.first, test.second)
			if _, err := NewNBT(encoded, bufferLimits(t)); !errors.Is(err, ErrDuplicateNBTKey) {
				t.Fatalf("NewNBT() error = %v, want ErrDuplicateNBTKey", err)
			}
		})
	}
}

func TestNBTAcceptsModifiedUTF8NullAndSurrogatePair(t *testing.T) {
	t.Parallel()

	for _, value := range [][]byte{
		{0xc0, 0x80},
		{0xed, 0xa0, 0xbd, 0xed, 0xb8, 0x80},
	} {
		if _, err := NewNBT(nbtWithStringBytes(value), bufferLimits(t)); err != nil {
			t.Errorf("NewNBT(%x) error = %v", value, err)
		}
	}
}

func TestNBTRejectsEveryTruncation(t *testing.T) {
	t.Parallel()

	encoded := allNBTTagShapes()
	for cut := range len(encoded) {
		buffer, err := NewReadBuffer(encoded[:cut], bufferLimits(t))
		if err != nil {
			t.Fatal(err)
		}
		_, err = buffer.ReadNBT("packet.nbt")
		if err == nil {
			t.Fatalf("cut %d/%d: error = nil", cut, len(encoded))
		}
		if !strings.Contains(err.Error(), "packet.nbt") {
			t.Errorf("cut %d/%d: error %q lacks field path", cut, len(encoded), err)
		}
	}
}

func TestNBTRejectsInvalidTagsLengthsDuplicatesLimitsDepthAndTrailingBytes(t *testing.T) {
	t.Parallel()

	duplicateCompound := []byte{
		TagCompound, 0, 0,
		TagByte, 0, 1, 'x', 1,
		TagByte, 0, 1, 'x', 2,
		TagEnd,
	}
	nested := []byte{
		TagCompound, 0, 0,
		TagCompound, 0, 1, 'x',
		TagByte, 0, 1, 'y', 1,
		TagEnd,
		TagEnd,
	}
	tests := []struct {
		name    string
		data    []byte
		options []protocol.LimitOption
		want    error
	}{
		{"empty", nil, nil, io.ErrUnexpectedEOF},
		{"root end", []byte{TagEnd}, nil, ErrInvalidNBT},
		{"invalid root tag", []byte{12, 0, 0}, nil, ErrInvalidNBT},
		{"invalid child tag", []byte{TagCompound, 0, 0, 13, 0, 0}, nil, ErrInvalidNBT},
		{"negative byte array", compoundWithTag(TagByteArray, []byte{0xff, 0xff, 0xff, 0xff}), nil, ErrNegativeLength},
		{"negative list", compoundWithTag(TagList, []byte{TagByte, 0xff, 0xff, 0xff, 0xff}), nil, ErrNegativeLength},
		{"negative int array", compoundWithTag(TagIntArray, []byte{0xff, 0xff, 0xff, 0xff}), nil, ErrNegativeLength},
		{"end list with item", compoundWithTag(TagList, []byte{TagEnd, 0, 0, 0, 1}), nil, ErrInvalidNBT},
		{"duplicate compound", duplicateCompound, nil, ErrDuplicateNBTKey},
		{"byte array collection limit", compoundWithTag(TagByteArray, []byte{0, 0, 0, 2, 1, 2}), []protocol.LimitOption{protocol.MaxCollectionItems(1)}, ErrValueTooLarge},
		{"list collection limit", compoundWithTag(TagList, []byte{TagByte, 0, 0, 0, 2, 1, 2}), []protocol.LimitOption{protocol.MaxCollectionItems(1)}, ErrValueTooLarge},
		{"string limit", compoundWithTag(TagString, []byte{0, 2, 'o', 'k'}), []protocol.LimitOption{protocol.MaxStringBytes(1)}, ErrValueTooLarge},
		{"compound entry limit", duplicateNames(2), []protocol.LimitOption{protocol.MaxCollectionItems(1)}, ErrValueTooLarge},
		{"recursion depth", nested, []protocol.LimitOption{protocol.MaxRecursionDepth(2)}, ErrRecursionLimit},
		{"NBT byte limit", allNBTTagShapes(), []protocol.LimitOption{protocol.MaxNBTBytes(8)}, ErrValueTooLarge},
		{"trailing bytes", append([]byte{TagCompound, 0, 0, TagEnd}, 2), nil, ErrTrailingBytes},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewNBT(test.data, bufferLimits(t, test.options...))
			if !errors.Is(err, test.want) {
				t.Fatalf("NewNBT() error = %v, want %v", err, test.want)
			}
		})
	}
}

// TestOptionalNBT covers the presence byte and the value behind it. The slot
// codec that used to share this test is gone: a slot is a schema-defined type
// and is compiled from the schema now.
func TestOptionalNBT(t *testing.T) {
	t.Parallel()

	limits := bufferLimits(t)
	nbt, err := NewNBT([]byte{TagCompound, 0, 0, TagEnd}, limits)
	if err != nil {
		t.Fatal(err)
	}
	w, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.WriteOptionalNBT("packet.none", nil); err != nil {
		t.Fatal(err)
	}
	if err := w.WriteOptionalNBT("packet.some", &nbt); err != nil {
		t.Fatal(err)
	}

	r, err := NewReadBuffer(w.Bytes(), limits)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.ReadOptionalNBT("packet.none"); err != nil || got != nil {
		t.Errorf("ReadOptionalNBT(none) = (%v, %v)", got, err)
	}
	if got, err := r.ReadOptionalNBT("packet.some"); err != nil || got == nil || !bytes.Equal(got.Bytes(), nbt.Bytes()) {
		t.Errorf("ReadOptionalNBT(some) = (%v, %v)", got, err)
	}
}

func allNBTTagShapes() []byte {
	return []byte{
		TagCompound, 0, 4, 'r', 'o', 'o', 't',
		TagByte, 0, 1, 'b', 0xfe,
		TagShort, 0, 1, 's', 0x12, 0x34,
		TagInt, 0, 1, 'i', 0, 0, 0, 1,
		TagLong, 0, 1, 'l', 0, 0, 0, 0, 0, 0, 0, 1,
		TagFloat, 0, 1, 'f', 0x3f, 0x80, 0, 0,
		TagDouble, 0, 1, 'd', 0x3f, 0xf0, 0, 0, 0, 0, 0, 0,
		TagByteArray, 0, 2, 'b', 'a', 0, 0, 0, 2, 0xfe, 0xff,
		TagString, 0, 3, 's', 't', 'r', 0, 2, 'h', 'i',
		TagList, 0, 4, 'l', 'i', 's', 't', TagInt, 0, 0, 0, 2, 0, 0, 0, 1, 0, 0, 0, 2,
		TagCompound, 0, 1, 'c', TagByte, 0, 1, 'x', 1, TagEnd,
		TagIntArray, 0, 2, 'i', 'a', 0, 0, 0, 2, 0, 0, 0, 1, 0, 0, 0, 2,
		TagEnd,
	}
}

func nbtWithStringBytes(value []byte) []byte {
	result := []byte{TagCompound, 0, 0, TagString, 0, 1, 's', byte(len(value) >> 8), byte(len(value))}
	result = append(result, value...)
	return append(result, TagEnd)
}

func compoundWithByteKeys(first, second []byte) []byte {
	result := []byte{TagCompound, 0, 0, TagByte, byte(len(first) >> 8), byte(len(first))}
	result = append(result, first...)
	result = append(result, 1, TagByte, byte(len(second)>>8), byte(len(second)))
	result = append(result, second...)
	return append(result, 2, TagEnd)
}

func compoundWithTag(tag byte, payload []byte) []byte {
	result := []byte{TagCompound, 0, 0, tag, 0, 1, 'x'}
	result = append(result, payload...)
	return append(result, TagEnd)
}

func duplicateNames(count int) []byte {
	result := []byte{TagCompound, 0, 0}
	for index := range count {
		result = append(result, TagByte, 0, 1, byte('a'+index), 1)
	}
	return append(result, TagEnd)
}
