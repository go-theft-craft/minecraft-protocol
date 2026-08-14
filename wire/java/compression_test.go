package java

import (
	"bytes"
	"compress/zlib"
	"errors"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
)

func compressionLimits(t *testing.T, frameBytes, decompressedBytes int) protocol.Limits {
	t.Helper()

	limits, err := protocol.NewLimits(
		protocol.MaxFrameBytes(frameBytes),
		protocol.MaxDecompressedBytes(decompressedBytes),
		protocol.MaxBufferedBytes(frameBytes+decompressedBytes),
	)
	if err != nil {
		t.Fatal(err)
	}
	return limits
}

func enabledCompression(threshold int32, policy CompressionPolicy) CompressionControl {
	return CompressionControl{Enabled: true, Threshold: threshold, Policy: policy}
}

// zlibStream returns a valid zlib stream for body.
func zlibStream(t *testing.T, body []byte) []byte {
	t.Helper()

	var out bytes.Buffer
	writer := zlib.NewWriter(&out)
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

// envelope builds a frame payload with a declared data length.
func envelope(declared int32, rest []byte) []byte {
	var header [5]byte
	headerBytes := PutVarInt(header[:], declared)
	return append(append([]byte{}, header[:headerBytes]...), rest...)
}

func TestCompressionControlName(t *testing.T) {
	t.Parallel()

	var control protocol.Control = CompressionControl{}
	if control.ControlName() != "java.compression" {
		t.Fatalf("ControlName() = %q, want %q", control.ControlName(), "java.compression")
	}
}

func TestCompressionPolicyNames(t *testing.T) {
	t.Parallel()

	if StrictCompression.Name() != "strict" {
		t.Errorf("StrictCompression.Name() = %q, want %q", StrictCompression.Name(), "strict")
	}
	if CompatibleCompression.Name() != "compatible" {
		t.Errorf("CompatibleCompression.Name() = %q, want %q", CompatibleCompression.Name(), "compatible")
	}
}

func TestCompressionRejectsUnusableControls(t *testing.T) {
	t.Parallel()

	limits := compressionLimits(t, 1024, 4096)
	cases := map[string]CompressionControl{
		"negative threshold": {Enabled: true, Threshold: -1, Policy: StrictCompression},
		"nil policy":         {Enabled: true, Threshold: 8},
	}

	for name, control := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := DecodeCompression([]byte{0x00, 0x01}, control, limits); !errors.Is(err, ErrInvalidCompression) {
				t.Errorf("DecodeCompression() error = %v, want ErrInvalidCompression", err)
			}
			if _, err := EncodeCompression([]byte{0x01}, control, limits); !errors.Is(err, ErrInvalidCompression) {
				t.Errorf("EncodeCompression() error = %v, want ErrInvalidCompression", err)
			}
		})
	}
}

func TestCompressionInvalidLimits(t *testing.T) {
	t.Parallel()

	var invalid protocol.Limits
	if _, err := DecodeCompression(nil, CompressionControl{}, invalid); !errors.Is(err, ErrInvalidLimits) {
		t.Errorf("DecodeCompression() error = %v, want ErrInvalidLimits", err)
	}
	if _, err := EncodeCompression(nil, CompressionControl{}, invalid); !errors.Is(err, ErrInvalidLimits) {
		t.Errorf("EncodeCompression() error = %v, want ErrInvalidLimits", err)
	}
}

func TestCompressionDisabledPassesBodyThrough(t *testing.T) {
	t.Parallel()

	limits := compressionLimits(t, 1024, 4096)
	body := []byte{0x01, 0xaa, 0xbb}

	encoded, err := EncodeCompression(body, CompressionControl{}, limits)
	if err != nil {
		t.Fatalf("EncodeCompression() error = %v", err)
	}
	if !bytes.Equal(encoded, body) {
		t.Fatalf("EncodeCompression() = %x, want %x", encoded, body)
	}

	decoded, err := DecodeCompression(encoded, CompressionControl{}, limits)
	if err != nil {
		t.Fatalf("DecodeCompression() error = %v", err)
	}
	if !bytes.Equal(decoded, body) {
		t.Fatalf("DecodeCompression() = %x, want %x", decoded, body)
	}
}

func TestCompressionDisabledEnforcesFrameLimit(t *testing.T) {
	t.Parallel()

	limits := compressionLimits(t, 4, 4096)
	if _, err := EncodeCompression(bytes.Repeat([]byte{0x00}, 5), CompressionControl{}, limits); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("EncodeCompression() error = %v, want ErrFrameTooLarge", err)
	}
}

