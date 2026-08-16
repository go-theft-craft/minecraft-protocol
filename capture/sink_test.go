package capture_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/capture"
)

// countingSink records the order sinks were called in.
type countingSink struct {
	order *[]string
	name  string
	err   error
}

func (s countingSink) Observe(context.Context, protocol.Observation) error {
	*s.order = append(*s.order, s.name)

	return s.err
}

func TestFileSinkCreatesAPrivateFile(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.mcpcap")
	sink, err := capture.NewFileSink(path, testHeader())
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("capture mode = %o, want 0600: it holds session content", mode)
	}
}

func TestFileSinkRefusesToOverwrite(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.mcpcap")
	if err := os.WriteFile(path, []byte("existing"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	if _, err := capture.NewFileSink(path, testHeader()); !errors.Is(err, capture.ErrCaptureExists) {
		t.Fatalf("NewFileSink error = %v, want ErrCaptureExists", err)
	}

	sink, err := capture.NewFileSink(path, testHeader(), capture.WithOverwrite())
	if err != nil {
		t.Fatalf("NewFileSink with overwrite: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if bytes.Contains(content, []byte("existing")) {
		t.Fatal("the overwritten file still holds the old content")
	}
}

func TestFileSinkFlushesOnTheByteThreshold(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.mcpcap")
	sink, err := capture.NewFileSink(path, testHeader(), capture.WithFlushBytes(1))
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	if err := sink.Observe(t.Context(), observation(1, []byte{0x01, 0x02})); err != nil {
		t.Fatalf("Observe: %v", err)
	}

	// With a one-byte threshold every record reaches the file immediately, so
	// a reader opening it now sees the record without the sink being closed.
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	reader, err := capture.NewReader(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	record, err := reader.Next()
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if record.Sequence != 1 {
		t.Fatalf("read sequence %d, want the flushed record", record.Sequence)
	}
}

func TestFileSinkCloseWritesTheTrailer(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.mcpcap")
	sink, err := capture.NewFileSink(path, testHeader())
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	for sequence := 1; sequence <= 3; sequence++ {
		if err := sink.Observe(t.Context(), observation(uint64(sequence), []byte{0x01})); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("second Close: %v, want it to be idempotent", err)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	reader, records := readAll(t, content)
	if len(records) != 3 {
		t.Fatalf("read %d records, want 3", len(records))
	}
	if !reader.Complete() {
		t.Fatal("a closed capture must read as complete")
	}
}

// TestAFileSinkKilledWithoutCloseIsStillReadable is why the flush policy has a
// byte threshold at all. A capture is most useful exactly when the process
// taking it did not get to finish.
func TestAFileSinkKilledWithoutCloseIsStillReadable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "session.mcpcap")
	sink, err := capture.NewFileSink(path, testHeader(), capture.WithFlushBytes(1))
	if err != nil {
		t.Fatalf("NewFileSink: %v", err)
	}
	t.Cleanup(func() { _ = sink.Close() })

	for sequence := 1; sequence <= 4; sequence++ {
		if err := sink.Observe(t.Context(), observation(uint64(sequence), []byte{0x01})); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}
	// The process dies here: no Close, no trailer.

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	reader, err := capture.NewReader(bytes.NewReader(content))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}

	var count int
	for {
		if _, err := reader.Next(); err != nil {
			break
		}
		count++
	}
	if count != 4 {
		t.Fatalf("read %d records, want the 4 that were flushed", count)
	}
	if reader.Complete() {
		t.Fatal("a capture with no trailer read as complete")
	}
}

func TestMultiSinkCallsSinksInOrder(t *testing.T) {
	t.Parallel()

	var order []string
	sink, err := capture.MultiSink(
		countingSink{order: &order, name: "first"},
		countingSink{order: &order, name: "second"},
	)
	if err != nil {
		t.Fatalf("MultiSink: %v", err)
	}

	if err := sink.Observe(t.Context(), observation(1, nil)); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("order = %v, want the sinks in the order given", order)
	}
}

func TestMultiSinkPropagatesTheFirstFailure(t *testing.T) {
	t.Parallel()

	refusal := errors.New("disk full")
	var order []string

	sink, err := capture.MultiSink(
		countingSink{order: &order, name: "first", err: refusal},
		countingSink{order: &order, name: "second"},
	)
	if err != nil {
		t.Fatalf("MultiSink: %v", err)
	}

	if err := sink.Observe(t.Context(), observation(1, nil)); !errors.Is(err, refusal) {
		t.Fatalf("Observe error = %v, want the sink's failure", err)
	}
	// The stream terminates on an observation failure, so running the rest
	// would be work on behalf of a connection that is already ending.
	if len(order) != 1 {
		t.Fatalf("order = %v, want the composition to stop at the failure", order)
	}
}

func TestMultiSinkRejectsANilSink(t *testing.T) {
	t.Parallel()

	if _, err := capture.MultiSink(nil); !errors.Is(err, capture.ErrInvalidCapture) {
		t.Fatalf("MultiSink error = %v, want ErrInvalidCapture", err)
	}
	if _, err := capture.MultiSink(); !errors.Is(err, capture.ErrInvalidCapture) {
		t.Fatalf("MultiSink() error = %v, want ErrInvalidCapture", err)
	}
}

func TestFileSinkRejectsAnUnwritablePath(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "no-such-directory", "session.mcpcap")
	if _, err := capture.NewFileSink(path, testHeader()); err == nil {
		t.Fatal("NewFileSink accepted a path it cannot create")
	}
}
