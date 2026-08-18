package protocol

import (
	"crypto/aes"
	"crypto/cipher"
	"fmt"
	"io"
	"math/rand"
	"testing"
)

// BenchmarkConduitWrite measures the transport write path with and without a
// stream cipher in front of it. The gap between the two is the price of
// enabling encryption at the conduit, independent of which cipher the wire
// package supplies.
func BenchmarkConduitWrite(b *testing.B) {
	sizes := []int{256, 4 << 10, 64 << 10, 1 << 20}

	for _, encrypted := range []bool{false, true} {
		mode := "plain"
		if encrypted {
			mode = "encrypted"
		}
		for _, size := range sizes {
			payload := make([]byte, size)
			rand.New(rand.NewSource(1)).Read(payload)

			conduit := newConduit(Transport{
				Reader:    io.MultiReader(),
				Writer:    io.Discard,
				Interrupt: func() error { return nil },
			})
			if encrypted {
				key := []byte("0123456789abcdef")
				block, err := aes.NewCipher(key)
				if err != nil {
					b.Fatalf("aes: %v", err)
				}
				//nolint:staticcheck // SA1019: any stream cipher exercises the switch.
				decrypt := cipher.NewCFBDecrypter(block, key)
				//nolint:staticcheck // SA1019: any stream cipher exercises the switch.
				encrypt := cipher.NewCFBEncrypter(block, key)
				if err := conduit.EnableEncryption(decrypt, encrypt); err != nil {
					b.Fatalf("EnableEncryption: %v", err)
				}
			}

			label := size >> 10
			name := fmt.Sprintf("%s/%dKiB", mode, label)
			if size < 1<<10 {
				name = fmt.Sprintf("%s/%dB", mode, size)
			}
			b.Run(name, func(b *testing.B) {
				b.SetBytes(int64(size))
				b.ReportAllocs()
				for b.Loop() {
					if _, err := conduit.Write(payload); err != nil {
						b.Fatalf("write: %v", err)
					}
				}
			})
		}
	}
}
