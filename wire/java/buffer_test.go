package java

import (
	"bytes"
	"errors"
	"io"
	"math"
	"strings"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

func bufferLimits(t *testing.T, options ...protocol.LimitOption) protocol.Limits {
	t.Helper()

	limits, err := protocol.NewLimits(options...)
	if err != nil {
		t.Fatal(err)
	}
	return limits
}

func TestBufferConstructorsValidateLimitsAndFrameSize(t *testing.T) {
	t.Parallel()

	if _, err := NewReadBuffer(nil, protocol.Limits{}); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("NewReadBuffer() error = %v, want ErrInvalidLimits", err)
	}
	if _, err := NewWriteBuffer(protocol.Limits{}); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("NewWriteBuffer() error = %v, want ErrInvalidLimits", err)
	}
	var zero Buffer
	if _, err := zero.ReadI8("packet.value"); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("zero Buffer.ReadI8() error = %v, want ErrInvalidLimits", err)
	}
	if err := zero.WriteI8("packet.value", 1); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("zero Buffer.WriteI8() error = %v, want ErrInvalidLimits", err)
	}

	limits := bufferLimits(t, protocol.MaxFrameBytes(2))
	if _, err := NewReadBuffer([]byte{1, 2, 3}, limits); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("NewReadBuffer() error = %v, want ErrFrameTooLarge", err)
	}
}

func TestBufferOwnsReadInputAndReturnedBytes(t *testing.T) {
	t.Parallel()

	payload := []byte{1, 2, 3}
	buffer, err := NewReadBuffer(payload, bufferLimits(t))
	if err != nil {
		t.Fatal(err)
	}
	payload[0] = 9

	got := buffer.Bytes()
	if !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Fatalf("Bytes() = %v, want [1 2 3]", got)
	}
	got[1] = 9
	if second := buffer.Bytes(); !bytes.Equal(second, []byte{1, 2, 3}) {
		t.Fatalf("second Bytes() = %v, want [1 2 3]", second)
	}
}

