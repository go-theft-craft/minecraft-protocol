//go:build livecheck

// Package livecheck drives one real connection against a real Java 26.1
// server.
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
	"sync"
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

// sizeRecord is the largest thing seen at one stage, and what it belonged to.
type sizeRecord struct {
	bytes  int
	packet string
}

// sizeSink records the largest raw frame and the largest decoded body a real
// login produces.
//
// The measurement is the point of the exercise. Every limit in this repository
// was chosen from the specification rather than from traffic, and a real
// server's registry data is the first thing large enough to test that choice.
type sizeSink struct {
	mutex           sync.Mutex
	largestFrame    sizeRecord
	largestDecoded  sizeRecord
	packetsObserved int
}

func (s *sizeSink) Observe(_ context.Context, observation protocol.Observation) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	name := "unnamed"
	if observation.Packet != nil {
		name = string(observation.Packet.State) + "/" + observation.Packet.Name
	}

	switch observation.Stage {
	case protocol.ObservationRawFrame:
		if len(observation.Bytes) > s.largestFrame.bytes {
			s.largestFrame = sizeRecord{bytes: len(observation.Bytes), packet: name}
		}
	case protocol.ObservationPacket:
		s.packetsObserved++
		if len(observation.Bytes) > s.largestDecoded.bytes {
			s.largestDecoded = sizeRecord{bytes: len(observation.Bytes), packet: name}
		}
	case protocol.ObservationSecret:
	}

	return nil
}

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

	host, port := splitAddress(t, address)

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("NewLimits: %v", err)
	}
	session, err := v26_1.Protocol().NewSession(protocol.RoleClient, limits)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	conn, err := net.DialTimeout("tcp", address, liveTimeout)
	if err != nil {
		t.Fatalf("dial %s: %v", address, err)
	}

	sink := &sizeSink{}
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
	defer func() {
		if err := stream.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	}()

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
		Value:     handshake,
	}); err != nil {
		t.Fatalf("write handshake: %v", err)
	}

	authenticator, err := login.NewOffline("mcprotocheck")
	if err != nil {
		t.Fatalf("NewOffline: %v", err)
	}
	negotiator, err := login.NewNegotiator(authenticator)
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}

	profile, err := negotiator.Negotiate(ctx, stream)
	if err != nil {
		t.Fatalf("Negotiate: %v (%s)", err, describe(t, ctx, stream))
	}
	t.Logf("logged in as %s (%s)", profile.Name, profile.UUID)

	snapshot, err := stream.Snapshot(ctx)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.State != v26_1.StatePlay {
		t.Fatalf("state = %q, want play", snapshot.State)
	}

	packet, err := stream.Read(ctx)
	if err != nil {
		t.Fatalf("read the first play packet: %v", err)
	}
	t.Logf("first play packet: %s (ID %#x)", packet.Name, packet.ID)

	if err := stream.Shutdown(ctx, "live check complete"); err != nil {
		t.Errorf("Shutdown: %v", err)
	}

	sink.mutex.Lock()
	defer sink.mutex.Unlock()
	t.Logf("packets observed: %d", sink.packetsObserved)
	t.Logf("largest raw frame: %d bytes (%s)", sink.largestFrame.bytes, sink.largestFrame.packet)
	t.Logf("largest decoded body: %d bytes (%s)", sink.largestDecoded.bytes, sink.largestDecoded.packet)
	t.Logf("frame limit: %d bytes", limits.FrameBytes())
	t.Logf("decompressed limit: %d bytes", limits.DecompressedBytes())
}

// describe reports the state and the last thing the stream knows, so a failed
// transition says where it failed rather than only that it did.
func describe(t *testing.T, ctx context.Context, stream *protocol.Stream) string {
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