func TestCompressionThresholdBoundaries(t *testing.T) {
	t.Parallel()

	limits := compressionLimits(t, 4096, 8192)

	cases := []struct {
		name           string
		threshold      int32
		body           []byte
		wantCompressed bool
	}{
		{name: "below threshold", threshold: 8, body: bytes.Repeat([]byte{0xab}, 7)},
		{name: "at threshold", threshold: 8, body: bytes.Repeat([]byte{0xab}, 8), wantCompressed: true},
		{name: "above threshold", threshold: 8, body: bytes.Repeat([]byte{0xab}, 9), wantCompressed: true},
		{name: "threshold zero compresses everything", threshold: 0, body: []byte{0x01}, wantCompressed: true},
		{name: "threshold one leaves empty bodies alone", threshold: 1, body: nil},
		{name: "threshold one compresses one byte", threshold: 1, body: []byte{0x01}, wantCompressed: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			control := enabledCompression(testCase.threshold, StrictCompression)
			encoded, err := EncodeCompression(testCase.body, control, limits)
			if err != nil {
				t.Fatalf("EncodeCompression() error = %v", err)
			}

			declared, _, err := ReadVarInt(bytes.NewReader(encoded))
			if err != nil {
				t.Fatalf("ReadVarInt() error = %v", err)
			}
			if compressed := declared != 0; compressed != testCase.wantCompressed {
				t.Fatalf("declared length = %d, compressed = %t, want compressed = %t", declared, compressed, testCase.wantCompressed)
			}

			decoded, err := DecodeCompression(encoded, control, limits)
			if err != nil {
				t.Fatalf("DecodeCompression() error = %v", err)
			}
			if !bytes.Equal(decoded, testCase.body) {
				t.Fatalf("DecodeCompression() = %x, want %x", decoded, testCase.body)
			}
		})
	}
}

func TestStrictCompressionRejectsWrongEnvelopeForm(t *testing.T) {
	t.Parallel()

	limits := compressionLimits(t, 4096, 8192)
	strict := enabledCompression(8, StrictCompression)
	compatible := enabledCompression(8, CompatibleCompression)

	t.Run("compressed below threshold", func(t *testing.T) {
		t.Parallel()

		body := []byte{0x01, 0x02}
		payload := envelope(int32(len(body)), zlibStream(t, body))

		if _, err := DecodeCompression(payload, strict, limits); !errors.Is(err, ErrCompressionPolicy) {
			t.Fatalf("strict DecodeCompression() error = %v, want ErrCompressionPolicy", err)
		}
		decoded, err := DecodeCompression(payload, compatible, limits)
		if err != nil {
			t.Fatalf("compatible DecodeCompression() error = %v", err)
		}
		if !bytes.Equal(decoded, body) {
			t.Fatalf("compatible DecodeCompression() = %x, want %x", decoded, body)
		}
	})

	t.Run("uncompressed at threshold", func(t *testing.T) {
		t.Parallel()

		body := bytes.Repeat([]byte{0xab}, 8)
		payload := envelope(0, body)

		if _, err := DecodeCompression(payload, strict, limits); !errors.Is(err, ErrCompressionPolicy) {
			t.Fatalf("strict DecodeCompression() error = %v, want ErrCompressionPolicy", err)
		}
		if _, err := DecodeCompression(payload, compatible, limits); err != nil {
			t.Fatalf("compatible DecodeCompression() error = %v", err)
		}
	})
}

func TestDecodeCompressionRejectsMalformedEnvelopes(t *testing.T) {
	t.Parallel()

	limits := compressionLimits(t, 4096, 64)
	control := enabledCompression(0, CompatibleCompression)
	body := bytes.Repeat([]byte{0xab}, 16)
	stream := zlibStream(t, body)

	cases := []struct {
		name    string
		payload []byte
		want    error
	}{
		{name: "empty payload", payload: nil, want: nil},
		{
			name:    "negative declared length",
			payload: envelope(-1, stream),
			want:    ErrNegativeLength,
		},
		{
			name:    "declared length above limit",
			payload: envelope(65, stream),
			want:    ErrDecompressedTooLarge,
		},
		{
			// The decompressed limit bounds the body on the uncompressed path
			// too, even though the frame limit alone would allow it.
			name:    "uncompressed body above decompressed limit",
			payload: envelope(0, bytes.Repeat([]byte{0xab}, 65)),
			want:    ErrDecompressedTooLarge,
		},
		{
			name:    "corrupt zlib data",
			payload: envelope(int32(len(body)), bytes.Repeat([]byte{0xff}, 8)),
			want:    ErrInvalidCompression,
		},
		{
			name:    "declared length below actual output",
			payload: envelope(int32(len(body))-1, stream),
			want:    ErrInvalidCompression,
		},
		{
			name:    "declared length above actual output",
			payload: envelope(int32(len(body))+1, stream),
			want:    ErrInvalidCompression,
		},
		{
			name:    "trailing bytes after the stream",
			payload: envelope(int32(len(body)), append(append([]byte{}, stream...), 0x00)),
			want:    ErrTrailingBytes,
		},
		{
			name:    "concatenated zlib streams",
			payload: envelope(int32(len(body)), append(append([]byte{}, stream...), stream...)),
			want:    ErrTrailingBytes,
		},
		{
			name:    "truncated zlib stream",
			payload: envelope(int32(len(body)), stream[:len(stream)-2]),
			want:    ErrInvalidCompression,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			_, err := DecodeCompression(testCase.payload, control, limits)
			if err == nil {
				t.Fatal("DecodeCompression() error = nil, want an error")
			}
			if testCase.want != nil && !errors.Is(err, testCase.want) {
				t.Fatalf("DecodeCompression() error = %v, want %v", err, testCase.want)
			}
		})
	}
}

