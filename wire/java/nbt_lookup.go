package java

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
)

// This file reads single named values out of an already-validated NBT value.
//
// Both NBT types are retained losslessly and exposed as bytes, which is right
// for a module that must not lose what it did not model. It is not enough for
// a caller that needs one scalar out of server-sent data: a dimension type's
// minimum build height decides where a chunk's sections sit in the world, and
// a consumer that cannot read it either guesses the world's floor or writes
// its own NBT walker. Both were observed. This is the narrow alternative —
// enough to answer a question, not enough to become a document model.
//
// The lookups take a path of compound keys, return the zero value with false
// when the path is absent or names a different tag, and never fail otherwise:
// the value was validated when it was constructed, so a walk over it cannot
// find a malformed tag.
//
// Only integers are readable. A string accessor would need a decoder from
// Java's modified UTF-8 to a Go string, and this package has no such thing:
// decodeModifiedUTF8 produces a canonical key for comparing names, not text.
// Adding one is a larger job than the caller that prompted this needed, and
// half of one that silently mangled a surrogate pair would be worse than none.

// Int returns the TAG_Int reached by following path through named compounds.
//
// The second result is false when the path does not exist or the value it
// names is not a TAG_Int, which a caller must distinguish from a real zero.
func (n NetworkNBT) Int(path ...string) (int32, bool) {
	return lookupInt(n.data, true, path)
}

// Int returns the TAG_Int reached by following path through named compounds.
func (n NBT) Int(path ...string) (int32, bool) {
	return lookupInt(n.data, false, path)
}

func lookupInt(data []byte, anonymousRoot bool, path []string) (int32, bool) {
	payload, tag, ok := lookup(data, anonymousRoot, path)
	if !ok || tag != TagInt || len(payload) < 4 {
		return 0, false
	}

	return int32(binary.BigEndian.Uint32(payload)), true
}

// lookup returns the payload and tag of the value at path, which must be
// reached through named compounds.
func lookup(data []byte, anonymousRoot bool, path []string) ([]byte, byte, bool) {
	if len(path) == 0 {
		return nil, TagEnd, false
	}

	walker := &nbtWalker{data: data}
	tag, err := walker.readByte()
	if err != nil || tag != TagCompound {
		return nil, TagEnd, false
	}
	if !anonymousRoot {
		if _, ok := walker.readName(); !ok {
			return nil, TagEnd, false
		}
	}

	// Every element but the last must be a compound to descend into. The last
	// is whatever it is, and the caller's typed wrapper decides whether that
	// is the tag it wanted.
	//
	// Names are compared as bytes in the encoding they arrived in rather than
	// decoded first. A name is modified UTF-8 and a Go string is UTF-8, and
	// the two agree on every key vanilla uses but not on all of them; encoding
	// the caller's key is exact where decoding the server's would need a
	// decoder this package does not have.
	for _, key := range path[:len(path)-1] {
		tag, ok := walker.enter(encodeModifiedUTF8(key))
		if !ok || tag != TagCompound {
			return nil, TagEnd, false
		}
	}

	return walker.value(encodeModifiedUTF8(path[len(path)-1]))
}

// nbtWalker steps over a validated NBT value. It bounds-checks anyway: a
// value that reached here was validated, and a walker that trusts that and is
// wrong reads someone else's memory rather than returning false.
type nbtWalker struct {
	data []byte
	pos  int
}

// enter positions the walker at the start of the named entry's payload and
// returns its tag. key is the name as it appears on the wire.
func (w *nbtWalker) enter(key []byte) (byte, bool) {
	for {
		tag, err := w.readByte()
		if err != nil || tag == TagEnd {
			return TagEnd, false
		}
		name, ok := w.readName()
		if !ok {
			return TagEnd, false
		}
		if bytes.Equal(name, key) {
			return tag, true
		}
		if !w.skipPayload(tag) {
			return TagEnd, false
		}
	}
}

// value returns the named entry's payload without consuming it, so a typed
// wrapper can read it.
func (w *nbtWalker) value(key []byte) ([]byte, byte, bool) {
	tag, ok := w.enter(key)
	if !ok {
		return nil, TagEnd, false
	}

	return w.data[w.pos:], tag, true
}

func (w *nbtWalker) readByte() (byte, error) {
	if w.pos >= len(w.data) {
		return 0, io.ErrUnexpectedEOF
	}
	value := w.data[w.pos]
	w.pos++

	return value, nil
}

func (w *nbtWalker) take(n int) ([]byte, bool) {
	if n < 0 || w.pos+n > len(w.data) {
		return nil, false
	}
	taken := w.data[w.pos : w.pos+n]
	w.pos += n

	return taken, true
}

func (w *nbtWalker) count() (int, bool) {
	raw, ok := w.take(4)
	if !ok {
		return 0, false
	}
	count := int32(binary.BigEndian.Uint32(raw))
	if count < 0 {
		return 0, false
	}

	return int(count), true
}

// readName returns one length-prefixed name as it was encoded.
func (w *nbtWalker) readName() ([]byte, bool) {
	raw, ok := w.take(2)
	if !ok {
		return nil, false
	}

	return w.take(int(binary.BigEndian.Uint16(raw)))
}

// skipPayload steps over one value of the given tag.
//
//nolint:cyclop // One switch over the NBT tags; splitting it hides the table.
func (w *nbtWalker) skipPayload(tag byte) bool {
	switch tag {
	case TagByte:
		_, ok := w.take(1)

		return ok
	case TagShort:
		_, ok := w.take(2)

		return ok
	case TagInt, TagFloat:
		_, ok := w.take(4)

		return ok
	case TagLong, TagDouble:
		_, ok := w.take(8)

		return ok
	case TagByteArray:
		return w.skipArray(1)
	case TagString:
		_, ok := w.readName()

		return ok
	case TagList:
		return w.skipList()
	case TagCompound:
		return w.skipCompound()
	case TagIntArray:
		return w.skipArray(4)
	default:
		return false
	}
}

func (w *nbtWalker) skipArray(itemBytes int) bool {
	count, ok := w.count()
	if !ok {
		return false
	}
	if count > math.MaxInt/itemBytes {
		return false
	}
	_, ok = w.take(count * itemBytes)

	return ok
}

func (w *nbtWalker) skipList() bool {
	tag, err := w.readByte()
	if err != nil {
		return false
	}
	count, ok := w.count()
	if !ok {
		return false
	}
	for range count {
		if !w.skipPayload(tag) {
			return false
		}
	}

	return true
}

func (w *nbtWalker) skipCompound() bool {
	for {
		tag, err := w.readByte()
		if err != nil {
			return false
		}
		if tag == TagEnd {
			return true
		}
		if _, ok := w.readName(); !ok {
			return false
		}
		if !w.skipPayload(tag) {
			return false
		}
	}
}
