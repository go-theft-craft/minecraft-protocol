package java

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
)

func frameLimits(t *testing.T, frameBytes int) protocol.Limits {
	t.Helper()

	limits, err := protocol.NewLimits(protocol.MaxFrameBytes(frameBytes))
	if err != nil {
		t.Fatal(err)
	}
	return limits
}

func TestReadRawPacketRoundTrip(t *testing.T) {
	t.Parallel()

	limits := frameLimits(t, 16)
	tests := []struct {
		name string
		want protocol.Packet
	}{
		{
			name: "empty body",
			want: protocol.Packet{ID: 0x00},
		},
		{
			name: "multi byte packet ID",
			want: protocol.Packet{
				ID:      0x80,
				Payload: []byte{0xaa, 0xbb},
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var wire bytes.Buffer
			if err := WriteRawPacket(&wire, limits, test.want); err != nil {
				t.Fatalf("WriteRawPacket() error = %v", err)
			}

			got, err := ReadRawPacket(&wire, limits)
			if err != nil {
				t.Fatalf("ReadRawPacket() error = %v", err)
			}
			if got.ID != test.want.ID {
				t.Errorf("ReadRawPacket() ID = %d, want %d", got.ID, test.want.ID)
			}
			if !bytes.Equal(got.Payload, test.want.Payload) {
				t.Errorf("ReadRawPacket() Payload = %x, want %x", got.Payload, test.want.Payload)
			}
			if got.State != "" || got.Direction != 0 || got.Name != "" || got.Value != nil {
				t.Errorf("ReadRawPacket() populated envelope metadata: %+v", got)
			}
		})
	}
}

func TestReadRawPacketOneByteReader(t *testing.T) {
	t.Parallel()

	limits := frameLimits(t, 16)
	want := protocol.Packet{
		ID:      0x01,
		Payload: []byte{0xaa, 0xbb},
	}
	var encoded bytes.Buffer
	if err := WriteRawPacket(&encoded, limits, want); err != nil {
		t.Fatalf("WriteRawPacket() error = %v", err)
	}

	got, err := ReadRawPacket(oneByteReader{Reader: bytes.NewReader(encoded.Bytes())}, limits)
	if err != nil {
		t.Fatalf("ReadRawPacket() error = %v", err)
	}
	if got.ID != want.ID || !bytes.Equal(got.Payload, want.Payload) {
		t.Errorf("ReadRawPacket() = %+v, want %+v", got, want)
	}
}

func TestReadRawPacketRejectsInvalidLengths(t *testing.T) {
	t.Parallel()

	limits := frameLimits(t, 4)
	t.Run("zero", func(t *testing.T) {
		if _, err := ReadRawPacket(bytes.NewReader([]byte{0x00}), limits); err == nil {
			t.Fatal("ReadRawPacket() error = nil, want error")
		}
	})
	t.Run("negative", func(t *testing.T) {
		var declared [5]byte
		length := PutVarInt(declared[:], -1)
		_, err := ReadRawPacket(bytes.NewReader(declared[:length]), limits)
		if !errors.Is(err, ErrNegativeLength) {
			t.Fatalf("ReadRawPacket() error = %v, want ErrNegativeLength", err)
		}
	})
}

func TestReadRawPacketRejectsOversizedFrameBeforePayload(t *testing.T) {
	t.Parallel()

	limits := frameLimits(t, 4)
	reader := &countingReader{reader: bytes.NewReader([]byte{0x05})}
	_, err := ReadRawPacket(reader, limits)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("ReadRawPacket() error = %v, want ErrFrameTooLarge", err)
	}
	if reader.reads != 1 {
		t.Errorf("ReadRawPacket() made %d reads, want 1", reader.reads)
	}
}

func TestReadRawPacketTruncatedPayload(t *testing.T) {
	t.Parallel()

	_, err := ReadRawPacket(bytes.NewReader([]byte{0x02, 0x00}), frameLimits(t, 4))
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("ReadRawPacket() error = %v, want io.ErrUnexpectedEOF", err)
	}
}

func TestReadRawPacketRejectsOverlongPacketID(t *testing.T) {
	t.Parallel()

	encoded := append([]byte{0x05}, bytes.Repeat([]byte{0x80}, 5)...)
	_, err := ReadRawPacket(bytes.NewReader(encoded), frameLimits(t, 8))
	if !errors.Is(err, ErrVarIntTooLong) {
		t.Fatalf("ReadRawPacket() error = %v, want ErrVarIntTooLong", err)
	}
}

func TestWriteRawPacket(t *testing.T) {
	t.Parallel()

	limits := frameLimits(t, 4)
	packet := protocol.Packet{
		State:     "play",
		Direction: protocol.DirectionClientbound,
		ID:        0x01,
		Name:      "ignored",
		Value:     struct{}{},
		Payload:   []byte{0xaa, 0xbb},
	}

	t.Run("one byte writer", func(t *testing.T) {
		var writer oneByteWriter
		if err := WriteRawPacket(&writer, limits, packet); err != nil {
			t.Fatalf("WriteRawPacket() error = %v", err)
		}
		if got, want := writer.Bytes(), []byte{0x03, 0x01, 0xaa, 0xbb}; !bytes.Equal(got, want) {
			t.Errorf("WriteRawPacket() = %x, want %x", got, want)
		}
	})

	t.Run("short writer", func(t *testing.T) {
		if err := WriteRawPacket(shortWriter{}, limits, packet); !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("WriteRawPacket() error = %v, want io.ErrShortWrite", err)
		}
	})

	t.Run("packet over limit before write", func(t *testing.T) {
		writer := &countingWriter{}
		err := WriteRawPacket(writer, frameLimits(t, 2), packet)
		if !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("WriteRawPacket() error = %v, want ErrFrameTooLarge", err)
		}
		if writer.writes != 0 {
			t.Errorf("WriteRawPacket() wrote %d times before rejecting the frame", writer.writes)
		}
	})
}

func TestRawPacketInvalidLimitsDoNotTouchIO(t *testing.T) {
	t.Parallel()

	var invalid protocol.Limits
	reader := &countingReader{reader: bytes.NewReader(nil)}
	if _, err := ReadRawPacket(reader, invalid); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("ReadRawPacket() error = %v, want ErrInvalidLimits", err)
	}
	if reader.reads != 0 {
		t.Errorf("ReadRawPacket() made %d reads with invalid limits", reader.reads)
	}

	writer := &countingWriter{}
	if err := WriteRawPacket(writer, invalid, protocol.Packet{}); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("WriteRawPacket() error = %v, want ErrInvalidLimits", err)
	}
	if writer.writes != 0 {
		t.Errorf("WriteRawPacket() made %d writes with invalid limits", writer.writes)
	}
}
