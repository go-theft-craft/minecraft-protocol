package capture_test

import (
	"bytes"
	"errors"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol/capture"
)

// FuzzCaptureReader drives the reader with arbitrary bytes.
//
// A capture is read from disk, and a file on disk is whatever is on disk: a
// truncated write, a corrupted sector, or something that is not a capture at
// all. Every one of those must produce an error rather than a panic, an
// unbounded allocation, or a loop that never ends.
func FuzzCaptureReader(f *testing.F) {
	f.Add(writeFuzzSeed(f, 1))
	f.Add(writeFuzzSeed(f, 8))
	f.Add([]byte(capture.Magic))
	f.Add([]byte("not a capture at all"))
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		reader, err := capture.NewReader(bytes.NewReader(data))
		if err != nil {
			return
		}

		// A capture cannot contain more records than it has bytes, so this
		// bound catches a reader that stops making progress without needing
		// to know anything about the input.
		limit := len(data) + 1
		for range limit {
			if _, err := reader.Next(); err != nil {
				if errors.Is(err, capture.ErrEndOfCapture) {
					return
				}

				return
			}
		}

		t.Fatalf("reader produced more than %d records from %d bytes", limit, len(data))
	})
}

func writeFuzzSeed(f *testing.F, count int) []byte {
	f.Helper()

	var buffer bytes.Buffer
	writer, err := capture.NewWriter(&buffer, capture.Header{
		Protocol:          "java/26.1",
		FrameBytes:        1 << 20,
		DecompressedBytes: 1 << 20,
	})
	if err != nil {
		f.Fatalf("NewWriter: %v", err)
	}
	for sequence := 1; sequence <= count; sequence++ {
		if err := writer.Observe(f.Context(), observation(uint64(sequence), []byte{0x01, 0x02})); err != nil {
			f.Fatalf("Observe: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		f.Fatalf("Close: %v", err)
	}

	return buffer.Bytes()
}