func TestDecodeCompressionBoundsAllocationByLimit(t *testing.T) {
	t.Parallel()

	limits := compressionLimits(t, 4096, 64)
	control := enabledCompression(0, CompatibleCompression)

	// A declared length of 1 GiB must be refused before a single compressed
	// byte is read, so the envelope body here is deliberately nonsense.
	payload := envelope(1<<30, []byte{0xff, 0xff})
	if _, err := DecodeCompression(payload, control, limits); !errors.Is(err, ErrDecompressedTooLarge) {
		t.Fatalf("DecodeCompression() error = %v, want ErrDecompressedTooLarge", err)
	}
}

func TestEncodeCompressionEnforcesLimits(t *testing.T) {
	t.Parallel()

	t.Run("body above decompressed limit", func(t *testing.T) {
		t.Parallel()

		limits := compressionLimits(t, 4096, 16)
		control := enabledCompression(0, StrictCompression)
		if _, err := EncodeCompression(bytes.Repeat([]byte{0xab}, 17), control, limits); !errors.Is(err, ErrDecompressedTooLarge) {
			t.Fatalf("EncodeCompression() error = %v, want ErrDecompressedTooLarge", err)
		}
	})

	t.Run("uncompressed envelope above frame limit", func(t *testing.T) {
		t.Parallel()

		limits := compressionLimits(t, 4, 4096)
		control := enabledCompression(100, StrictCompression)
		if _, err := EncodeCompression(bytes.Repeat([]byte{0xab}, 4), control, limits); !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("EncodeCompression() error = %v, want ErrFrameTooLarge", err)
		}
	})

	t.Run("compressed envelope above frame limit", func(t *testing.T) {
		t.Parallel()

		limits := compressionLimits(t, 8, 4096)
		control := enabledCompression(0, StrictCompression)
		// Random bytes do not compress, so the envelope grows past the frame.
		body := make([]byte, 256)
		for index := range body {
			body[index] = byte(index * 7)
		}
		if _, err := EncodeCompression(body, control, limits); !errors.Is(err, ErrFrameTooLarge) {
			t.Fatalf("EncodeCompression() error = %v, want ErrFrameTooLarge", err)
		}
	})
}

func TestCompressionRoundTripThroughFramer(t *testing.T) {
	t.Parallel()

	limits := compressionLimits(t, 4096, 8192)
	control := enabledCompression(8, StrictCompression)
	frameFramer, err := NewFramer(limits)
	if err != nil {
		t.Fatalf("NewFramer() error = %v", err)
	}

	want := protocol.Packet{ID: 0x02, Payload: bytes.Repeat([]byte{0xab}, 64)}
	body, err := JoinPacketBody(want, limits)
	if err != nil {
		t.Fatalf("JoinPacketBody() error = %v", err)
	}
	payload, err := EncodeCompression(body, control, limits)
	if err != nil {
		t.Fatalf("EncodeCompression() error = %v", err)
	}
	frame, err := frameFramer.BuildFrame(payload)
	if err != nil {
		t.Fatalf("BuildFrame() error = %v", err)
	}

	var wire bytes.Buffer
	if err := frameFramer.WriteFrame(&wire, frame); err != nil {
		t.Fatalf("WriteFrame() error = %v", err)
	}

	read, err := frameFramer.ReadFrame(&wire)
	if err != nil {
		t.Fatalf("ReadFrame() error = %v", err)
	}
	decodedBody, err := DecodeCompression(read.Payload(), control, limits)
	if err != nil {
		t.Fatalf("DecodeCompression() error = %v", err)
	}
	got, err := SplitPacketBody(decodedBody)
	if err != nil {
		t.Fatalf("SplitPacketBody() error = %v", err)
	}

	if got.ID != want.ID || !bytes.Equal(got.Payload, want.Payload) {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}