func TestBufferPrimitiveRoundTrip(t *testing.T) {
	t.Parallel()

	limits := bufferLimits(t)
	w, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatal(err)
	}

	uuid := UUID{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	writes := []struct {
		path string
		call func() error
	}{
		{"root.varint", func() error { return w.WriteVarInt("root.varint", -123456) }},
		{"root.varlong", func() error { return w.WriteVarLong("root.varlong", -1234567890123) }},
		{"root.i8", func() error { return w.WriteI8("root.i8", -12) }},
		{"root.u8", func() error { return w.WriteU8("root.u8", 250) }},
		{"root.i16", func() error { return w.WriteI16("root.i16", -1234) }},
		{"root.u16", func() error { return w.WriteU16("root.u16", 65000) }},
		{"root.i32", func() error { return w.WriteI32("root.i32", -12345678) }},
		{"root.u32", func() error { return w.WriteU32("root.u32", 4_000_000_000) }},
		{"root.i64", func() error { return w.WriteI64("root.i64", -1234567890123) }},
		{"root.u64", func() error { return w.WriteU64("root.u64", 18_000_000_000_000_000_000) }},
		{"root.f32", func() error { return w.WriteF32("root.f32", 1.25) }},
		{"root.f64", func() error { return w.WriteF64("root.f64", -2.5) }},
		{"root.false", func() error { return w.WriteBool("root.false", false) }},
		{"root.true", func() error { return w.WriteBool("root.true", true) }},
		{"root.uuid", func() error { return w.WriteUUID("root.uuid", uuid) }},
		{"root.string", func() error { return w.WriteString("root.string", "hello") }},
		{"root.bytes", func() error { return w.WriteByteArray("root.bytes", []byte{1, 2, 3}) }},
		{"root.fixed", func() error { return w.WriteBuffer("root.fixed", []byte{4, 5}) }},
		{"root.rest", func() error { return w.WriteRestBuffer("root.rest", []byte{6, 7}) }},
	}
	for _, write := range writes {
		if err := write.call(); err != nil {
			t.Fatalf("%s: %v", write.path, err)
		}
	}

	r, err := NewReadBuffer(w.Bytes(), limits)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := r.ReadVarInt("root.varint"); err != nil || got != -123456 {
		t.Errorf("ReadVarInt() = (%d, %v)", got, err)
	}
	if got, err := r.ReadVarLong("root.varlong"); err != nil || got != -1234567890123 {
		t.Errorf("ReadVarLong() = (%d, %v)", got, err)
	}
	if got, err := r.ReadI8("root.i8"); err != nil || got != -12 {
		t.Errorf("ReadI8() = (%d, %v)", got, err)
	}
	if got, err := r.ReadU8("root.u8"); err != nil || got != 250 {
		t.Errorf("ReadU8() = (%d, %v)", got, err)
	}
	if got, err := r.ReadI16("root.i16"); err != nil || got != -1234 {
		t.Errorf("ReadI16() = (%d, %v)", got, err)
	}
	if got, err := r.ReadU16("root.u16"); err != nil || got != 65000 {
		t.Errorf("ReadU16() = (%d, %v)", got, err)
	}
	if got, err := r.ReadI32("root.i32"); err != nil || got != -12345678 {
		t.Errorf("ReadI32() = (%d, %v)", got, err)
	}
	if got, err := r.ReadU32("root.u32"); err != nil || got != 4_000_000_000 {
		t.Errorf("ReadU32() = (%d, %v)", got, err)
	}
	if got, err := r.ReadI64("root.i64"); err != nil || got != -1234567890123 {
		t.Errorf("ReadI64() = (%d, %v)", got, err)
	}
	if got, err := r.ReadU64("root.u64"); err != nil || got != 18_000_000_000_000_000_000 {
		t.Errorf("ReadU64() = (%d, %v)", got, err)
	}
	if got, err := r.ReadF32("root.f32"); err != nil || got != 1.25 {
		t.Errorf("ReadF32() = (%v, %v)", got, err)
	}
	if got, err := r.ReadF64("root.f64"); err != nil || got != -2.5 {
		t.Errorf("ReadF64() = (%v, %v)", got, err)
	}
	if got, err := r.ReadBool("root.false"); err != nil || got {
		t.Errorf("ReadBool(false) = (%v, %v)", got, err)
	}
	if got, err := r.ReadBool("root.true"); err != nil || !got {
		t.Errorf("ReadBool(true) = (%v, %v)", got, err)
	}
	if got, err := r.ReadUUID("root.uuid"); err != nil || got != uuid {
		t.Errorf("ReadUUID() = (%v, %v)", got, err)
	}
	if got, err := r.ReadString("root.string"); err != nil || got != "hello" {
		t.Errorf("ReadString() = (%q, %v)", got, err)
	}
	if got, err := r.ReadByteArray("root.bytes"); err != nil || !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Errorf("ReadByteArray() = (%v, %v)", got, err)
	}
	if got, err := r.ReadBuffer("root.fixed", 2); err != nil || !bytes.Equal(got, []byte{4, 5}) {
		t.Errorf("ReadBuffer() = (%v, %v)", got, err)
	}
	if got, err := r.ReadRestBuffer("root.rest"); err != nil || !bytes.Equal(got, []byte{6, 7}) {
		t.Errorf("ReadRestBuffer() = (%v, %v)", got, err)
	}
	if r.Remaining() != 0 {
		t.Errorf("Remaining() = %d, want 0", r.Remaining())
	}
	if err := r.RequireEmpty("root"); err != nil {
		t.Errorf("RequireEmpty() error = %v", err)
	}
}

