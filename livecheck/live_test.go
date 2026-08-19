//go:build livecheck

// Package livecheck drives real connections against a real Java 26.1 server.
//
// It is behind a build tag and an environment variable because it is the one
// check here that needs something this repository cannot provide: a running
// server. Everything else — the differential suite, the loopback
// interoperability lane, the generated codec tests — compares this code
// against a specification or against another implementation of one. This
// compares it against the thing the specification describes.
package livecheck

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v26_1 "github.com/go-theft-craft/minecraft-protocol/generated/java/v26_1"
	"github.com/go-theft-craft/minecraft-protocol/login"
)

// liveAddressVariable names the server to connect to, as host:port. The check
// skips rather than fails when it is unset, so an accidental inclusion in a
// broader run cannot break a build that has no server.
const liveAddressVariable = "MCPROTO_LIVE_ADDR"

const liveTimeout = 30 * time.Second

// TestLiveLoginReachesPlay connects, logs in offline, reaches play, reads one
// clientbound play packet, and disconnects.
//
// It runs with the default limits on purpose. A frame or payload the defaults
// reject is a result — it says the defaults are wrong for a real server — and
// raising a ceiling in advance would be pre-empting the measurement this check
// exists to make.
func TestLiveLoginReachesPlay(t *testing.T) {
	address := os.Getenv(liveAddressVariable)
	if address == "" {
		t.Skipf("%s is unset; see livecheck/README.md", liveAddressVariable)
	}

	ctx, cancel := context.WithTimeout(context.Background(), liveTimeout)
	defer cancel()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("NewLimits: %v", err)
	}

	sink := newSizeSink()
	stream, closeStream := dialAndReachPlay(ctx, t, address, limits, sink)
	defer closeStream()

	packet, err := stream.Read(ctx)
	if err != nil {
		t.Fatalf("read the first play packet: %v", err)
	}
	t.Logf("first play packet: %s (ID %#x)", packet.Name, packet.ID)

	if err := stream.Shutdown(ctx, "live check complete"); err != nil {
		t.Errorf("Shutdown: %v", err)
	}

	for _, state := range sink.seenStates() {
		reportState(t, sink, state, limits)
	}
}

// dialAndReachPlay connects, logs in offline, and returns a stream sitting in
// play, with the function that closes it.
//
// Both checks here need exactly this much of a connection, and the login check
// is what proves it works: nothing about the measurement is allowed to change
// how the connection was made.
func dialAndReachPlay(
	ctx context.Context,
	t *testing.T,
	address string,
	limits protocol.Limits,
	sink protocol.ObservationSink,
) (*protocol.Stream, func()) {
	t.Helper()

	host, port := splitAddress(t, address)

	session, err := v26_1.Protocol().NewSession(protocol.RoleClient, limits)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	conn, err := net.DialTimeout("tcp", address, liveTimeout)
	if err != nil {
		t.Fatalf("dial %s: %v", address, err)
	}

	stream, err := protocol.NewStream(session, protocol.Transport{
		Reader:    conn,
		Writer:    conn,
		Interrupt: conn.Close,
	}, protocol.WithObservationSink(sink))
	if err != nil {
		t.Fatalf("NewStream: %v", err)
	}
	if err := stream.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	closeStream := func() {
		if err := stream.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}

	handshake := &v26_1.HandshakingServerboundSetProtocol{
		ProtocolVersion: 775,
		ServerHost:      host,
		ServerPort:      port,
		NextState:       2,
	}
	if err := stream.Write(ctx, protocol.Packet{
		State:     v26_1.StateHandshaking,
		Direction: protocol.DirectionServerbound,
		ID:        handshake.PacketID(),
		Name:      "set_protocol",
		Value:     handshake,
	}); err != nil {
		closeStream()
		t.Fatalf("write handshake: %v", err)
	}

	authenticator, err := login.NewOffline("mcprotocheck")
	if err != nil {
		closeStream()
		t.Fatalf("NewOffline: %v", err)
	}
	negotiator, err := login.NewNegotiator(authenticator)
	if err != nil {
		closeStream()
		t.Fatalf("NewNegotiator: %v", err)
	}

	profile, err := negotiator.Negotiate(ctx, stream)
	if err != nil {
		defer closeStream()
		t.Fatalf("Negotiate: %v (%s)", err, describe(ctx, t, stream))
	}
	t.Logf("logged in as %s (%s)", profile.Name, profile.UUID)

	snapshot, err := stream.Snapshot(ctx)
	if err != nil {
		defer closeStream()
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.State != v26_1.StatePlay {
		defer closeStream()
		t.Fatalf("state = %q, want play", snapshot.State)
	}

	return stream, closeStream
}

// describe reports the state and the last thing the stream knows, so a failed
// transition says where it failed rather than only that it did.
func describe(ctx context.Context, t *testing.T, stream *protocol.Stream) string {
	t.Helper()

	snapshot, err := stream.Snapshot(ctx)
	if err != nil {
		return fmt.Sprintf("state unknown: %v", err)
	}

	return fmt.Sprintf("state %q", snapshot.State)
}

func splitAddress(t *testing.T, address string) (string, uint16) {
	t.Helper()

	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		t.Fatalf("%s must be host:port: %v", liveAddressVariable, err)
	}
	port, err := net.LookupPort("tcp", portText)
	if err != nil {
		t.Fatalf("%s port: %v", liveAddressVariable, err)
	}

	return host, uint16(port)
}
