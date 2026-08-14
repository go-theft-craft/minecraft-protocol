package java

import (
	"io"

	"github.com/go-theft-craft/minecraft-protocol"
)

// ReadRawPacket reads an uncompressed Java Edition packet frame. It is a
// low-level helper for callers that own their own connection loop. A managed
// stream uses NewFramer and SplitPacketBody instead.
func ReadRawPacket(r io.Reader, limits protocol.Limits) (protocol.Packet, error) {
	frameReader, err := NewFramer(limits)
	if err != nil {
		return protocol.Packet{}, err
	}

	frame, err := frameReader.ReadFrame(r)
	if err != nil {
		return protocol.Packet{}, err
	}

	return SplitPacketBody(frame.Payload())
}

// WriteRawPacket writes an uncompressed Java Edition packet frame. It is a
// low-level helper for callers that own their own connection loop. A managed
// stream uses JoinPacketBody and NewFramer instead.
func WriteRawPacket(w io.Writer, limits protocol.Limits, packet protocol.Packet) error {
	frameWriter, err := NewFramer(limits)
	if err != nil {
		return err
	}

	body, err := JoinPacketBody(packet, limits)
	if err != nil {
		return err
	}

	frame, err := frameWriter.BuildFrame(body)
	if err != nil {
		return err
	}

	return frameWriter.WriteFrame(w, frame)
}
