package java

import (
	"bytes"
	"crypto/aes"
	"fmt"
	"io"
	"math/rand"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
)

// The throughput benchmarks measure the levers a caller can pull that change
// how fast bytes move through the wire layer: the compression threshold, the
// cost of zlib itself against payload entropy, CFB8 encryption, and framing.

var benchSizes = []int{256, 4 << 10, 64 << 10, 1 << 20}

// compressibleBody is repeating structured text, the shape of NBT and chat.
func compressibleBody(size int) []byte {
	pattern := []byte(`{"text":"minecraft:stone","count":64,"slot":13}`)
	body := make([]byte, size)
	for offset := 0; offset < size; offset += len(pattern) {
		copy(body[offset:], pattern)
	}

	return body
}

// randomBody is seeded noise, the shape of already-compressed chunk data.
func randomBody(size int) []byte {
	body := make([]byte, size)
	rand.New(rand.NewSource(1)).Read(body)

	return body
}

func benchLimits(b *testing.B) protocol.Limits {
	b.Helper()

	limits, err := protocol.NewLimits(
		protocol.MaxFrameBytes(4<<20),
		protocol.MaxDecompressedBytes(8<<20),
	)
	if err != nil {
		b.Fatalf("NewLimits: %v", err)
	}

	return limits
}

func sizeLabel(size int) string {
	switch {
	case size >= 1<<20:
		return fmt.Sprintf("%dMiB", size>>20)
	case size >= 1<<10:
		return fmt.Sprintf("%dKiB", size>>10)
	default:
		return fmt.Sprintf("%dB", size)
	}
}

func BenchmarkEncodeCompression(b *testing.B) {
	limits := benchLimits(b)

	variants := []struct {
		name    string
		control CompressionControl
		body    func(int) []byte
	}{
		{"disabled", CompressionControl{}, randomBody},
		{
			"belowThreshold",
			CompressionControl{Enabled: true, Threshold: 1 << 30, Policy: CompatibleCompression},
			randomBody,
		},
		{
			"compressible",
			CompressionControl{Enabled: true, Threshold: 256, Policy: CompatibleCompression},
			compressibleBody,
		},
		{
			"incompressible",
			CompressionControl{Enabled: true, Threshold: 256, Policy: CompatibleCompression},
			randomBody,
		},
	}

	for _, variant := range variants {
		for _, size := range benchSizes {
			body := variant.body(size)
			b.Run(fmt.Sprintf("%s/%s", variant.name, sizeLabel(size)), func(b *testing.B) {
				b.SetBytes(int64(size))
				b.ReportAllocs()
				for b.Loop() {
					if _, err := EncodeCompression(body, variant.control, limits); err != nil {
						b.Fatalf("EncodeCompression: %v", err)
					}
				}
			})
		}
	}
}

func BenchmarkDecodeCompression(b *testing.B) {
	limits := benchLimits(b)

	variants := []struct {
		name    string
		control CompressionControl
		body    func(int) []byte
	}{
		{"disabled", CompressionControl{}, randomBody},
		{
			"belowThreshold",
			CompressionControl{Enabled: true, Threshold: 1 << 30, Policy: CompatibleCompression},
			randomBody,
		},
		{
			"compressible",
			CompressionControl{Enabled: true, Threshold: 256, Policy: CompatibleCompression},
			compressibleBody,
		},
		{
			"incompressible",
			CompressionControl{Enabled: true, Threshold: 256, Policy: CompatibleCompression},
			randomBody,
		},
	}

	for _, variant := range variants {
		for _, size := range benchSizes {
			envelope, err := EncodeCompression(variant.body(size), variant.control, limits)
			if err != nil {
				b.Fatalf("EncodeCompression: %v", err)
			}
			b.Run(fmt.Sprintf("%s/%s", variant.name, sizeLabel(size)), func(b *testing.B) {
				b.SetBytes(int64(size))
				b.ReportAllocs()
				for b.Loop() {
					if _, err := DecodeCompression(envelope, variant.control, limits); err != nil {
						b.Fatalf("DecodeCompression: %v", err)
					}
				}
			})
		}
	}
}

func BenchmarkCFB8(b *testing.B) {
	key := []byte("0123456789abcdef")
	block, err := aes.NewCipher(key)
	if err != nil {
		b.Fatalf("aes: %v", err)
	}

	for _, direction := range []string{"encrypt", "decrypt"} {
		for _, size := range benchSizes {
			buffer := randomBody(size)
			stream := newCFB8Encrypter(block, key)
			if direction == "decrypt" {
				stream = newCFB8Decrypter(block, key)
			}
			b.Run(fmt.Sprintf("%s/%s", direction, sizeLabel(size)), func(b *testing.B) {
				b.SetBytes(int64(size))
				b.ReportAllocs()
				for b.Loop() {
					stream.XORKeyStream(buffer, buffer)
				}
			})
		}
	}
}

func BenchmarkFramerBuildFrame(b *testing.B) {
	framer, err := NewFramer(benchLimits(b))
	if err != nil {
		b.Fatalf("NewFramer: %v", err)
	}

	for _, size := range benchSizes {
		payload := randomBody(size)
		b.Run(sizeLabel(size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				if _, err := framer.BuildFrame(payload); err != nil {
					b.Fatalf("BuildFrame: %v", err)
				}
			}
		})
	}
}

func BenchmarkFramerReadFrame(b *testing.B) {
	framer, err := NewFramer(benchLimits(b))
	if err != nil {
		b.Fatalf("NewFramer: %v", err)
	}

	for _, size := range benchSizes {
		frame, err := framer.BuildFrame(randomBody(size))
		if err != nil {
			b.Fatalf("BuildFrame: %v", err)
		}
		wire := frame.WireBytes()
		reader := bytes.NewReader(wire)
		b.Run(sizeLabel(size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				reader.Reset(wire)
				if _, err := framer.ReadFrame(reader); err != nil {
					b.Fatalf("ReadFrame: %v", err)
				}
			}
		})
	}
}

// BenchmarkWriteFrame measures the write side without an owned buffer copy.
func BenchmarkFramerWriteFrame(b *testing.B) {
	framer, err := NewFramer(benchLimits(b))
	if err != nil {
		b.Fatalf("NewFramer: %v", err)
	}

	for _, size := range benchSizes {
		frame, err := framer.BuildFrame(randomBody(size))
		if err != nil {
			b.Fatalf("BuildFrame: %v", err)
		}
		b.Run(sizeLabel(size), func(b *testing.B) {
			b.SetBytes(int64(size))
			b.ReportAllocs()
			for b.Loop() {
				if err := framer.WriteFrame(io.Discard, frame); err != nil {
					b.Fatalf("WriteFrame: %v", err)
				}
			}
		})
	}
}
