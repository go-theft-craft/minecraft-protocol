package java

import (
	"errors"
	"strings"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol"
)

func depthLimits(t *testing.T, depth int) protocol.Limits {
	t.Helper()

	limits, err := protocol.NewLimits(protocol.MaxRecursionDepth(depth))
	if err != nil {
		t.Fatalf("NewLimits() error = %v", err)
	}

	return limits
}

func TestEnterNestedAcceptsExactlyTheLimit(t *testing.T) {
	t.Parallel()

	buffer, err := NewReadBuffer(nil, depthLimits(t, 4))
	if err != nil {
		t.Fatalf("NewReadBuffer() error = %v", err)
	}

	for level := range 4 {
		if err := buffer.EnterNested("value"); err != nil {
			t.Fatalf("EnterNested at level %d: %v", level, err)
		}
	}
	if got := buffer.NestingDepth(); got != 4 {
		t.Fatalf("depth = %d, want 4", got)
	}
}

func TestEnterNestedRejectsOnePastTheLimit(t *testing.T) {
	t.Parallel()

	buffer, err := NewReadBuffer(nil, depthLimits(t, 2))
	if err != nil {
		t.Fatalf("NewReadBuffer() error = %v", err)
	}

	if err := buffer.EnterNested("slot"); err != nil {
		t.Fatalf("EnterNested: %v", err)
	}
	if err := buffer.EnterNested("slot.component"); err != nil {
		t.Fatalf("EnterNested: %v", err)
	}

	err = buffer.EnterNested("slot.component.predicate")
	if !errors.Is(err, ErrRecursionLimit) {
		t.Fatalf("error = %v, want ErrRecursionLimit", err)
	}
	// The path is what makes a depth failure diagnosable in a packet with
	// several recursive fields.
	if !strings.Contains(err.Error(), "slot.component.predicate") {
		t.Fatalf("error %q does not name the path", err)
	}
	if got := buffer.NestingDepth(); got != 2 {
		t.Fatalf("a rejected entry changed the depth to %d, want 2", got)
	}
}

func TestLeaveNestedReturnsTheCounter(t *testing.T) {
	t.Parallel()

	buffer, err := NewReadBuffer(nil, depthLimits(t, 3))
	if err != nil {
		t.Fatalf("NewReadBuffer() error = %v", err)
	}

	for range 3 {
		if err := buffer.EnterNested("value"); err != nil {
			t.Fatalf("EnterNested: %v", err)
		}
	}
	for range 3 {
		buffer.LeaveNested()
	}
	if got := buffer.NestingDepth(); got != 0 {
		t.Fatalf("depth = %d, want 0", got)
	}
}

// TestLeaveNestedClampsAtZero covers the unwind of a failed decode, where the
// exit paths taken do not all match an entry. A negative counter would raise
// the effective limit for whatever decoded next.
func TestLeaveNestedClampsAtZero(t *testing.T) {
	t.Parallel()

	buffer, err := NewReadBuffer(nil, depthLimits(t, 2))
	if err != nil {
		t.Fatalf("NewReadBuffer() error = %v", err)
	}

	buffer.LeaveNested()
	buffer.LeaveNested()
	if got := buffer.NestingDepth(); got != 0 {
		t.Fatalf("depth = %d, want 0", got)
	}

	if err := buffer.EnterNested("value"); err != nil {
		t.Fatalf("EnterNested: %v", err)
	}
	if err := buffer.EnterNested("value"); err != nil {
		t.Fatalf("EnterNested: %v", err)
	}
	if err := buffer.EnterNested("value"); !errors.Is(err, ErrRecursionLimit) {
		t.Fatalf("error = %v, want the limit to still be 2", err)
	}
}

// TestSequentialBuffersDoNotAccumulateDepth states the property that matters
// in a session: one packet's nesting must not count against the next.
func TestSequentialBuffersDoNotAccumulateDepth(t *testing.T) {
	t.Parallel()

	limits := depthLimits(t, 2)

	for packet := range 3 {
		buffer, err := NewReadBuffer(nil, limits)
		if err != nil {
			t.Fatalf("NewReadBuffer() error = %v", err)
		}
		if err := buffer.EnterNested("value"); err != nil {
			t.Fatalf("packet %d: EnterNested: %v", packet, err)
		}
		if err := buffer.EnterNested("value"); err != nil {
			t.Fatalf("packet %d: EnterNested: %v", packet, err)
		}
		// Deliberately left entered: a decode that failed here must not
		// affect the buffer the next packet gets.
	}
}

func TestEnterNestedRejectsAnUnconstructedBuffer(t *testing.T) {
	t.Parallel()

	var buffer *Buffer
	if err := buffer.EnterNested("value"); !errors.Is(err, ErrInvalidLimits) {
		t.Fatalf("error = %v, want ErrInvalidLimits", err)
	}
	buffer.LeaveNested()
	if got := buffer.NestingDepth(); got != 0 {
		t.Fatalf("depth = %d, want 0", got)
	}
}
