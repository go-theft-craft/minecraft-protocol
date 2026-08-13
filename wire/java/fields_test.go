package java

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
)

type oneByteReader struct {
	io.Reader
}

func (r oneByteReader) Read(data []byte) (int, error) {
	if len(data) > 1 {
		data = data[:1]
	}
	return r.Reader.Read(data)
}

type oneByteWriter struct {
	bytes.Buffer
}

func (w *oneByteWriter) Write(data []byte) (int, error) {
	if len(data) == 0 {
		return 0, nil
	}
	return w.Buffer.Write(data[:1])
}

type shortWriter struct{}

func (shortWriter) Write([]byte) (int, error) {
	return 0, nil
}

type countingReader struct {
	reader io.Reader
	reads  int
}

func (r *countingReader) Read(data []byte) (int, error) {
	r.reads++
	return r.reader.Read(data)
}

type countingWriter struct {
	writes int
}

func (w *countingWriter) Write(data []byte) (int, error) {
	w.writes++
	return len(data), nil
}

func testLimits(t *testing.T) protocol.Limits {
	t.Helper()

	limits, err := protocol.NewLimits(
		protocol.MaxStringBytes(4),
		protocol.MaxCollectionItems(4),
	)
	if err != nil {
		t.Fatal(err)
	}
	return limits
}

func TestPosition(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		x, y, z int
	}{
		{name: "origin"},
		{name: "positive", x: 100, y: 64, z: 200},
		{name: "negative", x: -100, y: -64, z: -200},
		{name: "maximum y", y: 2047},
		{name: "minimum y", y: -2048},
		{name: "mixed", x: -33554432, z: 33554431},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			encoded := EncodePosition(test.x, test.y, test.z)
			x, y, z := DecodePosition(encoded)
			if x != test.x || y != test.y || z != test.z {
				t.Fatalf("DecodePosition(EncodePosition(%d, %d, %d)) = (%d, %d, %d)", test.x, test.y, test.z, x, y, z)
			}

			var wire bytes.Buffer
			if _, err := WritePosition(&wire, test.x, test.y, test.z); err != nil {
				t.Fatalf("WritePosition() error = %v", err)
			}
			x, y, z, err := ReadPosition(oneByteReader{Reader: bytes.NewReader(wire.Bytes())})
			if err != nil {
				t.Fatalf("ReadPosition() error = %v", err)
			}
			if x != test.x || y != test.y || z != test.z {
				t.Errorf("ReadPosition(WritePosition(%d, %d, %d)) = (%d, %d, %d)", test.x, test.y, test.z, x, y, z)
			}
		})
	}
}

func TestFieldFixedWidth(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		write func(io.Writer) (int, error)
		read  func(io.Reader) (any, error)
		want  any
	}{
		{
			name:  "i8",
			write: func(w io.Writer) (int, error) { return WriteI8(w, -42) },
			read:  func(r io.Reader) (any, error) { return ReadI8(r) },
			want:  int8(-42),
		},
		{
			name:  "i16",
			write: func(w io.Writer) (int, error) { return WriteI16(w, -12345) },
			read:  func(r io.Reader) (any, error) { return ReadI16(r) },
			want:  int16(-12345),
		},
		{
			name:  "i32",
			write: func(w io.Writer) (int, error) { return WriteI32(w, -123456789) },
			read:  func(r io.Reader) (any, error) { return ReadI32(r) },
			want:  int32(-123456789),
		},
		{
			name:  "i64",
			write: func(w io.Writer) (int, error) { return WriteI64(w, -1234567890123456789) },
			read:  func(r io.Reader) (any, error) { return ReadI64(r) },
			want:  int64(-1234567890123456789),
		},
		{
			name:  "f32",
			write: func(w io.Writer) (int, error) { return WriteF32(w, -1.25) },
			read:  func(r io.Reader) (any, error) { return ReadF32(r) },
			want:  float32(-1.25),
		},
		{
			name:  "f64",
			write: func(w io.Writer) (int, error) { return WriteF64(w, 1.25) },
			read:  func(r io.Reader) (any, error) { return ReadF64(r) },
			want:  float64(1.25),
		},
		{
			name:  "true",
			write: func(w io.Writer) (int, error) { return WriteBool(w, true) },
			read:  func(r io.Reader) (any, error) { return ReadBool(r) },
			want:  true,
		},
		{
			name:  "false",
			write: func(w io.Writer) (int, error) { return WriteBool(w, false) },
			read:  func(r io.Reader) (any, error) { return ReadBool(r) },
			want:  false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var encoded bytes.Buffer
			if _, err := test.write(&encoded); err != nil {
				t.Fatalf("write error = %v", err)
			}
			got, err := test.read(oneByteReader{Reader: bytes.NewReader(encoded.Bytes())})
			if err != nil {
				t.Fatalf("read error = %v", err)
			}
			if got != test.want {
				t.Errorf("read = %v, want %v", got, test.want)
			}
		})
	}
}