func TestBufferReportsEveryPrimitiveTruncationWithPath(t *testing.T) {
	t.Parallel()

	limits := bufferLimits(t)
	tests := []struct {
		name   string
		encode func(*Buffer) error
		decode func(*Buffer) error
	}{
		{"varint", func(b *Buffer) error { return b.WriteVarInt("write", math.MaxInt32) }, func(b *Buffer) error { _, err := b.ReadVarInt("packet.value"); return err }},
		{"varlong", func(b *Buffer) error { return b.WriteVarLong("write", math.MaxInt64) }, func(b *Buffer) error { _, err := b.ReadVarLong("packet.value"); return err }},
		{"i8", func(b *Buffer) error { return b.WriteI8("write", 1) }, func(b *Buffer) error { _, err := b.ReadI8("packet.value"); return err }},
		{"u8", func(b *Buffer) error { return b.WriteU8("write", 1) }, func(b *Buffer) error { _, err := b.ReadU8("packet.value"); return err }},
		{"i16", func(b *Buffer) error { return b.WriteI16("write", 1) }, func(b *Buffer) error { _, err := b.ReadI16("packet.value"); return err }},
		{"u16", func(b *Buffer) error { return b.WriteU16("write", 1) }, func(b *Buffer) error { _, err := b.ReadU16("packet.value"); return err }},
		{"i32", func(b *Buffer) error { return b.WriteI32("write", 1) }, func(b *Buffer) error { _, err := b.ReadI32("packet.value"); return err }},
		{"u32", func(b *Buffer) error { return b.WriteU32("write", 1) }, func(b *Buffer) error { _, err := b.ReadU32("packet.value"); return err }},
		{"i64", func(b *Buffer) error { return b.WriteI64("write", 1) }, func(b *Buffer) error { _, err := b.ReadI64("packet.value"); return err }},
		{"u64", func(b *Buffer) error { return b.WriteU64("write", 1) }, func(b *Buffer) error { _, err := b.ReadU64("packet.value"); return err }},
		{"f32", func(b *Buffer) error { return b.WriteF32("write", 1) }, func(b *Buffer) error { _, err := b.ReadF32("packet.value"); return err }},
		{"f64", func(b *Buffer) error { return b.WriteF64("write", 1) }, func(b *Buffer) error { _, err := b.ReadF64("packet.value"); return err }},
		{"bool", func(b *Buffer) error { return b.WriteBool("write", true) }, func(b *Buffer) error { _, err := b.ReadBool("packet.value"); return err }},
		{"uuid", func(b *Buffer) error { return b.WriteUUID("write", UUID{}) }, func(b *Buffer) error { _, err := b.ReadUUID("packet.value"); return err }},
		{"string", func(b *Buffer) error { return b.WriteString("write", "abc") }, func(b *Buffer) error { _, err := b.ReadString("packet.value"); return err }},
		{"byte array", func(b *Buffer) error { return b.WriteByteArray("write", []byte{1, 2, 3}) }, func(b *Buffer) error { _, err := b.ReadByteArray("packet.value"); return err }},
		{"fixed buffer", func(b *Buffer) error { return b.WriteBuffer("write", []byte{1, 2, 3}) }, func(b *Buffer) error { _, err := b.ReadBuffer("packet.value", 3); return err }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			w, err := NewWriteBuffer(limits)
			if err != nil {
				t.Fatal(err)
			}
			if err := test.encode(w); err != nil {
				t.Fatal(err)
			}
			encoded := w.Bytes()
			for cut := range len(encoded) {
				r, err := NewReadBuffer(encoded[:cut], limits)
				if err != nil {
					t.Fatal(err)
				}
				err = test.decode(r)
				if err == nil {
					t.Fatalf("cut %d/%d: error = nil", cut, len(encoded))
				}
				if !strings.Contains(err.Error(), "packet.value") {
					t.Errorf("cut %d/%d: error %q lacks field path", cut, len(encoded), err)
				}
			}
		})
	}
}

