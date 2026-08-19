//go:build livecheck

package livecheck

import (
	"context"
	"errors"
	"os"
	"strconv"
	"testing"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v26_1 "github.com/go-theft-craft/minecraft-protocol/generated/java/v26_1"
)

// playWindowVariable overrides how long the play check stays connected, in
// seconds. The default is long enough for a default world at the server's own
// view distance to finish streaming its chunks and for at least one keep-alive
// round trip; a larger world, a slower disk, or a farther view distance wants
// more.
const playWindowVariable = "MCPROTO_LIVE_PLAY_SECONDS"

const defaultPlayWindow = 60 * time.Second

// chunksPerTick is what this check asks the server to send. It is a request
// rather than a measurement: vanilla times each batch and ramps its own rate,
// and this number only has to be inside the band the server will accept.
const chunksPerTick = 8.0

// requestedViewDistance is what this check asks for in its settings packet.
// The server clamps it to its own view-distance, so asking for more than any
// server allows means the measurement covers whatever the server under test
// is configured to send.
//
// It matters more than it looks. Without a settings packet a vanilla server
// uses a client view distance of 2 and streams 49 chunks, so a run that
// skipped it would report the largest chunk out of a handful near spawn as
// though it were the largest chunk a server sends.
const requestedViewDistance = 32

// TestLivePlayMeasuresLimits reaches play, stays there, and measures the
// largest raw frame and the largest decoded body play produces.
//
// This is the measurement the login check could not make. Login is a bounded
// sequence and its largest frame is registry data; play is unbounded and its
// largest frame is chunk data, which no check in this repository had ever put
// in front of the default limits.
//
// It runs with the default limits on purpose, for the reason the login check
// does: a frame the defaults reject is the result.
func TestLivePlayMeasuresLimits(t *testing.T) {
	address := os.Getenv(liveAddressVariable)
	if address == "" {
		t.Skipf("%s is unset; see livecheck/README.md", liveAddressVariable)
	}

	window := playWindow(t)

	// The budget covers the whole connection: login, the play window, and the
	// shutdown that follows it.
	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout+window)
	defer cancel()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("NewLimits: %v", err)
	}

	sink := newSizeSink()
	stream, closeStream := dialAndReachPlay(ctx, t, address, limits, sink)
	defer closeStream()

	requestChunks(ctx, t, stream)

	deadline := time.Now().Add(window)
	loaded := false
	for time.Now().Before(deadline) {
		readCtx, cancelRead := context.WithDeadline(ctx, deadline)
		packet, err := stream.Read(readCtx)
		cancelRead()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				break
			}
			t.Fatalf("read in play: %v (%s)", err, describe(ctx, t, stream))
		}

		if answer, sendLoaded := answerPlay(packet, loaded); answer != nil {
			if err := stream.Write(ctx, *answer); err != nil {
				t.Fatalf("answer %s: %v", packet.Name, err)
			}
			loaded = loaded || sendLoaded
		}
	}

	// Shut down before reporting. A server that disconnected the check for not
	// answering something is a finding, and it shows up here rather than in
	// numbers that look complete.
	if err := stream.Shutdown(ctx, "live play check complete"); err != nil {
		t.Errorf("Shutdown: %v", err)
	}

	reportState(t, sink, v26_1.StatePlay, limits)
	for _, state := range sink.seenStates() {
		if state == v26_1.StatePlay {
			continue
		}
		reportState(t, sink, state, limits)
	}

	play := sink.snapshot(v26_1.StatePlay)
	if play.packets == 0 {
		t.Fatalf("no play packets observed in %s; the measurement did not happen", window)
	}
	// A play window that saw no chunk data measured the wrong thing: chunk
	// data is the largest thing a server sends, and a run without it would
	// report a headroom the defaults have not actually been asked for.
	if play.byPacket["map_chunk"] == 0 {
		t.Errorf("no map_chunk packet observed; %s was not long enough, or the server sent no chunks", window)
	}
}

// requestChunks asks the server for its full view distance.
//
// The negotiator sends no settings packet, and neither does the headless
// client this repository's consumers use; both reach play without one. What a
// settings packet changes is how much world the server streams, which is the
// difference between measuring a corner of spawn and measuring the world.
func requestChunks(ctx context.Context, t *testing.T, stream *protocol.Stream) {
	t.Helper()

	settings := &v26_1.PlayServerboundSettings{
		Locale:              "en_us",
		ViewDistance:        requestedViewDistance,
		ChatFlags:           0,
		ChatColors:          true,
		SkinParts:           0x7f,
		MainHand:            1,
		EnableServerListing: true,
		ParticleStatus:      "all",
	}
	if err := stream.Write(ctx, *playPacket("settings", settings.PacketID(), settings)); err != nil {
		t.Fatalf("write settings: %v", err)
	}
}

