package protocol

import (
	"errors"
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
