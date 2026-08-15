package java

import (
	"bytes"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

// TestHolderBiasesRegistryIDsByOne is the whole point of the encoding: zero is
// reserved for "inline", so registry entry 0 has to travel as 1. Getting the
// bias backwards would shift every registry reference by one silently, which
// is why both directions are asserted explicitly.
func TestHolderBiasesRegistryIDsByOne(t *testing.T) {
	limits := bufferLimits(t)

	cases := []struct {
		name    string
		id      int32
		encoded []byte
	}{
		{name: "entry zero", id: 0, encoded: []byte{0x01}},
		{name: "entry one", id: 1, encoded: []byte{0x02}},
		{name: "a two-byte entry", id: 200, encoded: []byte{0xC9, 0x01}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writer, err := NewWriteBuffer(limits)
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.WriteHolder("holder", tc.id, false); err != nil {
				t.Fatalf("WriteHolder: %v", err)
			}
			if got := writer.Bytes(); !bytes.Equal(got, tc.encoded) {
				t.Fatalf("encoded % x, want % x", got, tc.encoded)
			}

			reader, err := NewReadBuffer(tc.encoded, limits)
			if err != nil {
				t.Fatal(err)
			}
			id, inline, err := reader.ReadHolder("holder")
			if err != nil {
				t.Fatalf("ReadHolder: %v", err)
			}
			if inline {
				t.Fatal("a registry reference decoded as inline")
			}
			if id != tc.id {
				t.Errorf("id = %d, want %d", id, tc.id)
			}
		})
	}
}

func TestHolderZeroSelectsTheInlineBranch(t *testing.T) {
	limits := bufferLimits(t)

	writer, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteHolder("holder", 0, true); err != nil {
		t.Fatalf("WriteHolder: %v", err)
	}
	if got := writer.Bytes(); !bytes.Equal(got, []byte{0x00}) {
		t.Fatalf("encoded % x, want 00", got)
	}

	reader, err := NewReadBuffer([]byte{0x00}, limits)
	if err != nil {
		t.Fatal(err)
	}
	id, inline, err := reader.ReadHolder("holder")
	if err != nil {
		t.Fatalf("ReadHolder: %v", err)
	}
	if !inline || id != 0 {
		t.Errorf("id = %d, inline = %v, want an inline value", id, inline)
	}
}

func TestHolderRejectsANegativeID(t *testing.T) {
	writer, err := NewWriteBuffer(bufferLimits(t))
	if err != nil {
		t.Fatal(err)
	}

	if err := writer.WriteHolder("holder", -1, false); err == nil {
		t.Fatal("a negative registry ID was accepted")
	}
}

// TestHolderSetBiasesCountsByOne covers the other discriminator: zero means a
// tag name follows, so an explicit set of n entries travels as n+1 and an empty
// explicit set is still distinguishable from a tag.
func TestHolderSetBiasesCountsByOne(t *testing.T) {
	limits := bufferLimits(t)

	cases := []struct {
		name    string
		count   int
		encoded []byte
	}{
		{name: "an empty explicit set", count: 0, encoded: []byte{0x01}},
		{name: "one entry", count: 1, encoded: []byte{0x02}},
		{name: "three entries", count: 3, encoded: []byte{0x04}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writer, err := NewWriteBuffer(limits)
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.WriteHolderSet("set", tc.count, false); err != nil {
				t.Fatalf("WriteHolderSet: %v", err)
			}
			if got := writer.Bytes(); !bytes.Equal(got, tc.encoded) {
				t.Fatalf("encoded % x, want % x", got, tc.encoded)
			}

			reader, err := NewReadBuffer(tc.encoded, limits)
			if err != nil {
				t.Fatal(err)
			}
			count, tagged, err := reader.ReadHolderSet("set")
			if err != nil {
				t.Fatalf("ReadHolderSet: %v", err)
			}
			if tagged {
				t.Fatal("an explicit set decoded as a tag")
			}
			if count != tc.count {
				t.Errorf("count = %d, want %d", count, tc.count)
			}
		})
	}
}

func TestHolderSetZeroSelectsATagName(t *testing.T) {
	limits := bufferLimits(t)

	writer, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteHolderSet("set", 0, true); err != nil {
		t.Fatalf("WriteHolderSet: %v", err)
	}
	if err := writer.WriteString("set.name", "minecraft:logs"); err != nil {
		t.Fatalf("WriteString: %v", err)
	}

	reader, err := NewReadBuffer(writer.Bytes(), limits)
	if err != nil {
		t.Fatal(err)
	}
	count, tagged, err := reader.ReadHolderSet("set")
	if err != nil {
		t.Fatalf("ReadHolderSet: %v", err)
	}
	if !tagged || count != 0 {
		t.Fatalf("count = %d, tagged = %v, want a tag", count, tagged)
	}
	tag, err := reader.ReadString("set.name")
	if err != nil {
		t.Fatalf("ReadString: %v", err)
	}
	if tag != "minecraft:logs" {
		t.Errorf("tag = %q", tag)
	}
}

// TestHolderSetBoundsItsCountBeforeAllocating proves the count is checked
// against the configured limit at decode time, not after a slice that size has
// already been made.
func TestHolderSetBoundsItsCountBeforeAllocating(t *testing.T) {
	limits := bufferLimits(t, protocol.MaxCollectionItems(4))

	// A count of 8 travels as 9.
	reader, err := NewReadBuffer([]byte{0x09}, limits)
	if err != nil {
		t.Fatal(err)
	}

	if _, _, err := reader.ReadHolderSet("set"); err == nil {
		t.Fatal("a count past the collection limit was accepted")
	}
}

func TestHolderGenericsCarryEitherBranch(t *testing.T) {
	byID := NewHolderID[string](7)
	if byID.Inline != nil || byID.ID != 7 {
		t.Errorf("holder = %+v, want registry entry 7", byID)
	}

	inline := NewHolderInline("value")
	if inline.Inline == nil || *inline.Inline != "value" {
		t.Errorf("holder = %+v, want an inline value", inline)
	}

	tagged := NewHolderSetTag[int32]("minecraft:logs")
	if tagged.IDs != nil || tagged.Tag != "minecraft:logs" {
		t.Errorf("set = %+v, want a tag", tagged)
	}

	// An empty explicit set is not the same as a tag, so the constructor keeps
	// the slice non-nil.
	empty := NewHolderSetIDs[int32](nil)
	if empty.IDs == nil {
		t.Error("an empty explicit set decayed into a tag")
	}
}