func TestBufferLengthAndPayloadLimits(t *testing.T) {
	t.Parallel()

	limits := bufferLimits(
		t,
		protocol.MaxFrameBytes(32),
		protocol.MaxStringBytes(3),
		protocol.MaxCollectionItems(2),
		protocol.MaxPluginBytes(2),
	)

	tests := []struct {
		name string
		data []byte
		call func(*Buffer) error
		want error
	}{
		{"negative string", []byte{0xff, 0xff, 0xff, 0xff, 0x0f}, func(b *Buffer) error { _, err := b.ReadString("packet.name"); return err }, ErrNegativeLength},
		{"long string", []byte{4, 'f', 'o', 'u', 'r'}, func(b *Buffer) error { _, err := b.ReadString("packet.name"); return err }, ErrValueTooLarge},
		{"negative byte array", []byte{0xff, 0xff, 0xff, 0xff, 0x0f}, func(b *Buffer) error { _, err := b.ReadByteArray("packet.data"); return err }, ErrNegativeLength},
		{"long byte array", []byte{3, 1, 2, 3}, func(b *Buffer) error { _, err := b.ReadByteArray("packet.data"); return err }, ErrValueTooLarge},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			buffer, err := NewReadBuffer(test.data, limits)
			if err != nil {
				t.Fatal(err)
			}
			err = test.call(buffer)
			if !errors.Is(err, test.want) || !strings.Contains(err.Error(), "packet.") {
				t.Fatalf("error = %v, want path and %v", err, test.want)
			}
		})
	}

	read, err := NewReadBuffer([]byte{1, 2, 3}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := read.ReadPluginPayload("packet.plugin"); !errors.Is(err, ErrValueTooLarge) {
		t.Fatalf("ReadPluginPayload() error = %v, want ErrValueTooLarge", err)
	}

	write, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []func() error{
		func() error { return write.WriteString("packet.name", "four") },
		func() error { return write.WriteByteArray("packet.data", []byte{1, 2, 3}) },
		func() error { return write.WritePluginPayload("packet.plugin", []byte{1, 2, 3}) },
		func() error { return write.ValidateCollection("packet.items", 3) },
	} {
		before := write.Bytes()
		err := call()
		if !errors.Is(err, ErrValueTooLarge) || !strings.Contains(err.Error(), "packet.") {
			t.Errorf("write error = %v, want path and ErrValueTooLarge", err)
		}
		if !bytes.Equal(write.Bytes(), before) {
			t.Errorf("failed write changed buffer from %x to %x", before, write.Bytes())
		}
	}
	if err := write.ValidateCollection("packet.items", -1); !errors.Is(err, ErrNegativeLength) {
		t.Errorf("ValidateCollection(-1) error = %v, want ErrNegativeLength", err)
	}

	countReader, err := NewReadBuffer([]byte{2}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := countReader.ReadCollectionLength("packet.items"); err != nil || got != 2 {
		t.Errorf("ReadCollectionLength() = (%d, %v), want (2, nil)", got, err)
	}
	countWriter, err := NewWriteBuffer(limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := countWriter.WriteCollectionLength("packet.items", 2); err != nil {
		t.Fatal(err)
	}
	if got := countWriter.Bytes(); !bytes.Equal(got, []byte{2}) {
		t.Errorf("WriteCollectionLength() = %x, want 02", got)
	}
}

func TestBufferWriteFrameLimitIsAtomicAndPathAware(t *testing.T) {
	t.Parallel()

	buffer, err := NewWriteBuffer(bufferLimits(t, protocol.MaxFrameBytes(2)))
	if err != nil {
		t.Fatal(err)
	}
	if err := buffer.WriteU8("packet.first", 1); err != nil {
		t.Fatal(err)
	}
	if err := buffer.WriteI16("packet.second", 2); !errors.Is(err, ErrFrameTooLarge) || !strings.Contains(err.Error(), "packet.second") {
		t.Fatalf("WriteI16() error = %v, want path and ErrFrameTooLarge", err)
	}
	if got := buffer.Bytes(); !bytes.Equal(got, []byte{1}) {
		t.Fatalf("Bytes() after rejected write = %x, want 01", got)
	}
}

func TestBufferRemainingAndTrailingBytes(t *testing.T) {
	t.Parallel()

	buffer, err := NewReadBuffer([]byte{1, 2}, bufferLimits(t))
	if err != nil {
		t.Fatal(err)
	}
	if buffer.Remaining() != 2 {
		t.Fatalf("Remaining() = %d, want 2", buffer.Remaining())
	}
	if _, err := buffer.ReadU8("packet.first"); err != nil {
		t.Fatal(err)
	}
	if err := buffer.RequireEmpty("packet"); !errors.Is(err, ErrTrailingBytes) || !strings.Contains(err.Error(), "packet") {
		t.Fatalf("RequireEmpty() error = %v, want path and ErrTrailingBytes", err)
	}
	if _, err := buffer.ReadBuffer("packet.invalid", -1); !errors.Is(err, ErrNegativeLength) {
		t.Errorf("ReadBuffer(-1) error = %v, want ErrNegativeLength", err)
	}
	if _, err := buffer.ReadBuffer("packet.short", 2); !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("ReadBuffer(2) error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestMapperAndBitfieldBoundaryFailuresRemainPathAware(t *testing.T) {
	t.Parallel()

	buffer, err := NewReadBuffer(nil, bufferLimits(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := buffer.ReadVarInt("packet.mapper"); err == nil || !strings.Contains(err.Error(), "packet.mapper") {
		t.Fatalf("mapper backing read error = %v, want mapper path", err)
	}
	// A packed bitfield reads through the 64-bit primitive, so the path a
	// generated bitfield reports comes from here.
	if _, err := buffer.ReadU64("packet.bitfield"); err == nil || !strings.Contains(err.Error(), "packet.bitfield") {
		t.Fatalf("bitfield backing read error = %v, want bitfield path", err)
	}
}
