package java

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
)

func newTestFramer(t *testing.T, frameBytes int) protocol.Framer {
	t.Helper()

	frameFramer, err := NewFramer(frameLimits(t, frameBytes))
	if err != nil {
		t.Fatalf("NewFramer() error = %v", err)
	}
	return frameFramer
}

func TestNewFramerRejectsInvalidLimits(t *testing.T) {
	t.Parallel()

	if _, err := NewFramer(protocol.Limits{}); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("NewFramer() error = %v, want ErrInvalidLimits", err)
	}
}

func TestFramerReadFrameRetainsLengthPrefix(t *testing.T) {
	t.Parallel()

	wire := []byte{0x03, 0x01, 0xaa, 0xbb}
	frame, err := newTestFramer(t, 16).ReadFrame(bytes.NewReader(wire))
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}

	if !bytes.Equal(frame.WireBytes(), wire) {
		t.Errorf("WireBytes() = %x, want %x", frame.WireBytes(), wire)
	}
	if want := wire[1:]; !bytes.Equal(frame.Payload(), want) {
		t.Errorf("Payload() = %x, want %x", frame.Payload(), want)
	}
}

func TestFramerReadFrameRejectsMalformedLengths(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		input []byte
		want  error
	}{
		{name: "empty frame", input: []byte{0x00}, want: nil},
		{name: "negative length", input: []byte{0xff, 0xff, 0xff, 0xff, 0x0f}, want: ErrNegativeLength},
		{name: "overlong length", input: []byte{0x83, 0x80, 0x80, 0x80, 0x00}, want: ErrVarIntTooLong},
		{name: "unterminated length", input: bytes.Repeat([]byte{0x80}, 5), want: ErrVarIntTooLong},
		{name: "truncated length", input: []byte{0x80}, want: io.ErrUnexpectedEOF},
		{name: "no bytes", input: nil, want: io.EOF},
		{name: "truncated payload", input: []byte{0x04, 0x01}, want: io.ErrUnexpectedEOF},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := newTestFramer(t, 16).ReadFrame(bytes.NewReader(testCase.input))
			if err == nil {
				t.Fatal("ReadFrame() error = nil, want an error")
			}
			if testCase.want != nil && !errors.Is(err, testCase.want) {
				t.Fatalf("ReadFrame() error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestFramerReadFrameRejectsOversizedFrameBeforePayload(t *testing.T) {
	t.Parallel()

	reader := &countingReader{reader: bytes.NewReader([]byte{0x05})}
	_, err := newTestFramer(t, 4).ReadFrame(reader)
	if !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("ReadFrame() error = %v, want ErrFrameTooLarge", err)
	}
	if reader.reads != 1 {
		t.Errorf("ReadFrame() made %d reads before rejecting, want 1", reader.reads)
	}
}

func TestFramerReadFrameOneByteReader(t *testing.T) {
	t.Parallel()

	wire := []byte{0x03, 0x01, 0xaa, 0xbb}
	frame, err := newTestFramer(t, 16).ReadFrame(oneByteReader{Reader: bytes.NewReader(wire)})
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	if !bytes.Equal(frame.WireBytes(), wire) {
		t.Errorf("WireBytes() = %x, want %x", frame.WireBytes(), wire)
	}
}

func TestFramerReadFrameOwnsItsBuffer(t *testing.T) {
	t.Parallel()

	source := []byte{0x02, 0x01, 0xaa}
	frame, err := newTestFramer(t, 16).ReadFrame(bytes.NewReader(source))
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}

	source[2] = 0xff
	if frame.Payload()[1] != 0xaa {
		t.Fatal("ReadFrame() aliased the caller's buffer, want an owned copy")
	}
}

func TestFramerBuildFrame(t *testing.T) {
	t.Parallel()

	frame, err := newTestFramer(t, 16).BuildFrame([]byte{0x01, 0xaa, 0xbb})
	if err != nil {
		t.Fatalf("BuildFrame() error = %v", err)
	}
	if want := []byte{0x03, 0x01, 0xaa, 0xbb}; !bytes.Equal(frame.WireBytes(), want) {
		t.Errorf("WireBytes() = %x, want %x", frame.WireBytes(), want)
	}
	if want := []byte{0x01, 0xaa, 0xbb}; !bytes.Equal(frame.Payload(), want) {
		t.Errorf("Payload() = %x, want %x", frame.Payload(), want)
	}
}

func TestFramerBuildFrameRejectsInvalidPayloads(t *testing.T) {
	t.Parallel()

	frameFramer := newTestFramer(t, 4)

	if _, err := frameFramer.BuildFrame(nil); err == nil {
		t.Fatal("BuildFrame(nil) error = nil, want an error")
	}
	if _, err := frameFramer.BuildFrame(bytes.Repeat([]byte{0x00}, 5)); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("BuildFrame(oversized) error = %v, want ErrFrameTooLarge", err)
	}
}

