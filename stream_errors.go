package protocol

import "errors"

var (
	// ErrInvalidStream reports a stream configuration that cannot run.
	ErrInvalidStream = errors.New("invalid managed stream")
	// ErrStreamNotStarted reports an operation that requires a started stream.
	ErrStreamNotStarted = errors.New("managed stream is not started")
	// ErrStreamStarted reports a second call to Start.
	ErrStreamStarted = errors.New("managed stream is already started")
	// ErrStreamClosing reports work submitted after graceful shutdown began.
	ErrStreamClosing = errors.New("managed stream is shutting down")
	// ErrStreamClosed reports work submitted to a terminated stream, and is
	// the cause Wait reports after an abortive local close.
	ErrStreamClosed = errors.New("managed stream is closed")
	// ErrMalformedInbound reports inbound bytes that the protocol rejects.
	// It always wraps the underlying framing, compression, or decode error.
	ErrMalformedInbound = errors.New("malformed inbound data")
	// ErrAmbiguousWrite reports a write that was abandoned after the
	// transport already accepted part of the frame, so the caller cannot know
	// how many bytes the peer received.
	ErrAmbiguousWrite = errors.New("ambiguous outbound write")
	// ErrEncryptionOverrun reports that a peer sent bytes past the point where
	// encryption began. The buffered bytes were read as plaintext and cannot
	// be recovered, so the stream cannot continue.
	ErrEncryptionOverrun = errors.New("peer sent bytes past the encryption switch point")
	// ErrEncryptionEnabled reports a second attempt to enable encryption.
	// Java Edition has no rekeying, so this is a caller bug.
	ErrEncryptionEnabled = errors.New("encryption is already enabled")
	// ErrEncryptionUnavailable reports an encryption control applied to a
	// stream whose conduit has already terminated.
	ErrEncryptionUnavailable = errors.New("encryption is unavailable")
	// ErrObservation reports a failed observation delivery. Observation is
	// lossless, so a sink failure terminates the stream.
	ErrObservation = errors.New("observation delivery failed")
)
