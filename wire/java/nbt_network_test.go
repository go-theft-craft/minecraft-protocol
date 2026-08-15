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
