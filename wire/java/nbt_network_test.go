package java

import (
	"bytes"
	"errors"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

// anonymousCompound is TAG_Compound, then one TAG_Byte named "a" holding 1,
// then TAG_End. The root carries no name, which is the whole difference from
// the 1.8 form.
var anonymousCompound = []byte{
	0x0A,
	0x01, 0x00, 0x01, 'a', 0x01,
	0x00,
}

// namedCompound is the same value in the 1.8 form: the root compound is
// preceded by an empty name.
var namedCompound = []byte{
	0x0A, 0x00, 0x00,
	0x01, 0x00, 0x01, 'a', 0x01,
	0x00,
}

func TestAnonymousNBTRoundTripsBytes(t *testing.T) {
	limits := bufferLimits(t)

	reader, err := NewReadBuffer(anonymousCompound, limits)
	if err != nil {
		t.Fatal(err)
	}
	value, err := reader.ReadAnonymousNBT("tag")
	if err != nil {
		t.Fatalf("ReadAnonymousNBT: %v", err)
	}
	if reader.Remaining() != 0 {
		t.Errorf("%d bytes left unread", reader.Remaining())
	}

	writer, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteAnonymousNBT("tag", value); err != nil {
		t.Fatalf("WriteAnonymousNBT: %v", err)
	}
	if got := writer.Bytes(); !bytes.Equal(got, anonymousCompound) {
		t.Errorf("re-encoded % x, want % x", got, anonymousCompound)
	}
}

// TestAnonymousNBTRejectsANamedRoot is the case that makes the two forms
// distinguishable at all. Read anonymously, the root name's length prefix is a
// TAG_End that closes the compound early, leaving the rest unread.
func TestAnonymousNBTRejectsANamedRoot(t *testing.T) {
	limits := bufferLimits(t)

	if _, err := NewNetworkNBT(namedCompound, limits); err == nil {
		t.Fatal("a named-root payload was accepted as network NBT")
	} else if !errors.Is(err, ErrTrailingBytes) {
		t.Errorf("error = %v, want trailing bytes", err)
	}
}

func TestAnonymousNBTRejectsANonCompoundRoot(t *testing.T) {
	limits := bufferLimits(t)

	if _, err := NewNetworkNBT([]byte{0x01, 0x00}, limits); err == nil {
		t.Fatal("a TAG_Byte root was accepted")
	} else if !errors.Is(err, ErrInvalidNBT) {
		t.Errorf("error = %v, want invalid NBT", err)
	}
}

func TestAnonymousNBTRejectsAPayloadOverTheByteLimit(t *testing.T) {
	limits := bufferLimits(t, protocol.MaxNBTBytes(4))

	if _, err := NewNetworkNBT(anonymousCompound, limits); err == nil {
		t.Fatal("an oversized payload was accepted")
	}
}

// TestAnonymousNBTRejectsNestingPastTheDepthLimit builds a chain of compounds
// deeper than the configured limit, so the bound is proved rather than assumed.
func TestAnonymousNBTRejectsNestingPastTheDepthLimit(t *testing.T) {
	limits := bufferLimits(t, protocol.MaxRecursionDepth(4))

	const depth = 8
	nested := []byte{0x0A}
	for range depth {
		nested = append(nested, 0x0A, 0x00, 0x01, 'n')
	}
	for range depth + 1 {
		nested = append(nested, 0x00)
	}

	if _, err := NewNetworkNBT(nested, limits); err == nil {
		t.Fatal("a payload nested past the limit was accepted")
	} else if !errors.Is(err, ErrRecursionLimit) {
		t.Errorf("error = %v, want a recursion limit", err)
	}
}

func TestAnonOptionalNBTEncodesAbsenceAsOneZeroByte(t *testing.T) {
	limits := bufferLimits(t)

	reader, err := NewReadBuffer([]byte{0x00}, limits)
	if err != nil {
		t.Fatal(err)
	}
	value, err := reader.ReadAnonOptionalNBT("tag")
	if err != nil {
		t.Fatalf("ReadAnonOptionalNBT: %v", err)
	}
	if value != nil {
		t.Fatalf("value = %v, want nil", value)
	}

	writer, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteAnonOptionalNBT("tag", nil); err != nil {
		t.Fatalf("WriteAnonOptionalNBT: %v", err)
	}
	if got := writer.Bytes(); !bytes.Equal(got, []byte{0x00}) {
		t.Errorf("re-encoded % x, want 00", got)
	}
}

func TestAnonOptionalNBTRoundTripsAPresentValue(t *testing.T) {
	limits := bufferLimits(t)

	reader, err := NewReadBuffer(anonymousCompound, limits)
	if err != nil {
		t.Fatal(err)
	}
	value, err := reader.ReadAnonOptionalNBT("tag")
	if err != nil {
		t.Fatalf("ReadAnonOptionalNBT: %v", err)
	}
	if value == nil {
		t.Fatal("value = nil, want a compound")
	}

	writer, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteAnonOptionalNBT("tag", value); err != nil {
		t.Fatalf("WriteAnonOptionalNBT: %v", err)
	}
	if got := writer.Bytes(); !bytes.Equal(got, anonymousCompound) {
		t.Errorf("re-encoded % x, want % x", got, anonymousCompound)
	}
}

// TestNetworkNBTTextRoundTrips covers the encoder the generated disconnect
// depends on: the component it builds must read back as network NBT, and the
// modified UTF-8 it writes must be the encoding NBT strings actually use.
func TestNetworkNBTTextRoundTrips(t *testing.T) {
	limits := bufferLimits(t)

	tests := []struct {
		name string
		text string
		want []byte
	}{
		{
			name: "ascii",
			text: "bye",
			want: []byte{
				0x0a,
				0x08, 0x00, 0x04, 't', 'e', 'x', 't',
				0x00, 0x03, 'b', 'y', 'e',
				0x00,
			},
		},
		{
			// NUL is two bytes in modified UTF-8, which is the whole point of
			// the encoding: a string can carry one without ending early.
			name: "a NUL byte",
			text: "a\x00b",
			want: []byte{
				0x0a,
				0x08, 0x00, 0x04, 't', 'e', 'x', 't',
				0x00, 0x04, 'a', 0xc0, 0x80, 'b',
				0x00,
			},
		},
		{
			// A supplementary code point is a surrogate pair of three-byte
			// sequences, not one four-byte sequence.
			name: "a supplementary code point",
			text: "\U0001F600",
			want: []byte{
				0x0a,
				0x08, 0x00, 0x04, 't', 'e', 'x', 't',
				0x00, 0x06, 0xed, 0xa0, 0xbd, 0xed, 0xb8, 0x80,
				0x00,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value, err := NewNetworkNBTText(test.text, limits)
			if err != nil {
				t.Fatalf("NewNetworkNBTText: %v", err)
			}
			if got := value.Bytes(); !bytes.Equal(got, test.want) {
				t.Fatalf("encoded % x, want % x", got, test.want)
			}

			writer, err := NewWriteBuffer(limits)
			if err != nil {
				t.Fatal(err)
			}
			if err := writer.WriteAnonymousNBT("reason", value); err != nil {
				t.Fatalf("WriteAnonymousNBT: %v", err)
			}
			reader, err := NewReadBuffer(writer.Bytes(), limits)
			if err != nil {
				t.Fatal(err)
			}
			read, err := reader.ReadAnonymousNBT("reason")
			if err != nil {
				t.Fatalf("ReadAnonymousNBT: %v", err)
			}
			if !bytes.Equal(read.Bytes(), test.want) {
				t.Errorf("read back % x, want % x", read.Bytes(), test.want)
			}
		})
	}
}
