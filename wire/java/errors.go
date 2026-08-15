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
	// ErrTrailingBytes reports unread bytes after a complete value.
	ErrTrailingBytes = errors.New("trailing bytes")
	// ErrValueOutOfRange reports a value that does not fit its wire representation.
	ErrValueOutOfRange = errors.New("value is out of range")
	// ErrInvalidNBT reports malformed named binary tag data.
	ErrInvalidNBT = errors.New("invalid NBT")
	// ErrDuplicateNBTKey reports a repeated key in one NBT compound.
	ErrDuplicateNBTKey = errors.New("duplicate NBT compound key")
	// ErrRecursionLimit reports a value nested beyond the configured depth.
	ErrRecursionLimit = errors.New("recursion depth exceeds configured limit")
	// ErrInvalidSlot reports a slot that cannot be represented on the wire.
	ErrInvalidSlot = errors.New("invalid slot")
	// ErrInvalidMetadata reports malformed entity metadata.
	ErrInvalidMetadata = errors.New("invalid entity metadata")
	// ErrDuplicateMetadataIndex reports a repeated entity metadata index.
	ErrDuplicateMetadataIndex = errors.New("duplicate entity metadata index")
	// ErrInvalidSharedSecret reports a session key of the wrong length.
	ErrInvalidSharedSecret = errors.New("invalid shared secret")
	// ErrWrongBufferMode reports a read from a write buffer or a write to a read buffer.
	ErrWrongBufferMode = errors.New("wrong buffer mode")
	// ErrInvalidCompression reports a malformed compression envelope or an
	// unusable compression configuration.
	ErrInvalidCompression = errors.New("invalid compression envelope")
	// ErrDecompressedTooLarge reports a packet body beyond the configured
	// decompressed limit.
	ErrDecompressedTooLarge = errors.New("decompressed packet body exceeds configured limit")
	// ErrCompressionPolicy reports an envelope that a compression policy
	// refused even though it was structurally valid.
	ErrCompressionPolicy = errors.New("compression policy violation")
)

// PathError attaches a generated packet field path to a wire error.
type PathError struct {
	Path string
	Err  error
}

// Error implements error.
func (e *PathError) Error() string {
	return fmt.Sprintf("field %s: %v", e.Path, e.Err)
}

// Unwrap returns the underlying wire error.
func (e *PathError) Unwrap() error { return e.Err }

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

func withPath(path string, err error) error {
	if err == nil {
		return nil
	}
	return &PathError{Path: path, Err: err}
}
