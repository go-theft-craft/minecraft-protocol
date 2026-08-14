package protocol

import (
	"errors"
	"strings"
	"testing"
)

func TestNewLimitsUsesFiniteDefaults(t *testing.T) {
	t.Parallel()

	limits, err := NewLimits()
	if err != nil {
		t.Fatalf("NewLimits() error = %v", err)
	}
	if limits.FrameBytes() == 0 || limits.QueueItems() == 0 {
		t.Fatalf("NewLimits() returned an unlimited value: %+v", limits)
	}
}

func TestNewLimitsRejectsHardCeilingViolation(t *testing.T) {
	t.Parallel()

	_, err := NewLimits(MaxFrameBytes(hardFrameBytes + 1))
	if !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("NewLimits() error = %v, want ErrLimitExceeded", err)
	}
}

func TestNewLimitsAcceptsCustomBound(t *testing.T) {
	t.Parallel()

	const frameBytes = 4 << 20
	limits, err := NewLimits(MaxFrameBytes(frameBytes))
	if err != nil {
		t.Fatalf("NewLimits() error = %v", err)
	}
	if limits.FrameBytes() != frameBytes {
		t.Fatalf("FrameBytes() = %d, want %d", limits.FrameBytes(), frameBytes)
	}
}

func TestNewLimitsUsesDefaultBufferedBytes(t *testing.T) {
	t.Parallel()

	limits, err := NewLimits()
	if err != nil {
		t.Fatalf("NewLimits() error = %v", err)
	}
	if limits.BufferedBytes() != defaultBufferedBytes {
		t.Fatalf("BufferedBytes() = %d, want %d", limits.BufferedBytes(), defaultBufferedBytes)
	}
	if defaultBufferedBytes != 32<<20 {
		t.Fatalf("defaultBufferedBytes = %d, want %d", defaultBufferedBytes, 32<<20)
	}
}

func TestMaxBufferedBytesBounds(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		value   int
		wantErr bool
	}{
		{name: "accepted", value: 64 << 20},
		{name: "zero", value: 0, wantErr: true},
		{name: "negative", value: -1, wantErr: true},
		{name: "hard ceiling", value: hardBufferedBytes},
		{name: "above hard ceiling", value: hardBufferedBytes + 1, wantErr: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			limits, err := NewLimits(MaxBufferedBytes(testCase.value))
			if testCase.wantErr {
				if !errors.Is(err, ErrLimitExceeded) {
					t.Fatalf("NewLimits() error = %v, want ErrLimitExceeded", err)
				}

				return
			}
			if err != nil {
				t.Fatalf("NewLimits() error = %v", err)
			}
			if limits.BufferedBytes() != testCase.value {
				t.Fatalf("BufferedBytes() = %d, want %d", limits.BufferedBytes(), testCase.value)
			}
		})
	}
}

func TestZeroLimitsStayInvalid(t *testing.T) {
	t.Parallel()

	var limits Limits
	if limits.Valid() {
		t.Fatal("Limits{}.Valid() = true, want false")
	}
	if limits.BufferedBytes() != 0 {
		t.Fatalf("Limits{}.BufferedBytes() = %d, want 0", limits.BufferedBytes())
	}
}

func TestNewLimitsRejectsBufferedBytesBelowWorkingSet(t *testing.T) {
	t.Parallel()

	const (
		frameBytes        = 4 << 20
		decompressedBytes = 8 << 20
		bufferedBytes     = frameBytes + decompressedBytes - 1
	)

	orders := map[string][]LimitOption{
		"buffered last": {
			MaxFrameBytes(frameBytes),
			MaxDecompressedBytes(decompressedBytes),
			MaxBufferedBytes(bufferedBytes),
		},
		"buffered first": {
			MaxBufferedBytes(bufferedBytes),
			MaxFrameBytes(frameBytes),
			MaxDecompressedBytes(decompressedBytes),
		},
		"buffered middle": {
			MaxFrameBytes(frameBytes),
			MaxBufferedBytes(bufferedBytes),
			MaxDecompressedBytes(decompressedBytes),
		},
	}

	for name, options := range orders {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := NewLimits(options...)
			if !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("NewLimits() error = %v, want ErrLimitExceeded", err)
			}
			for _, want := range []string{"4194304", "8388608", "12582911"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("NewLimits() error = %q, want it to name %s", err.Error(), want)
				}
			}
		})
	}
}

func TestNewLimitsAcceptsBufferedBytesAtWorkingSet(t *testing.T) {
	t.Parallel()

	const (
		frameBytes        = 4 << 20
		decompressedBytes = 8 << 20
	)

	limits, err := NewLimits(
		MaxFrameBytes(frameBytes),
		MaxDecompressedBytes(decompressedBytes),
		MaxBufferedBytes(frameBytes+decompressedBytes),
	)
	if err != nil {
		t.Fatalf("NewLimits() error = %v", err)
	}
	if limits.BufferedBytes() != frameBytes+decompressedBytes {
		t.Fatalf("BufferedBytes() = %d, want %d", limits.BufferedBytes(), frameBytes+decompressedBytes)
	}
}