// answerPlay returns the one packet this check owes in reply, if any, and
// whether that reply is the once-only player_loaded.
//
// The set is deliberately minimal, and every member of it is here because
// leaving it out stops the traffic this check is measuring:
//   - keep_alive, or the server disconnects mid-window;
//   - teleport_confirm, owed on every server correction rather than only the
//     first, or the server holds the player;
//   - player_loaded, once, which releases the server's loading hold;
//   - chunk_batch_received, or the server sends exactly one batch and then
//     waits forever for an acknowledgement that never comes — which looks
//     like a healthy connection carrying no chunks.
func answerPlay(packet protocol.Packet, loaded bool) (*protocol.Packet, bool) {
	switch value := packet.Value.(type) {
	case *v26_1.PlayClientboundKeepAlive:
		answer := &v26_1.PlayServerboundKeepAlive{KeepAliveID: value.KeepAliveID}

		return playPacket("keep_alive", answer.PacketID(), answer), false
	case *v26_1.PlayClientboundPing:
		answer := &v26_1.PlayServerboundPong{ID: value.ID}

		return playPacket("pong", answer.PacketID(), answer), false
	case *v26_1.PlayClientboundChunkBatchFinished:
		answer := &v26_1.PlayServerboundChunkBatchReceived{ChunksPerTick: chunksPerTick}

		return playPacket("chunk_batch_received", answer.PacketID(), answer), false
	case *v26_1.PlayClientboundPosition:
		if !loaded {
			// One packet per read, so the teleport this position asks for is
			// confirmed on the next one. The server repeats the position
			// until it is.
			answer := &v26_1.PlayServerboundPlayerLoaded{}

			return playPacket("player_loaded", answer.PacketID(), answer), true
		}
		answer := &v26_1.PlayServerboundTeleportConfirm{TeleportID: value.TeleportID}

		return playPacket("teleport_confirm", answer.PacketID(), answer), false
	default:
		return nil, false
	}
}

// playPacket names what it builds. The name is not needed to write a packet —
// the identity is state, direction, and ID — but an observation carries the
// name, and a report of what a connection sent is unreadable without it.
func playPacket(name string, id int32, value any) *protocol.Packet {
	return &protocol.Packet{
		State:     v26_1.StatePlay,
		Direction: protocol.DirectionServerbound,
		ID:        id,
		Name:      name,
		Value:     value,
	}
}

// reportState logs one state's totals. Every number here belongs in the
// milestone record, because the point is the measurement rather than the pass.
func reportState(t *testing.T, sink *sizeSink, state protocol.State, limits protocol.Limits) {
	t.Helper()

	totals := sink.snapshot(state)
	if totals.frames == 0 && totals.packets == 0 {
		return
	}

	t.Logf("--- %s ---", state)
	t.Logf("frames: %d (%d bytes total)", totals.frames, totals.frameBytes)
	t.Logf("packets: %d (%d bytes decoded)", totals.packets, totals.decodedBytes)
	t.Logf("largest raw frame: %d bytes (%s), limit %d, headroom %.0fx",
		totals.largestFrame.bytes, totals.largestFrame.packet,
		limits.FrameBytes(), headroom(limits.FrameBytes(), totals.largestFrame.bytes))
	t.Logf("largest decoded body: %d bytes (%s), limit %d, headroom %.0fx",
		totals.largestDecoded.bytes, totals.largestDecoded.packet,
		limits.DecompressedBytes(), headroom(limits.DecompressedBytes(), totals.largestDecoded.bytes))
	for _, line := range totals.largestPackets(10) {
		t.Logf("  %s", line)
	}
}

func headroom(limit, measured int) float64 {
	if measured == 0 {
		return 0
	}

	return float64(limit) / float64(measured)
}

func playWindow(t *testing.T) time.Duration {
	t.Helper()

	text := os.Getenv(playWindowVariable)
	if text == "" {
		return defaultPlayWindow
	}

	seconds, err := strconv.Atoi(text)
	if err != nil || seconds <= 0 {
		t.Fatalf("%s must be a positive whole number of seconds, got %q", playWindowVariable, text)
	}

	return time.Duration(seconds) * time.Second
}
