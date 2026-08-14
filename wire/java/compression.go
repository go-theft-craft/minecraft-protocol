package java

import (
	"bytes"
	"compress/zlib"
	"errors"
	"fmt"
	"io"

	"github.com/go-theft-craft/minecraft-protocol"
)

// CompressionControl configures the Java Edition compression envelope. It is a
// protocol.Control, so a session applies it at a frame boundary rather than
// between the bytes of one frame.
type CompressionControl struct {
	// Enabled reports whether frames carry a data-length envelope at all.
	Enabled bool
	// Threshold is the smallest packet body that is compressed. Bodies below
	// it travel uncompressed inside the envelope.
	Threshold int32
	// Policy decides how strictly a peer must honour the threshold.
	Policy CompressionPolicy
}

// ControlName implements protocol.Control.
func (CompressionControl) ControlName() string { return "java.compression" }

var _ protocol.Control = CompressionControl{}

func (c CompressionControl) validate() error {
	if !c.Enabled {
		return nil
	}
	if c.Threshold < 0 {
		return fmt.Errorf(
			"%w: enabled compression has negative threshold %d",
			ErrInvalidCompression,
			c.Threshold,
		)
	}
	if c.Policy == nil {
		return fmt.Errorf("%w: enabled compression has no policy", ErrInvalidCompression)
	}

	return nil
}

// CompressionValidation describes one decoded envelope for a policy. Every
// mandatory safety check has already passed by the time a policy sees it.
type CompressionValidation struct {
	// Compressed reports whether the envelope carried a zlib stream.
	Compressed bool
	// EncodedBytes is the size the packet body occupied inside the envelope.
	EncodedBytes int
	// DecompressedBytes is the size of the recovered packet body.
	DecompressedBytes int
	// Threshold is the configured compression threshold.
	Threshold int32
}

// CompressionPolicy decides whether a peer honoured the threshold. A policy
// can only tighten acceptance: it never relaxes frame limits, decompressed
// limits, exact-length checks, or zlib validity.
type CompressionPolicy interface {
	Name() string
	ValidateThreshold(CompressionValidation) error
}

type strictCompression struct{}

func (strictCompression) Name() string { return "strict" }

func (strictCompression) ValidateThreshold(validation CompressionValidation) error {
	switch {
	case validation.Compressed && validation.DecompressedBytes < int(validation.Threshold):
		return fmt.Errorf(
			"%w: compressed body of %d bytes is below threshold %d",
			ErrCompressionPolicy,
			validation.DecompressedBytes,
			validation.Threshold,
		)
	case !validation.Compressed && validation.DecompressedBytes >= int(validation.Threshold):
		return fmt.Errorf(
			"%w: uncompressed body of %d bytes is at or above threshold %d",
			ErrCompressionPolicy,
			validation.DecompressedBytes,
			validation.Threshold,
		)
	default:
		return nil
	}
}

type compatibleCompression struct{}

func (compatibleCompression) Name() string { return "compatible" }

// ValidateThreshold accepts either envelope form. Implementations in the wild
// compress packets a strict reading would leave alone, and refusing to talk to
// them is worse than accepting a larger frame we already bounded.
func (compatibleCompression) ValidateThreshold(CompressionValidation) error { return nil }

// StrictCompression requires a peer to compress exactly the packets the
// threshold selects.
var StrictCompression CompressionPolicy = strictCompression{}

// CompatibleCompression accepts any threshold choice a peer makes.
var CompatibleCompression CompressionPolicy = compatibleCompression{}

// DecodeCompression recovers one packet body from a frame payload.
//
// The returned slice may alias framePayload when no decompression was needed,
// so a caller that retains the body must copy it.
func DecodeCompression(
	framePayload []byte,
	control CompressionControl,
	limits protocol.Limits,
) ([]byte, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}
	if err := control.validate(); err != nil {
		return nil, err
	}
	if !control.Enabled {
		return framePayload, nil
	}

	declared, declaredBytes, err := ReadVarInt(bytes.NewReader(framePayload))
	if err != nil {
		return nil, fmt.Errorf("read compression data length: %w", err)
	}
	envelope := framePayload[declaredBytes:]

	if declared < 0 {
		return nil, fmt.Errorf("compression data length %d: %w", declared, ErrNegativeLength)
	}

	if declared == 0 {
		// The decompressed limit bounds the recovered packet body on every
		// path, not only the one that inflates a stream.
		if len(envelope) > limits.DecompressedBytes() {
			return nil, fmt.Errorf(
				"uncompressed packet body of %d bytes exceeds limit %d: %w",
				len(envelope),
				limits.DecompressedBytes(),
				ErrDecompressedTooLarge,
			)
		}

		validation := CompressionValidation{
			EncodedBytes:      len(envelope),
			DecompressedBytes: len(envelope),
			Threshold:         control.Threshold,
		}
		if err := control.Policy.ValidateThreshold(validation); err != nil {
			return nil, err
		}

		return envelope, nil
	}

	// Bound the allocation by the configured limit before reading a single
	// compressed byte, so a declared length cannot drive memory use.
	if int(declared) > limits.DecompressedBytes() {
		return nil, fmt.Errorf(
			"compression data length %d exceeds limit %d: %w",
			declared,
			limits.DecompressedBytes(),
			ErrDecompressedTooLarge,
		)
	}

	body, err := inflateExact(envelope, int(declared))
	if err != nil {
		return nil, err
	}

	validation := CompressionValidation{
		Compressed:        true,
		EncodedBytes:      len(envelope),
		DecompressedBytes: len(body),
		Threshold:         control.Threshold,
	}
	if err := control.Policy.ValidateThreshold(validation); err != nil {
		return nil, err
	}

	return body, nil
}

