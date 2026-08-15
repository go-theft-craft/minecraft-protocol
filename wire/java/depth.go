package java

import (
	"fmt"
	"io"
)

// EnterNested records entry into one level of nested decoding and rejects a
// value nested past Limits.RecursionDepth.
//
// A generated decoder calls it on the way into a named type and LeaveNested on
// the way out. The counter exists because protocol schemas contain mutually
// recursive types: a slot holds components, a component can hold a predicate,
// and a predicate can hold a slot. Nothing in the wire format bounds how deep a
// peer nests those, so without a counter a hostile packet is a stack overflow,
// which no recover can turn back into a decode error.
//
// The counter lives on the buffer rather than in generated code because the
// buffer is already the thing that owns one packet's decode and already holds
// the limits. There is no synchronisation: a buffer belongs to one session,
// which is driven by one coordinator goroutine.
func (b *Buffer) EnterNested(path string) error {
	if b == nil || !b.limits.Valid() {
		return withPath(path, ErrInvalidLimits)
	}
	if b.depth >= b.limits.RecursionDepth() {
		return withPath(path, fmt.Errorf(
			"%w: depth %d exceeds limit %d",
			ErrRecursionLimit,
			b.depth+1,
			b.limits.RecursionDepth(),
		))
	}
	b.depth++

	return nil
}

// LeaveNested records the exit from one level of nested decoding.
//
// It is safe to call without a matching EnterNested, because a decoder that
// fails partway unwinds through paths that did not all enter. Clamping at zero
// keeps a failed decode from leaving the counter negative and silently raising
// the effective limit for the next packet.
func (b *Buffer) LeaveNested() {
	if b == nil || b.depth == 0 {
		return
	}
	b.depth--
}

// NestingDepth reports the current nesting level. It exists for tests and for
// diagnostics; decoding does not consult it.
func (b *Buffer) NestingDepth() int {
	if b == nil {
		return 0
	}

	return b.depth
}

// ReadTerminator reports whether the next byte is the loop terminator,
// consuming it only when it is.
//
// A terminated loop -- Java Edition's entity metadata is the only one in
// protocol 47 -- ends at a sentinel byte rather than at a count, and that byte
// occupies the same position as the first byte of an entry. The peek is what
// lets the generated element decoder read that byte itself when the loop
// continues, so the loop needs no knowledge of the element's layout.
//
// The terminator differs between protocol versions: 127 in protocol 47 and 255
// in protocol 775. It is a parameter for that reason, taken from the schema
// rather than compiled in.
func (b *Buffer) ReadTerminator(path string, terminator uint8) (bool, error) {
	if err := b.requireMode(readMode); err != nil {
		return false, withPath(path, err)
	}
	if b.offset >= len(b.data) {
		return false, withPath(path, io.ErrUnexpectedEOF)
	}
	if b.data[b.offset] != terminator {
		return false, nil
	}
	b.offset++

	return true, nil
}

// WriteTerminator writes the byte that ends a terminated loop.
func (b *Buffer) WriteTerminator(path string, terminator uint8) error {
	return b.WriteU8(path, terminator)
}
