// Package java implements Java Edition wire values.
package java

import (
	"errors"
	"fmt"
)

var (
	// ErrInvalidLimits reports an unconstructed protocol.Limits value.
	ErrInvalidLimits = errors.New("invalid protocol limits")
	// ErrVarIntTooLong reports a VarInt with more than five encoded bytes.
	ErrVarIntTooLong = errors.New("VarInt is too long")
	// ErrVarLongTooLong reports a VarLong with more than ten encoded bytes.
	ErrVarLongTooLong = errors.New("VarLong is too long")
	// ErrValueTooLarge reports a length-prefixed value beyond its configured limit.
	ErrValueTooLarge = errors.New("value exceeds configured limit")
	// ErrFrameTooLarge reports a frame beyond its configured limit.
	ErrFrameTooLarge = errors.New("frame exceeds configured limit")
	// ErrNegativeLength reports a negative length-prefixed value length.
	ErrNegativeLength = errors.New("negative length")
)

// SizeError describes a value that exceeds a configured size limit.
type SizeError struct {
	Value string
	Size  int
	Limit int
}

// Error implements error.
func (e *SizeError) Error() string {
	return fmt.Sprintf("%s size %d exceeds limit %d", e.Value, e.Size, e.Limit)
}

// Unwrap returns the sentinel error for a size violation.
func (e *SizeError) Unwrap() error {
	return ErrValueTooLarge
}