// inflateExact decompresses exactly one zlib stream and requires it to produce
// exactly size bytes and to end the envelope.
func inflateExact(envelope []byte, size int) ([]byte, error) {
	source := bytes.NewReader(envelope)
	decompressor, err := zlib.NewReader(source)
	if err != nil {
		return nil, fmt.Errorf("open compressed packet body: %w", errors.Join(err, ErrInvalidCompression))
	}
	defer func() { _ = decompressor.Close() }()

	body := make([]byte, size)
	// LimitReader keeps a hostile stream from expanding beyond the declared
	// size even before the exactness check below runs.
	if _, err := io.ReadFull(io.LimitReader(decompressor, int64(size)), body); err != nil {
		return nil, fmt.Errorf("read compressed packet body: %w", errors.Join(err, ErrInvalidCompression))
	}

	// A reader may return data together with io.EOF, so the byte count decides
	// whether the stream really ended at the declared length.
	var overflow [1]byte
	extra, err := decompressor.Read(overflow[:])
	if extra > 0 {
		return nil, fmt.Errorf(
			"%w: compressed packet body is larger than its declared length %d",
			ErrInvalidCompression,
			size,
		)
	}
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("finish compressed packet body: %w", errors.Join(err, ErrInvalidCompression))
	}

	if source.Len() != 0 {
		return nil, fmt.Errorf(
			"compressed packet body has %d bytes after the zlib stream: %w",
			source.Len(),
			ErrTrailingBytes,
		)
	}

	return body, nil
}

// EncodeCompression wraps one packet body in the configured frame envelope.
//
// The returned slice may alias packetBody when compression is disabled.
func EncodeCompression(
	packetBody []byte,
	control CompressionControl,
	limits protocol.Limits,
) ([]byte, error) {
	if err := validateLimits(limits); err != nil {
		return nil, err
	}
	if err := control.validate(); err != nil {
		return nil, err
	}

	if !control.Enabled {
		if len(packetBody) > limits.FrameBytes() {
			return nil, frameLimitError(len(packetBody), limits)
		}

		return packetBody, nil
	}

	if len(packetBody) > limits.DecompressedBytes() {
		return nil, fmt.Errorf(
			"packet body of %d bytes exceeds decompressed limit %d: %w",
			len(packetBody),
			limits.DecompressedBytes(),
			ErrDecompressedTooLarge,
		)
	}

	if len(packetBody) < int(control.Threshold) {
		envelope := make([]byte, 1+len(packetBody))
		PutVarInt(envelope, 0)
		copy(envelope[1:], packetBody)
		if len(envelope) > limits.FrameBytes() {
			return nil, frameLimitError(len(envelope), limits)
		}

		return envelope, nil
	}

	var header [5]byte
	headerBytes := PutVarInt(header[:], int32(len(packetBody)))

	var compressed bytes.Buffer
	compressed.Write(header[:headerBytes])

	compressor := zlib.NewWriter(&compressed)
	if _, err := compressor.Write(packetBody); err != nil {
		return nil, fmt.Errorf("compress packet body: %w", err)
	}
	if err := compressor.Close(); err != nil {
		return nil, fmt.Errorf("finish compressed packet body: %w", err)
	}

	if compressed.Len() > limits.FrameBytes() {
		return nil, frameLimitError(compressed.Len(), limits)
	}

	return compressed.Bytes(), nil
}

func frameLimitError(size int, limits protocol.Limits) error {
	return fmt.Errorf(
		"compression envelope of %d bytes exceeds frame limit %d: %w",
		size,
		limits.FrameBytes(),
		ErrFrameTooLarge,
	)
}
