package replay_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/capture"
	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
	"github.com/go-theft-craft/minecraft-protocol/replay"
)

// resolver knows the one protocol these tests use.
var resolver = replay.ResolverFunc(func(id string) (protocol.Protocol, bool) {
	if id == v1_8.Protocol().ID() {
		return v1_8.Protocol(), true
	}

	return nil, false
})

// encodingSession builds the frames a fixture holds. It is the server half:
// only a server session encodes clientbound packets, and a capture taken from
// a client is a recording of what a server sent.
func encodingSession(t *testing.T) protocol.Session {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("NewLimits: %v", err)
	}
	session, err := v1_8.Protocol().NewSession(protocol.RoleServer, limits)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	session.ApplyTransition(protocol.Transition{Control: protocol.StateControl{State: v1_8.StatePlay}})

	return session
}

// buildFixture writes a capture of real protocol 47 frames.
//
// The frames come from the real encoder rather than from literals, so a replay
// of this capture exercises the same decode path a connection does.
func buildFixture(t *testing.T, gap time.Duration) []byte {
	t.Helper()

	session := encodingSession(t)

	var buffer bytes.Buffer
	writer, err := capture.NewWriter(&buffer, capture.Header{
		Protocol:          v1_8.Protocol().ID(),
		Role:              "client",
		FrameBytes:        2 << 20,
		DecompressedBytes: 8 << 20,
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	for index := range 4 {
		value := &v1_8.PlayClientboundKeepAlive{KeepAliveID: int32(index + 1)}
		packet := protocol.Packet{
			State:     v1_8.StatePlay,
			Direction: protocol.DirectionClientbound,
			ID:        value.PacketID(),
			Name:      "keep_alive",
			Value:     value,
		}

		body, err := session.EncodeFrame(packet)
		if err != nil {
			t.Fatalf("EncodeFrame: %v", err)
		}
		frame, err := session.Framer().BuildFrame(body)
		if err != nil {
			t.Fatalf("BuildFrame: %v", err)
		}

		snapshot := protocol.NewSnapshot(v1_8.StatePlay, nil)
		if err := writer.Observe(t.Context(), protocol.Observation{
			Sequence:    uint64(index + 1),
			Frame:       uint64(index + 1),
			Elapsed:     time.Duration(index) * gap,
			Direction:   protocol.DirectionClientbound,
			Stage:       protocol.ObservationRawFrame,
			Before:      snapshot,
			After:       snapshot,
			Bytes:       frame.WireBytes(),
			OriginalLen: len(frame.WireBytes()),
		}); err != nil {
			t.Fatalf("Observe: %v", err)
		}
	}

	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	return buffer.Bytes()
}

func newPlayer(t *testing.T, file []byte, options ...replay.Option) *replay.Player {
	t.Helper()

	reader, err := capture.NewReader(bytes.NewReader(file))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	player, err := replay.New(reader, options...)
	if err != nil {
		t.Fatalf("replay.New: %v", err)
	}

	return player
}

func trailerOf(t *testing.T, file []byte) capture.Trailer {
	t.Helper()

	reader, err := capture.NewReader(bytes.NewReader(file))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	for {
		if _, err := reader.Next(); err != nil {
			break
		}
	}
	trailer, ok := reader.Trailer()
	if !ok {
		t.Fatal("fixture capture has no trailer")
	}

	return trailer
}

func TestAnOfflineReplayReproducesTheCapturesDigest(t *testing.T) {
	t.Parallel()

	file := buildFixture(t, 0)
	player := newPlayer(t, file, replay.WithResolver(resolver))

	result, err := player.Run(t.Context())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Records != 4 {
		t.Fatalf("replayed %d records, want 4", result.Records)
	}
	if want := trailerOf(t, file).Digest; result.Digest != want {
		t.Fatalf("replay digest = %q, capture recorded %q", result.Digest, want)
	}
	if len(result.Divergences) != 0 {
		t.Fatalf("replay diverged from the capture: %v", result.Divergences)
	}
}

func TestTwoRunsProduceTheSameDigest(t *testing.T) {
	t.Parallel()

	file := buildFixture(t, 0)

	first, err := newPlayer(t, file, replay.WithResolver(resolver)).Run(t.Context())
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	second, err := newPlayer(t, file, replay.WithResolver(resolver)).Run(t.Context())
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if first.Digest != second.Digest {
		t.Fatalf("two replays of one capture digest differently: %q then %q", first.Digest, second.Digest)
	}
}

// TestAMutatedCaptureIsReportedAsAValue is why the digest exists as a return
// rather than a log line: a verification run has to be able to act on it.
func TestAMutatedCaptureIsReportedAsAValue(t *testing.T) {
	t.Parallel()

	file := buildFixture(t, 0)
	recorded := trailerOf(t, file).Digest

	mutated := buildFixtureWithID(t, 99)
	result, err := newPlayer(t, mutated, replay.WithResolver(resolver)).Run(t.Context())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if result.Digest == recorded {
		t.Fatal("a capture of different bytes produced the same digest")
	}
}

// buildFixtureWithID writes a capture whose first packet carries a different
// keepalive ID, so its bytes differ by one field.
func buildFixtureWithID(t *testing.T, id int32) []byte {
	t.Helper()

	session := encodingSession(t)

	var buffer bytes.Buffer
	writer, err := capture.NewWriter(&buffer, capture.Header{
		Protocol:          v1_8.Protocol().ID(),
		Role:              "client",
		FrameBytes:        2 << 20,
		DecompressedBytes: 8 << 20,
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	value := &v1_8.PlayClientboundKeepAlive{KeepAliveID: id}
	body, err := session.EncodeFrame(protocol.Packet{
		State:     v1_8.StatePlay,
		Direction: protocol.DirectionClientbound,
		ID:        value.PacketID(),
		Value:     value,
	})
	if err != nil {
		t.Fatalf("EncodeFrame: %v", err)
	}
	frame, err := session.Framer().BuildFrame(body)
	if err != nil {
		t.Fatalf("BuildFrame: %v", err)
	}

	snapshot := protocol.NewSnapshot(v1_8.StatePlay, nil)
	if err := writer.Observe(t.Context(), protocol.Observation{
		Sequence:  1,
		Frame:     1,
		Direction: protocol.DirectionClientbound,
		Stage:     protocol.ObservationRawFrame,
		Before:    snapshot,
		After:     snapshot,
		Bytes:     frame.WireBytes(),
	}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	return buffer.Bytes()
}

func TestRecordedModeHonoursTheRecordedGap(t *testing.T) {
	t.Parallel()

	const gap = 50 * time.Millisecond

	file := buildFixture(t, gap)
	player := newPlayer(t, file, replay.WithResolver(resolver), replay.WithMode(replay.ModeRecorded))

	started := time.Now()
	result, err := player.Run(t.Context())
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	elapsed := time.Since(started)

	// Four records at 50ms apart: the last one is 150ms into the capture.
	if elapsed < 3*gap {
		t.Fatalf("recorded replay took %v, want at least %v", elapsed, 3*gap)
	}
	if elapsed > 3*gap+2*time.Second {
		t.Fatalf("recorded replay took %v, far beyond the recorded timing", elapsed)
	}
	if result.Drift < 0 {
		t.Fatalf("drift = %v, want a non-negative measurement", result.Drift)
	}
}

func TestScaledModeWithZeroBehavesAsFast(t *testing.T) {
	t.Parallel()

	file := buildFixture(t, 500*time.Millisecond)
	player := newPlayer(
		t, file,
		replay.WithResolver(resolver),
		replay.WithMode(replay.ModeScaled),
		replay.WithScale(0),
	)

	started := time.Now()
	if _, err := player.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("scale 0 took %v, want no waiting at all", elapsed)
	}
}

func TestStepModeReturnsOneRecordPerCall(t *testing.T) {
	t.Parallel()

	file := buildFixture(t, time.Second)
	player := newPlayer(t, file, replay.WithResolver(resolver), replay.WithMode(replay.ModeStep))

	for expected := uint64(1); expected <= 4; expected++ {
		record, err := player.Next(t.Context())
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if record.Sequence != expected {
			t.Fatalf("Next returned sequence %d, want %d", record.Sequence, expected)
		}
	}
	if _, err := player.Next(t.Context()); !errors.Is(err, capture.ErrEndOfCapture) {
		t.Fatalf("Next after the last record = %v, want ErrEndOfCapture", err)
	}
}

func TestCancellationStopsAReplayPromptly(t *testing.T) {
	t.Parallel()

	file := buildFixture(t, time.Hour)
	player := newPlayer(t, file, replay.WithResolver(resolver), replay.WithMode(replay.ModeRecorded))

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err := player.Run(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run error = %v, want the context's error", err)
	}
	if elapsed := time.Since(started); elapsed > 30*time.Second {
		t.Fatalf("cancellation took %v: a replay must not wait out the next gap", elapsed)
	}
}

// TestARedactedRecordCannotBeSentToAPeer is the one place redaction is fatal.
// Offline it is honest to skip a withheld body; on a connection it would mean
// sending a frame that is not the frame.
func TestARedactedRecordCannotBeSentToAPeer(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	writer, err := capture.NewWriter(&buffer, capture.Header{
		Protocol:          v1_8.Protocol().ID(),
		FrameBytes:        1 << 20,
		DecompressedBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Observe(t.Context(), protocol.Observation{
		Sequence:    1,
		Direction:   protocol.DirectionServerbound,
		Stage:       protocol.ObservationRawFrame,
		Redacted:    true,
		OriginalLen: 128,
	}); err != nil {
		t.Fatalf("Observe: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	var peer bytes.Buffer
	player := newPlayer(t, buffer.Bytes(), replay.WithTransport(
		protocol.Transport{Writer: &peer},
		protocol.DirectionServerbound,
	))

	if _, err := player.Run(t.Context()); !errors.Is(err, replay.ErrRedactedRecord) {
		t.Fatalf("Run error = %v, want ErrRedactedRecord", err)
	}
	if peer.Len() != 0 {
		t.Fatal("a redacted record put bytes on the connection anyway")
	}
}

func TestAConnectedReplaySendsOnlyTheSelectedDirection(t *testing.T) {
	t.Parallel()

	file := buildFixture(t, 0)

	var peer bytes.Buffer
	player := newPlayer(t, file, replay.WithTransport(
		protocol.Transport{Writer: &peer},
		protocol.DirectionServerbound,
	))
	if _, err := player.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if peer.Len() != 0 {
		t.Fatalf("sent %d bytes, want none: the capture holds clientbound frames", peer.Len())
	}

	peer.Reset()
	player = newPlayer(t, file, replay.WithTransport(
		protocol.Transport{Writer: &peer},
		protocol.DirectionClientbound,
	))
	if _, err := player.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if peer.Len() == 0 {
		t.Fatal("sent nothing in the direction the capture holds")
	}
}

func TestAnUnknownProtocolIsNamed(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	writer, err := capture.NewWriter(&buffer, capture.Header{
		Protocol:          "java/9999",
		FrameBytes:        1 << 20,
		DecompressedBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reader, err := capture.NewReader(bytes.NewReader(buffer.Bytes()))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if _, err := replay.New(reader, replay.WithResolver(resolver)); !errors.Is(err, replay.ErrUnknownProtocol) {
		t.Fatalf("replay.New error = %v, want ErrUnknownProtocol", err)
	}
}

func TestAPlayerWithNoDestinationIsRefused(t *testing.T) {
	t.Parallel()

	reader, err := capture.NewReader(bytes.NewReader(buildFixture(t, 0)))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	if _, err := replay.New(reader); !errors.Is(err, replay.ErrInvalidPlayer) {
		t.Fatalf("replay.New error = %v, want ErrInvalidPlayer", err)
	}
}

func TestWithTransportRequiresADirection(t *testing.T) {
	t.Parallel()

	reader, err := capture.NewReader(bytes.NewReader(buildFixture(t, 0)))
	if err != nil {
		t.Fatalf("NewReader: %v", err)
	}
	_, err = replay.New(reader, replay.WithTransport(protocol.Transport{Writer: &bytes.Buffer{}}, 0))
	if !errors.Is(err, replay.ErrInvalidPlayer) {
		t.Fatalf("replay.New error = %v, want ErrInvalidPlayer", err)
	}
}