func TestFramerWriteFrame(t *testing.T) {
	t.Parallel()

	frameFramer := newTestFramer(t, 16)
	frame, err := frameFramer.BuildFrame([]byte{0x01, 0xaa, 0xbb})
	if err != nil {
		t.Fatalf("BuildFrame() error = %v", err)
	}

	t.Run("one byte writer", func(t *testing.T) {
		var writer oneByteWriter
		if err := frameFramer.WriteFrame(&writer, frame); err != nil {
			t.Fatalf("WriteFrame() error = %v", err)
		}
		if want := []byte{0x03, 0x01, 0xaa, 0xbb}; !bytes.Equal(writer.Bytes(), want) {
			t.Errorf("WriteFrame() wrote %x, want %x", writer.Bytes(), want)
		}
	})

	t.Run("short writer", func(t *testing.T) {
		if err := frameFramer.WriteFrame(shortWriter{}, frame); !errors.Is(err, io.ErrShortWrite) {
			t.Fatalf("WriteFrame() error = %v, want io.ErrShortWrite", err)
		}
	})

	t.Run("oversized frame does not reach the transport", func(t *testing.T) {
		writer := &countingWriter{}
		if err := newTestFramer(t, 2).WriteFrame(writer, frame); !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("WriteFrame() error = %v, want ErrFrameTooLarge", err)
		}
		if writer.writes != 0 {
			t.Errorf("WriteFrame() wrote %d times before rejecting the frame", writer.writes)
		}
	})

	t.Run("empty frame", func(t *testing.T) {
		writer := &countingWriter{}
		if err := frameFramer.WriteFrame(writer, protocol.Frame{}); err == nil {
			t.Fatal("WriteFrame(zero frame) error = nil, want an error")
		}
		if writer.writes != 0 {
			t.Errorf("WriteFrame() wrote %d times for a zero frame", writer.writes)
		}
	})
}

func TestFramerWriteFramePartialWriteReportsFailure(t *testing.T) {
	t.Parallel()

	frameFramer := newTestFramer(t, 16)
	frame, err := frameFramer.BuildFrame([]byte{0x01, 0xaa, 0xbb})
	if err != nil {
		t.Fatalf("BuildFrame() error = %v", err)
	}

	sentinel := errors.New("transport failed")
	writer := &partialWriter{limit: 2, err: sentinel}
	if err := frameFramer.WriteFrame(writer, frame); !errors.Is(err, sentinel) {
		t.Fatalf("WriteFrame() error = %v, want the transport error", err)
	}
	if writer.written != 2 {
		t.Errorf("WriteFrame() wrote %d bytes, want 2", writer.written)
	}
}

func TestSplitPacketBody(t *testing.T) {
	t.Parallel()

	t.Run("multi byte packet ID", func(t *testing.T) {
		packet, err := SplitPacketBody([]byte{0x80, 0x01, 0xaa})
		if err != nil {
			t.Fatalf("SplitPacketBody() error = %v", err)
		}
		if packet.ID != 0x80 {
			t.Errorf("ID = %#x, want %#x", packet.ID, 0x80)
		}
		if want := []byte{0xaa}; !bytes.Equal(packet.Payload, want) {
			t.Errorf("Payload = %x, want %x", packet.Payload, want)
		}
	})

	t.Run("empty body", func(t *testing.T) {
		if _, err := SplitPacketBody(nil); !errors.Is(err, io.EOF) {
			t.Fatalf("SplitPacketBody(nil) error = %v, want io.EOF", err)
		}
	})

	t.Run("overlong packet ID", func(t *testing.T) {
		if _, err := SplitPacketBody(bytes.Repeat([]byte{0x80}, 5)); !errors.Is(err, ErrVarIntTooLong) {
			t.Fatalf("SplitPacketBody() error = %v, want ErrVarIntTooLong", err)
		}
	})
}

func TestJoinPacketBody(t *testing.T) {
	t.Parallel()

	limits := frameLimits(t, 8)

	body, err := JoinPacketBody(protocol.Packet{ID: 0x80, Payload: []byte{0xaa}}, limits)
	if err != nil {
		t.Fatalf("JoinPacketBody() error = %v", err)
	}
	if want := []byte{0x80, 0x01, 0xaa}; !bytes.Equal(body, want) {
		t.Errorf("JoinPacketBody() = %x, want %x", body, want)
	}

	if _, err := JoinPacketBody(protocol.Packet{}, protocol.Limits{}); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("JoinPacketBody(invalid limits) error = %v, want ErrInvalidLimits", err)
	}

	oversized := protocol.Packet{ID: 0x01, Payload: bytes.Repeat([]byte{0x00}, 8)}
	if _, err := JoinPacketBody(oversized, limits); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("JoinPacketBody(oversized) error = %v, want ErrFrameTooLarge", err)
	}
}

func TestJoinPacketBodyOwnsItsResult(t *testing.T) {
	t.Parallel()

	payload := []byte{0xaa, 0xbb}
	body, err := JoinPacketBody(protocol.Packet{ID: 0x01, Payload: payload}, frameLimits(t, 8))
	if err != nil {
		t.Fatalf("JoinPacketBody() error = %v", err)
	}

	payload[0] = 0xff
	if body[1] != 0xaa {
		t.Fatal("JoinPacketBody() aliased the caller's payload, want an owned copy")
	}
}

// partialWriter accepts limit bytes and then fails, modelling a transport that
// dies mid-frame.
type partialWriter struct {
	limit   int
	written int
	err     error
}

func (w *partialWriter) Write(data []byte) (int, error) {
	remaining := w.limit - w.written
	if remaining <= 0 {
		return 0, w.err
	}
	if len(data) > remaining {
		data = data[:remaining]
	}
	w.written += len(data)
	return len(data), nil
}
