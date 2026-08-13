package java

import (
	"bytes"
	"errors"
	"math"
	"testing"
)

func TestVarInt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value int32
		want  []byte
	}{
		{name: "zero", value: 0, want: []byte{0x00}},
		{name: "one", value: 1, want: []byte{0x01}},
		{name: "127", value: 127, want: []byte{0x7f}},
		{name: "128", value: 128, want: []byte{0x80, 0x01}},
		{name: "255", value: 255, want: []byte{0xff, 0x01}},
		{name: "25565", value: 25565, want: []byte{0xdd, 0xc7, 0x01}},
		{name: "maximum", value: math.MaxInt32, want: []byte{0xff, 0xff, 0xff, 0xff, 0x07}},
		{name: "minus one", value: -1, want: []byte{0xff, 0xff, 0xff, 0xff, 0x0f}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var encoded bytes.Buffer
			written, err := WriteVarInt(&encoded, test.value)
			if err != nil {
				t.Fatalf("WriteVarInt(%d) error = %v", test.value, err)
			}
			if written != len(test.want) {
				t.Fatalf("WriteVarInt(%d) wrote %d bytes, want %d", test.value, written, len(test.want))
			}
			if got := encoded.Bytes(); !bytes.Equal(got, test.want) {
				t.Fatalf("WriteVarInt(%d) = %x, want %x", test.value, got, test.want)
			}
			if got := VarIntSize(test.value); got != len(test.want) {
				t.Fatalf("VarIntSize(%d) = %d, want %d", test.value, got, len(test.want))
			}

			var put [5]byte
			if got := PutVarInt(put[:], test.value); got != len(test.want) {
				t.Fatalf("PutVarInt(%d) = %d, want %d", test.value, got, len(test.want))
			}
			if got := put[:len(test.want)]; !bytes.Equal(got, test.want) {
				t.Fatalf("PutVarInt(%d) = %x, want %x", test.value, got, test.want)
			}

			value, read, err := ReadVarInt(oneByteReader{Reader: bytes.NewReader(test.want)})
			if err != nil {
				t.Fatalf("ReadVarInt() error = %v", err)
			}
			if read != len(test.want) {
				t.Errorf("ReadVarInt() read %d bytes, want %d", read, len(test.want))
			}
			if value != test.value {
				t.Errorf("ReadVarInt() = %d, want %d", value, test.value)
			}
		})
	}
}

func TestVarIntTooLong(t *testing.T) {
	t.Parallel()

	_, read, err := ReadVarInt(bytes.NewReader([]byte{0x80, 0x80, 0x80, 0x80, 0x80, 0x80}))
	if !errors.Is(err, ErrVarIntTooLong) {
		t.Fatalf("ReadVarInt() error = %v, want ErrVarIntTooLong", err)
	}
	if read != 5 {
		t.Errorf("ReadVarInt() read %d bytes, want 5", read)
	}
}

func TestVarLong(t *testing.T) {
	t.Parallel()

	tests := []int64{0, 1, 127, 128, 25565, math.MaxInt64, -1}
	for _, test := range tests {
		t.Run("round trip", func(t *testing.T) {
			var encoded bytes.Buffer
			written, err := WriteVarLong(&encoded, test)
			if err != nil {
				t.Fatalf("WriteVarLong(%d) error = %v", test, err)
			}

			value, read, err := ReadVarLong(oneByteReader{Reader: bytes.NewReader(encoded.Bytes())})
			if err != nil {
				t.Fatalf("ReadVarLong() error = %v", err)
			}
			if read != written {
				t.Errorf("ReadVarLong() read %d bytes, want %d", read, written)
			}
			if value != test {
				t.Errorf("ReadVarLong() = %d, want %d", value, test)
			}
		})
	}
}

func TestVarLongTooLong(t *testing.T) {
	t.Parallel()

	_, read, err := ReadVarLong(bytes.NewReader(bytes.Repeat([]byte{0x80}, 11)))
	if !errors.Is(err, ErrVarLongTooLong) {
		t.Fatalf("ReadVarLong() error = %v, want ErrVarLongTooLong", err)
	}
	if read != 10 {
		t.Errorf("ReadVarLong() read %d bytes, want 10", read)
	}
}
