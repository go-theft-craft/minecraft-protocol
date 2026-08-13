package java

import (
	"bytes"
	"fmt"
	"io"

	"github.com/go-theft-craft/minecraft-protocol"
)

// ReadRawPacket reads an uncompressed Java Edition packet frame.
func ReadRawPacket(r io.Reader, limits protocol.Limits) (protocol.Packet, error) {
	if err := validateLimits(limits); err != nil {
		return protocol.Packet{}, err
	}

	length, _, err := ReadVarInt(r)
	if err != nil {
		return protocol.Packet{}, fmt.Errorf("read packet frame length: %w", err)
	}
	if length < 0 {
		return protocol.Packet{}, fmt.Errorf("read packet frame length %d: %w", length, ErrNegativeLength)
	}
	if length == 0 {
		return protocol.Packet{}, fmt.Errorf("read packet frame length 0: missing packet ID")
	}
	if int(length) > limits.FrameBytes() {
		return protocol.Packet{}, fmt.Errorf(
			"read packet frame length %d exceeds limit %d: %w",
			length,
			limits.FrameBytes(),
			ErrFrameTooLarge,
		)
	}

	frame := make([]byte, int(length))
	if _, err := io.ReadFull(r, frame); err != nil {
		return protocol.Packet{}, fmt.Errorf("read packet frame payload: %w", err)
	}

	id, idBytes, err := ReadVarInt(bytes.NewReader(frame))
	if err != nil {
		return protocol.Packet{}, fmt.Errorf("read packet ID: %w", err)
	}

	return protocol.Packet{ID: id, Payload: frame[idBytes:]}, nil
}

// WriteRawPacket writes an uncompressed Java Edition packet frame.
func WriteRawPacket(w io.Writer, limits protocol.Limits, packet protocol.Packet) error {
	if err := validateLimits(limits); err != nil {
		return err
	}

	idBytes := VarIntSize(packet.ID)
	if idBytes > limits.FrameBytes() || len(packet.Payload) > limits.FrameBytes()-idBytes {
		return fmt.Errorf(
			"write packet frame size exceeds limit %d: %w",
			limits.FrameBytes(),
			ErrFrameTooLarge,
		)
	}

	frameLength := idBytes + len(packet.Payload)
	lengthBytes := VarIntSize(int32(frameLength))
	frame := make([]byte, lengthBytes+frameLength)
	PutVarInt(frame, int32(frameLength))
	PutVarInt(frame[lengthBytes:], packet.ID)
	copy(frame[lengthBytes+idBytes:], packet.Payload)

	if _, err := writeFull(w, frame); err != nil {
		return fmt.Errorf("write packet frame: %w", err)
	}
	return nil
}
