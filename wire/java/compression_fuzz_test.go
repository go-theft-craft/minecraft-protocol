package java

import (
	"bytes"
	"compress/zlib"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
)

// FuzzDecodeCompression proves that no envelope, however malformed, panics or
// produces more bytes than the configured decompressed limit allows.
func FuzzDecodeCompression(f *testing.F) {
	const (
		frameBytes        = 4096
		decompressedBytes = 64
	)

	limits, err := protocol.NewLimits(
		protocol.MaxFrameBytes(frameBytes),
		protocol.MaxDecompressedBytes(decompressedBytes),
		protocol.MaxBufferedBytes(frameBytes+decompressedBytes),
	)
	if err != nil {
		f.Fatal(err)
	}

	body := bytes.Repeat([]byte{0xab}, 16)
	var stream bytes.Buffer
	writer := zlib.NewWriter(&stream)
	if _, err := writer.Write(body); err != nil {
		f.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		f.Fatal(err)
	}

	prefixed := func(declared int32, rest []byte) []byte {
		var header [5]byte
		headerBytes := PutVarInt(header[:], declared)
		return append(append([]byte{}, header[:headerBytes]...), rest...)
	}

	seeds := [][]byte{
		nil,
		{0x00},
		{0x01},
		{0xff, 0xff, 0xff, 0xff, 0x0f},
		bytes.Repeat([]byte{0x80}, 5),
		prefixed(0, body),
		prefixed(int32(len(body)), stream.Bytes()),
		prefixed(int32(len(body))-1, stream.Bytes()),
		prefixed(int32(len(body))+1, stream.Bytes()),
		prefixed(int32(len(body)), append(append([]byte{}, stream.Bytes()...), stream.Bytes()...)),
		prefixed(int32(len(body)), stream.Bytes()[:stream.Len()-2]),
		prefixed(decompressedBytes+1, stream.Bytes()),
		prefixed(1<<30, stream.Bytes()),
		// A decompression bomb: many zero bytes shrink to almost nothing.
		prefixed(decompressedBytes, compressZeros(f, 1<<20)),
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	policies := []CompressionPolicy{StrictCompression, CompatibleCompression}

	f.Fuzz(func(t *testing.T, payload []byte) {
		for _, policy := range policies {
			control := CompressionControl{Enabled: true, Threshold: 8, Policy: policy}

			decoded, err := DecodeCompression(payload, control, limits)
			if err != nil {
				continue
			}
			if len(decoded) > limits.DecompressedBytes() {
				t.Fatalf(
					"DecodeCompression() returned %d bytes, above the limit of %d",
					len(decoded),
					limits.DecompressedBytes(),
				)
			}
		}
	})
}

func compressZeros(f *testing.F, size int) []byte {
	f.Helper()

	var out bytes.Buffer
	writer := zlib.NewWriter(&out)
	if _, err := writer.Write(make([]byte, size)); err != nil {
		f.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		f.Fatal(err)
	}
	return out.Bytes()
}
