package java

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"github.com/go-theft-craft/minecraft-protocol"
)

// framer implements protocol.Framer for Java Edition length-prefixed frames.
// It knows nothing about packet IDs or compression: a frame payload is opaque
// bytes that later pipeline stages interpret.
type framer struct {
	limits protocol.Limits
}

var _ protocol.Framer = framer{}

// NewFramer returns a Java Edition VarInt-length framer bound to limits.
func NewFramer(limits protocol.Limits) (protocol.Framer, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}

	return framer{limits: limits}, nil
}

// ReadFrame reads one complete length-prefixed frame into an owned buffer. The
// returned wire bytes retain the length prefix so observers see exactly what
// crossed the transport, while the payload view excludes it.
func (f framer) ReadFrame(reader io.Reader) (protocol.Frame, error) {
	length, prefix, err := readFrameLength(reader)
	if err != nil {
		return protocol.Frame{}, err
	}
	if length < 0 {
		return protocol.Frame{}, fmt.Errorf("read packet frame length %d: %w", length, ErrNegativeLength)
	}
	if length == 0 {
		return protocol.Frame{}, fmt.Errorf("read packet frame length 0: empty frame")
	}
	if int(length) > f.limits.FrameBytes() {
		return protocol.Frame{}, fmt.Errorf(
			"read packet frame length %d exceeds limit %d: %w",
			length,
			f.limits.FrameBytes(),
			ErrFrameTooLarge,
		)
	}

	wire := make([]byte, len(prefix)+int(length))
	copy(wire, prefix)
	if _, err := io.ReadFull(reader, wire[len(prefix):]); err != nil {
		return protocol.Frame{}, fmt.Errorf("read packet frame payload: %w", err)
	}

	return protocol.NewFrame(wire, len(prefix))
}

// BuildFrame prefixes one frame payload with its VarInt length.
func (f framer) BuildFrame(payload []byte) (protocol.Frame, error) {
	if len(payload) == 0 {
		return protocol.Frame{}, fmt.Errorf("build packet frame: empty frame payload")
	}
	if len(payload) > f.limits.FrameBytes() {
		return protocol.Frame{}, fmt.Errorf(
			"build packet frame size %d exceeds limit %d: %w",
			len(payload),
			f.limits.FrameBytes(),
			ErrFrameTooLarge,
		)
	}

	lengthBytes := VarIntSize(int32(len(payload)))
	wire := make([]byte, lengthBytes+len(payload))
	PutVarInt(wire, int32(len(payload)))
	copy(wire[lengthBytes:], payload)

	return protocol.NewFrame(wire, lengthBytes)
}

// WriteFrame writes every byte of one frame or returns an error.
func (f framer) WriteFrame(writer io.Writer, frame protocol.Frame) error {
	payload := frame.Payload()
	if len(payload) == 0 {
		return fmt.Errorf("write packet frame: empty frame payload")
	}
	if len(payload) > f.limits.FrameBytes() {
		return fmt.Errorf(
			"write packet frame size %d exceeds limit %d: %w",
			len(payload),
			f.limits.FrameBytes(),
			ErrFrameTooLarge,
		)
	}

	if _, err := writeFull(writer, frame.WireBytes()); err != nil {
		return fmt.Errorf("write packet frame: %w", err)
	}

	return nil
}

// readFrameLength reads the VarInt frame length and returns the exact bytes it
// consumed. Retaining them keeps observations lossless. A length that is
// encoded in more bytes than it needs is rejected, because accepting it would
// let one frame have several valid encodings.
func readFrameLength(reader io.Reader) (int32, []byte, error) {
	var scratch [5]byte
	var result uint32

	for read := range scratch {
		if _, err := io.ReadFull(reader, scratch[read:read+1]); err != nil {
			// A clean EOF is only clean before the first byte. Losing that
			// distinction would let a truncated frame look like a peer that
			// closed the connection normally.
			if read > 0 && errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}

			return 0, nil, fmt.Errorf("read packet frame length byte %d: %w", read+1, err)
		}

		result |= uint32(scratch[read]&0x7f) << (7 * read)
		if scratch[read]&0x80 != 0 {
			continue
		}

		length := int32(result)
		if VarIntSize(length) != read+1 {
			return 0, nil, fmt.Errorf("read packet frame length: %w", ErrVarIntTooLong)
		}

		return length, scratch[:read+1], nil
	}

	return 0, nil, fmt.Errorf("read packet frame length: %w", ErrVarIntTooLong)
}

// SplitPacketBody separates the leading packet ID from one uncompressed packet
// body. The returned payload borrows body.
func SplitPacketBody(body []byte) (protocol.Packet, error) {
	id, idBytes, err := ReadVarInt(bytes.NewReader(body))
	if err != nil {
		return protocol.Packet{}, fmt.Errorf("read packet ID: %w", err)
	}

	return protocol.Packet{ID: id, Payload: body[idBytes:]}, nil
}

// JoinPacketBody prefixes a packet payload with its VarInt packet ID and
// returns an owned buffer bounded by the frame limit.
func JoinPacketBody(packet protocol.Packet, limits protocol.Limits) ([]byte, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}

	idBytes := VarIntSize(packet.ID)
	if idBytes > limits.FrameBytes() || len(packet.Payload) > limits.FrameBytes()-idBytes {
		return nil, fmt.Errorf(
			"packet body size exceeds frame limit %d: %w",
			limits.FrameBytes(),
			ErrFrameTooLarge,
		)
	}

	body := make([]byte, idBytes+len(packet.Payload))
	PutVarInt(body, packet.ID)
	copy(body[idBytes:], packet.Payload)

	return body, nil
}
