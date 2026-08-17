package java

import (
	"encoding/binary"
	"testing"
)

// nbt builds NBT bodies for these tests. The lookups walk a value that was
// already validated, so every fixture here goes through NewNetworkNBT or
// NewNBT first, which is also what proves the fixtures are well-formed.

func compound(entries ...[]byte) []byte {
	body := []byte{}
	for _, entry := range entries {
		body = append(body, entry...)
	}

	return append(body, TagEnd)
}

func named(tag byte, name string, payload ...byte) []byte {
	encoded := encodeModifiedUTF8(name)
	entry := []byte{tag, byte(len(encoded) >> 8), byte(len(encoded))}
	entry = append(entry, encoded...)

	return append(entry, payload...)
}

func intPayload(value int32) []byte {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint32(payload, uint32(value))

	return payload
}

func nested(name string, entries ...[]byte) []byte {
	return named(TagCompound, name, compound(entries...)...)
}

func anonymous(t *testing.T, entries ...[]byte) NetworkNBT {
	t.Helper()

	value, err := NewNetworkNBT(append([]byte{TagCompound}, compound(entries...)...), bufferLimits(t))
	if err != nil {
		t.Fatalf("NewNetworkNBT: %v", err)
	}

	return value
}

func TestIntReadsAValueFromTheRootCompound(t *testing.T) {
	// The case that prompted this: a dimension type's minimum build height,
	// which decides where a chunk column's sections sit in the world.
	value := anonymous(t,
		named(TagInt, "height", intPayload(384)...),
		named(TagInt, "min_y", intPayload(-64)...),
	)

	minY, ok := value.Int("min_y")
	if !ok {
		t.Fatal("min_y was not found")
	}
	if minY != -64 {
		t.Errorf("min_y is %d, want -64", minY)
	}
	if height, ok := value.Int("height"); !ok || height != 384 {
		t.Errorf("height is %d, %v, want 384", height, ok)
	}
}

func TestIntFollowsAPathOfCompounds(t *testing.T) {
	value := anonymous(t, nested("outer", nested("inner", named(TagInt, "leaf", intPayload(7)...))))

	if got, ok := value.Int("outer", "inner", "leaf"); !ok || got != 7 {
		t.Errorf("got %d, %v, want 7", got, ok)
	}
}

func TestIntSkipsEveryTagOnTheWayToItsKey(t *testing.T) {
	// The walk has to step over whatever precedes the key, and a skip that
	// gets one tag's width wrong lands mid-value and reads a plausible number.
	// This puts one of every tag in front of the answer.
	value := anonymous(t,
		named(TagByte, "b", 1),
		named(TagShort, "s", 0, 2),
		named(TagInt, "i", intPayload(3)...),
		named(TagLong, "l", 0, 0, 0, 0, 0, 0, 0, 4),
		named(TagFloat, "f", 0, 0, 0, 5),
		named(TagDouble, "d", 0, 0, 0, 0, 0, 0, 0, 6),
		named(TagByteArray, "ba", 0, 0, 0, 2, 7, 8),
		named(TagString, "str", 0, 3, 'a', 'b', 'c'),
		named(TagList, "list", TagInt, 0, 0, 0, 2, 0, 0, 0, 9, 0, 0, 0, 10),
		named(TagIntArray, "ia", 0, 0, 0, 1, 0, 0, 0, 11),
		nested("c", named(TagByte, "inner", 12)),
		named(TagList, "compounds",
			append([]byte{TagCompound, 0, 0, 0, 1}, compound(named(TagByte, "x", 13))...)...),
		named(TagInt, "answer", intPayload(42)...),
	)

	if got, ok := value.Int("answer"); !ok || got != 42 {
		t.Errorf("got %d, %v, want 42", got, ok)
	}
}

func TestIntReportsAbsenceRatherThanZero(t *testing.T) {
	// A caller cannot tell a missing key from a real zero without the second
	// result, and min_y is legitimately zero in the nether.
	value := anonymous(t, named(TagInt, "min_y", intPayload(0)...))

	if got, ok := value.Int("min_y"); !ok || got != 0 {
		t.Errorf("a real zero read as %d, %v", got, ok)
	}
	for _, path := range [][]string{
		{"absent"},
		{"min_y", "deeper"},
		{"absent", "min_y"},
		{},
	} {
		if got, ok := value.Int(path...); ok {
			t.Errorf("path %v returned %d", path, got)
		}
	}
}

func TestIntRefusesATagThatIsNotAnInt(t *testing.T) {
	// A TAG_Long that reported its first four bytes as an int would be wrong
	// by a factor of four billion and look like a plausible answer.
	value := anonymous(t,
		named(TagLong, "long", 0, 0, 0, 0, 0, 0, 0, 1),
		named(TagString, "string", 0, 1, 'x'),
	)

	if got, ok := value.Int("long"); ok {
		t.Errorf("a long read as the int %d", got)
	}
	if got, ok := value.Int("string"); ok {
		t.Errorf("a string read as the int %d", got)
	}
}

func TestIntReadsTheNamedRootFormToo(t *testing.T) {
	// Protocol 47 uses the named root. The two encodings differ by exactly the
	// root name, so a walker that skips the wrong one reads the first entry's
	// tag as a name length.
	body := append([]byte{TagCompound, 0x00, 0x04}, "root"...)
	body = append(body, compound(named(TagInt, "min_y", intPayload(-64)...))...)

	value, err := NewNBT(body, bufferLimits(t))
	if err != nil {
		t.Fatalf("NewNBT: %v", err)
	}
	if got, ok := value.Int("min_y"); !ok || got != -64 {
		t.Errorf("got %d, %v, want -64", got, ok)
	}
}

func TestIntOnTheZeroValueIsAbsentRatherThanAPanic(t *testing.T) {
	var network NetworkNBT
	if _, ok := network.Int("min_y"); ok {
		t.Error("the zero NetworkNBT answered a lookup")
	}

	var named NBT
	if _, ok := named.Int("min_y"); ok {
		t.Error("the zero NBT answered a lookup")
	}
}
