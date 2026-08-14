package protocol_test

import (
	"bytes"
	"compress/zlib"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

// FuzzInboundPipeline drives arbitrary bytes through the same framing,
// compression, and decode steps the read pump and coordinator use, without the
// goroutines. Nothing it produces may panic or exceed the configured limits.
func FuzzInboundPipeline(f *testing.F) {
	const (
		frameBytes        = 512
		decompressedBytes = 1024
	)

	limits, err := protocol.NewLimits(
		protocol.MaxFrameBytes(frameBytes),
		protocol.MaxDecompressedBytes(decompressedBytes),
		protocol.MaxBufferedBytes(frameBytes+decompressedBytes),
	)
	if err != nil {
		f.Fatal(err)
	}

	for _, seed := range inboundFuzzSeeds(f, limits) {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, wire []byte) {
		for _, compressed := range []bool{false, true} {
			session, err := v1_8.Protocol().NewSession(protocol.RoleServer, limits)
			if err != nil {
				t.Fatal(err)
			}
			if compressed {
				control := java.CompressionControl{
					Enabled:   true,
					Threshold: 16,
					Policy:    java.CompatibleCompression,
				}
				if err := session.ValidateControl(control); err != nil {
					t.Fatal(err)
				}
				session.ApplyControl(control)
			}

			reader := bytes.NewReader(wire)
			for {
				frame, err := session.Framer().ReadFrame(reader)
				if err != nil {
					break
				}
				if len(frame.Payload()) > limits.FrameBytes() {
					t.Fatalf("framer returned %d payload bytes, above the limit of %d",
						len(frame.Payload()), limits.FrameBytes())
				}

				packet, err := session.DecodeFrame(frame.Payload())
				if err != nil {
					break
				}
				if len(packet.Payload) > limits.DecompressedBytes() {
					t.Fatalf("decode returned %d payload bytes, above the limit of %d",
						len(packet.Payload), limits.DecompressedBytes())
				}

				// A packet may propose a transition; it must never panic and
				// must never apply without validating.
				transition, proposed, err := session.ProposeTransition(packet)
				if err != nil || !proposed {
					continue
				}
				if err := session.ValidateTransition(transition); err == nil {
					session.ApplyTransition(transition)
				}
			}
		}
	})
}

func inboundFuzzSeeds(f *testing.F, limits protocol.Limits) [][]byte {
	f.Helper()

	framed := func(body []byte) []byte {
		var header [5]byte
		size := java.PutVarInt(header[:], int32(len(body)))
		return append(append([]byte{}, header[:size]...), body...)
	}

	compress := func(body []byte) []byte {
		var out bytes.Buffer
		writer := zlib.NewWriter(&out)
		if _, err := writer.Write(body); err != nil {
			f.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			f.Fatal(err)
		}
		return out.Bytes()
	}

	envelope := func(declared int32, rest []byte) []byte {
		var header [5]byte
		size := java.PutVarInt(header[:], declared)
		return append(append([]byte{}, header[:size]...), rest...)
	}

	// A real handshake, so the fuzzer starts from something that parses.
	handshake, err := java.JoinPacketBody(protocol.Packet{
		ID:      0x00,
		Payload: append([]byte{47, 9}, []byte("localhost\x63\xdd\x02")...),
	}, limits)
	if err != nil {
		f.Fatal(err)
	}

	body := bytes.Repeat([]byte{0x41}, 64)

	return [][]byte{
		nil,
		{0x00},
		{0x01},
		{0x01, 0x00},
		// Negative and oversized frame lengths.
		{0xff, 0xff, 0xff, 0xff, 0x0f},
		{0xff, 0xff, 0x7f},
		// Overlong and unterminated length VarInts.
		{0x83, 0x80, 0x80, 0x80, 0x00},
		bytes.Repeat([]byte{0x80}, 5),
		// Truncated frames.
		{0x10, 0x00, 0x01},
		framed(handshake)[:len(framed(handshake))-1],
		framed(handshake),
		// Overlong packet ID.
		framed(bytes.Repeat([]byte{0x80}, 5)),
		// Compression envelopes.
		framed(envelope(0, body)),
		framed(envelope(int32(len(body)), compress(body))),
		framed(envelope(int32(len(body))-1, compress(body))),
		framed(envelope(int32(len(body))+1, compress(body))),
		framed(envelope(int32(len(body)), append(compress(body), 0x00))),
		framed(envelope(1<<20, compress(body))),
		framed(envelope(-1, compress(body))),
		// A decompression bomb inside a bounded frame.
		framed(envelope(1024, compress(make([]byte, 1<<16)))),
		// Two frames back to back.
		append(framed(handshake), framed(handshake)...),
	}
}
