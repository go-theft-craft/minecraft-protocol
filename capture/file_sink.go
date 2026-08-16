package capture

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
)

// ErrCaptureExists reports a path that already holds a file. Overwriting a
// capture destroys evidence of a connection someone chose to record, so it
// takes WithOverwrite to do it.
var ErrCaptureExists = errors.New("capture file already exists")

// Default flush policy. A capture is written from the observation path, which
// backpressures the connection, so the defaults favour steady small writes
// over long stalls.
const (
	defaultFlushBytes    = 64 << 10
	defaultFlushInterval = time.Second
)

// FileSink writes a capture to a file, buffered.
//
// The buffer is what keeps the observation path off the disk on every record;
// the flush policy is what keeps a killed process from losing more than the
// last fraction of a second. Neither is a per-record fsync: that would put a
// disk round trip inside a connection's read loop.
type FileSink struct {
	file      *os.File
	buffered  *bufio.Writer
	writer    *Writer
	sinceLast int
	lastFlush time.Time

	flushBytes    int
	flushInterval time.Duration
	closed        bool
}

// FileOption configures a file sink.
type FileOption func(*fileConfig) error

type fileConfig struct {
	flushBytes    int
	flushInterval time.Duration
	overwrite     bool
	writerOptions []WriterOption
}

// WithFlushBytes sets how many bytes may sit in the buffer before it is
// flushed to the file.
func WithFlushBytes(bytes int) FileOption {
	return func(c *fileConfig) error {
		if bytes <= 0 {
			return fmt.Errorf("%w: flush threshold must be positive, got %d", ErrInvalidCapture, bytes)
		}
		c.flushBytes = bytes

		return nil
	}
}

// WithFlushInterval sets how long buffered bytes may wait before the next
// record flushes them.
//
// The interval is checked when a record arrives rather than driven by a timer.
// A background timer would need its own synchronisation with the writer, and
// would buy nothing: a capture with nothing arriving has nothing to lose.
func WithFlushInterval(interval time.Duration) FileOption {
	return func(c *fileConfig) error {
		if interval <= 0 {
			return fmt.Errorf("%w: flush interval must be positive, got %v", ErrInvalidCapture, interval)
		}
		c.flushInterval = interval

		return nil
	}
}

// WithOverwrite allows replacing a file that already exists.
func WithOverwrite() FileOption {
	return func(c *fileConfig) error {
		c.overwrite = true

		return nil
	}
}

// WithWriterOptions passes options through to the underlying writer, which is
// how a file sink is made to disclose.
func WithWriterOptions(options ...WriterOption) FileOption {
	return func(c *fileConfig) error {
		c.writerOptions = append(c.writerOptions, options...)

		return nil
	}
}

// NewFileSink creates the file and writes the header.
func NewFileSink(path string, header Header, options ...FileOption) (*FileSink, error) {
	config := fileConfig{flushBytes: defaultFlushBytes, flushInterval: defaultFlushInterval}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil file option", ErrInvalidCapture)
		}
		if err := option(&config); err != nil {
			return nil, err
		}
	}

	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if !config.overwrite {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}

	// 0600: a capture holds session content, and the default umask is not a
	// decision anyone made about this file.
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil, fmt.Errorf("%w: %s", ErrCaptureExists, path)
		}

		return nil, fmt.Errorf("create capture: %w", err)
	}

	buffered := bufio.NewWriterSize(file, config.flushBytes)
	writer, err := NewWriter(buffered, header, config.writerOptions...)
	if err != nil {
		_ = file.Close()
		_ = os.Remove(path)

		return nil, err
	}

	return &FileSink{
		file:          file,
		buffered:      buffered,
		writer:        writer,
		lastFlush:     time.Now(),
		flushBytes:    config.flushBytes,
		flushInterval: config.flushInterval,
	}, nil
}

// Header returns the header written to the file.
func (s *FileSink) Header() Header { return s.writer.Header() }

// Observe implements protocol.ObservationSink.
func (s *FileSink) Observe(ctx context.Context, observation protocol.Observation) error {
	if s.closed {
		return fmt.Errorf("%w: file sink is closed", ErrInvalidCapture)
	}

	before := s.buffered.Buffered()
	if err := s.writer.Observe(ctx, observation); err != nil {
		return err
	}
	s.sinceLast += s.buffered.Buffered() - before + len(observation.Bytes)

	if s.sinceLast >= s.flushBytes || time.Since(s.lastFlush) >= s.flushInterval {
		return s.flush()
	}

	return nil
}

func (s *FileSink) flush() error {
	if err := s.buffered.Flush(); err != nil {
		return fmt.Errorf("flush capture: %w", err)
	}
	s.sinceLast = 0
	s.lastFlush = time.Now()

	return nil
}

// Close writes the trailer, flushes, syncs, and closes the file. It is
// idempotent.
//
// The sync is here and nowhere else. A capture that reaches the page cache is
// readable by anything on the machine; one that reaches the disk survives the
// machine, and that is worth exactly one round trip, at the end.
func (s *FileSink) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true

	writeErr := s.writer.Close()
	flushErr := s.buffered.Flush()
	syncErr := s.file.Sync()
	closeErr := s.file.Close()

	return errors.Join(writeErr, flushErr, syncErr, closeErr)
}

var _ protocol.ObservationSink = (*FileSink)(nil)