func TestUUID(t *testing.T) {
	t.Parallel()

	want := [16]byte{0x12, 0x34, 0x56, 0x78, 0x9a, 0xbc, 0xde, 0xf0, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77, 0x88}
	var encoded bytes.Buffer
	if _, err := WriteUUID(&encoded, want); err != nil {
		t.Fatalf("WriteUUID() error = %v", err)
	}
	got, err := ReadUUID(oneByteReader{Reader: bytes.NewReader(encoded.Bytes())})
	if err != nil {
		t.Fatalf("ReadUUID() error = %v", err)
	}
	if got != want {
		t.Errorf("ReadUUID() = %x, want %x", got, want)
	}
}

func TestString(t *testing.T) {
	t.Parallel()

	limits := testLimits(t)
	t.Run("at limit", func(t *testing.T) {
		var encoded bytes.Buffer
		if _, err := WriteString(&encoded, limits, "four"); err != nil {
			t.Fatalf("WriteString() error = %v", err)
		}
		got, err := ReadString(oneByteReader{Reader: bytes.NewReader(encoded.Bytes())}, limits)
		if err != nil {
			t.Fatalf("ReadString() error = %v", err)
		}
		if got != "four" {
			t.Errorf("ReadString() = %q, want %q", got, "four")
		}
	})

	t.Run("write over limit", func(t *testing.T) {
		writer := &countingWriter{}
		_, err := WriteString(writer, limits, "five!")
		assertSizeError(t, err)
		if writer.writes != 0 {
			t.Errorf("WriteString() wrote %d times before rejecting the size", writer.writes)
		}
	})

	t.Run("negative length", func(t *testing.T) {
		_, err := ReadString(bytes.NewReader([]byte{0xff, 0xff, 0xff, 0xff, 0x0f}), limits)
		if err == nil {
			t.Fatal("ReadString() error = nil, want error")
		}
	})

	t.Run("read over limit", func(t *testing.T) {
		reader := &countingReader{reader: bytes.NewReader([]byte{0x05})}
		_, err := ReadString(reader, limits)
		assertSizeError(t, err)
		if reader.reads != 1 {
			t.Errorf("ReadString() made %d reads, want 1", reader.reads)
		}
	})
}

func TestByteArray(t *testing.T) {
	t.Parallel()

	limits := testLimits(t)
	t.Run("at limit", func(t *testing.T) {
		want := []byte{0x01, 0x02, 0x03, 0x04}
		var encoded bytes.Buffer
		if _, err := WriteByteArray(&encoded, limits, want); err != nil {
			t.Fatalf("WriteByteArray() error = %v", err)
		}
		got, err := ReadByteArray(oneByteReader{Reader: bytes.NewReader(encoded.Bytes())}, limits)
		if err != nil {
			t.Fatalf("ReadByteArray() error = %v", err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("ReadByteArray() = %x, want %x", got, want)
		}
	})

	t.Run("write over limit", func(t *testing.T) {
		writer := &countingWriter{}
		_, err := WriteByteArray(writer, limits, []byte{1, 2, 3, 4, 5})
		assertSizeError(t, err)
		if writer.writes != 0 {
			t.Errorf("WriteByteArray() wrote %d times before rejecting the size", writer.writes)
		}
	})

	t.Run("read over limit", func(t *testing.T) {
		reader := &countingReader{reader: bytes.NewReader([]byte{0x05})}
		_, err := ReadByteArray(reader, limits)
		assertSizeError(t, err)
		if reader.reads != 1 {
			t.Errorf("ReadByteArray() made %d reads, want 1", reader.reads)
		}
	})
}

func TestFieldInvalidLimits(t *testing.T) {
	t.Parallel()

	var invalid protocol.Limits
	reader := &countingReader{reader: bytes.NewReader(nil)}
	if _, err := ReadString(reader, invalid); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("ReadString() error = %v, want ErrInvalidLimits", err)
	}
	if reader.reads != 0 {
		t.Errorf("ReadString() made %d reads with invalid limits", reader.reads)
	}

	writer := &countingWriter{}
	if _, err := WriteByteArray(writer, invalid, nil); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("WriteByteArray() error = %v, want ErrInvalidLimits", err)
	}
	if writer.writes != 0 {
		t.Errorf("WriteByteArray() made %d writes with invalid limits", writer.writes)
	}
}

func TestFieldCompleteWrites(t *testing.T) {
	t.Parallel()

	limits := testLimits(t)
	writer := &oneByteWriter{}
	written, err := WriteString(writer, limits, "four")
	if err != nil {
		t.Fatalf("WriteString() error = %v", err)
	}
	if written != 5 {
		t.Errorf("WriteString() wrote %d bytes, want 5", written)
	}
	if got := writer.Bytes(); !bytes.Equal(got, []byte{0x04, 'f', 'o', 'u', 'r'}) {
		t.Errorf("WriteString() = %x, want 04666f7572", got)
	}

	if _, err := WriteI64(shortWriter{}, 1); !errors.Is(err, io.ErrShortWrite) {
		t.Errorf("WriteI64() error = %v, want io.ErrShortWrite", err)
	}
}

func assertSizeError(t *testing.T, err error) {
	t.Helper()

	if !errors.Is(err, ErrValueTooLarge) {
		t.Fatalf("error = %v, want ErrValueTooLarge", err)
	}

	var sizeError *SizeError
	if !errors.As(err, &sizeError) {
		t.Fatalf("error = %v, want *SizeError", err)
	}
}
