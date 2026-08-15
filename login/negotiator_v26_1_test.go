package login_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"testing"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v26_1 "github.com/go-theft-craft/minecraft-protocol/generated/java/v26_1"
	"github.com/go-theft-craft/minecraft-protocol/login"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

// modernScript describes what the scripted protocol 775 server does. A zero
// script writes a plain success and finishes configuration.
type modernScript struct {
	encrypt          bool
	compress         bool
	disconnect       string
	configurationRun bool
	stall            bool
}

func startModernStream(t *testing.T, conn net.Conn, role protocol.Role) *protocol.Stream {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("limits: %v", err)
	}
	session, err := v26_1.Protocol().NewSession(role, limits)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	stream, err := protocol.NewStream(session, protocol.Transport{
		Reader:    conn,
		Writer:    conn,
		Interrupt: conn.Close,
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if err := stream.Start(t.Context()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := stream.SetState(t.Context(), v26_1.StateLogin); err != nil {
		t.Fatalf("set state: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	return stream
}

func modernLoginPair(t *testing.T) (*protocol.Stream, *protocol.Stream) {
	t.Helper()

	clientConn, serverConn := net.Pipe()

	return startModernStream(t, clientConn, protocol.RoleClient),
		startModernStream(t, serverConn, protocol.RoleServer)
}

func writeModernPacket(t *testing.T, stream *protocol.Stream, state protocol.State, value any) bool {
	t.Helper()

	identified, ok := value.(interface{ PacketID() int32 })
	if !ok {
		t.Errorf("%T has no PacketID", value)

		return false
	}

	err := stream.Write(t.Context(), protocol.Packet{
		State:     state,
		Direction: protocol.DirectionClientbound,
		ID:        identified.PacketID(),
		Value:     value,
	})

	return err == nil
}

// serveModernLogin plays the server half of a protocol 775 login: login,
// acknowledgement, configuration, finish handshake, play.
func serveModernLogin(t *testing.T, stream *protocol.Stream, script modernScript) {
	t.Helper()

	ctx := t.Context()

	if _, err := stream.Read(ctx); err != nil {
		return // The client gave up first.
	}
	if script.stall {
		<-ctx.Done()

		return
	}
	if script.disconnect != "" {
		writeModernPacket(t, stream, v26_1.StateLogin, &v26_1.LoginClientboundDisconnect{Reason: script.disconnect})

		return
	}

	if script.encrypt && !serveModernEncryption(t, stream) {
		return
	}

	if script.compress {
		if !writeModernPacket(t, stream, v26_1.StateLogin, &v26_1.LoginClientboundCompress{Threshold: 256}) {
			return
		}
	}

	identity, err := java.ParseUUID("069a79f4-44e9-4726-a5be-fca90e38aaf5")
	if err != nil {
		t.Errorf("ParseUUID: %v", err)

		return
	}
	if !writeModernPacket(t, stream, v26_1.StateLogin, &v26_1.LoginClientboundSuccess{
		UUID:     identity,
		Username: "tester",
	}) {
		return
	}

	// The acknowledgement is what moves both sides to configuration.
	if _, err := stream.Read(ctx); err != nil {
		return
	}

	if script.configurationRun {
		// One packet a server really sends in configuration, to prove the
		// negotiator passes through what it does not need.
		if !writeModernPacket(t, stream, v26_1.StateConfiguration, &v26_1.ConfigurationClientboundFeatureFlags{
			Features: []string{"minecraft:vanilla"},
		}) {
			return
		}
	}

	if !writeModernPacket(t, stream, v26_1.StateConfiguration, &v26_1.ConfigurationClientboundFinishConfiguration{}) {
		return
	}
	if _, err := stream.Read(ctx); err != nil {
		return
	}
}

func serveModernEncryption(t *testing.T, stream *protocol.Stream) bool {
	t.Helper()

	ctx := t.Context()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Errorf("generate key: %v", err)

		return false
	}
	encoded, err := java.EncodeServerPublicKey(&key.PublicKey)
	if err != nil {
		t.Errorf("encode key: %v", err)

		return false
	}

	token := []byte{0x01, 0x02, 0x03, 0x04}
	if !writeModernPacket(t, stream, v26_1.StateLogin, &v26_1.LoginClientboundEncryptionBegin{
		PublicKey:          encoded,
		VerifyToken:        token,
		ShouldAuthenticate: true,
	}) {
		return false
	}

	packet, err := stream.Read(ctx)
	if err != nil {
		return false
	}
	response, ok := packet.Value.(*v26_1.LoginServerboundEncryptionBegin)
	if !ok {
		t.Errorf("received %T, want *v26_1.LoginServerboundEncryptionBegin", packet.Value)

		return false
	}
	if err := java.VerifyToken(key, token, response.VerifyToken); err != nil {
		t.Errorf("verify token: %v", err)

		return false
	}
	secret, err := java.DecryptSharedSecret(key, response.SharedSecret)
	if err != nil {
		t.Errorf("adopt session key: %v", err)

		return false
	}
	if err := stream.Control(ctx, java.EncryptionControl{Secret: secret}); err != nil {
		t.Errorf("enable server encryption: %v", err)

		return false
	}

	return true
}

// TestNegotiateReachesPlayOnProtocol775 is the whole point of the role-driven
// negotiator: the same code that drives protocol 47's two-packet login drives
// a login that passes through configuration.
func TestNegotiateReachesPlayOnProtocol775(t *testing.T) {
	tests := []struct {
		name   string
		script modernScript
	}{
		{name: "offline", script: modernScript{configurationRun: true}},
		{name: "encrypted", script: modernScript{encrypt: true, configurationRun: true}},
		{name: "compressed mid-login", script: modernScript{compress: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, server := modernLoginPair(t)

			go serveModernLogin(t, server, test.script)

			negotiator, err := login.NewNegotiator(offlineTester(t))
			if err != nil {
				t.Fatalf("NewNegotiator: %v", err)
			}
			profile, err := negotiator.Negotiate(t.Context(), client)
			if err != nil {
				t.Fatalf("Negotiate: %v", err)
			}
			if profile.Name.String() != "tester" {
				t.Errorf("profile name = %q, want tester", profile.Name)
			}
			if profile.UUID.String() != "069a79f4-44e9-4726-a5be-fca90e38aaf5" {
				t.Errorf("profile UUID = %q, want the one the server confirmed", profile.UUID)
			}

			snapshot, err := client.Snapshot(t.Context())
			if err != nil {
				t.Fatalf("Snapshot: %v", err)
			}
			if snapshot.State != v26_1.StatePlay {
				t.Errorf("state = %q, want play", snapshot.State)
			}
		})
	}
}

// TestNegotiateStopsAtConfiguration covers the option that exists because this
// negotiator does not read what a server sends in configuration.
func TestNegotiateStopsAtConfiguration(t *testing.T) {
	client, server := modernLoginPair(t)

	go serveModernLogin(t, server, modernScript{configurationRun: true})

	negotiator, err := login.NewNegotiator(offlineTester(t), login.WithTerminalState("configuration"))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}
	if _, err := negotiator.Negotiate(t.Context(), client); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}

	snapshot, err := client.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if snapshot.State != v26_1.StateConfiguration {
		t.Errorf("state = %q, want configuration", snapshot.State)
	}
}

// TestNegotiateReportsAModernDisconnect checks the failure path a modern login
// shares with protocol 47.
func TestNegotiateReportsAModernDisconnect(t *testing.T) {
	client, server := modernLoginPair(t)

	go serveModernLogin(t, server, modernScript{disconnect: "banned"})

	negotiator, err := login.NewNegotiator(offlineTester(t))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}
	if _, err := negotiator.Negotiate(t.Context(), client); !errors.Is(err, login.ErrLoginDisconnected) {
		t.Fatalf("Negotiate error = %v, want a login disconnect", err)
	}
}

// TestNegotiateFailsWhenAuthenticationIsRejected replays protocol 47's
// rejection path on 775, where the server states that it wants the join.
func TestNegotiateFailsWhenAuthenticationIsRejected(t *testing.T) {
	client, server := modernLoginPair(t)

	go serveModernLogin(t, server, modernScript{encrypt: true})

	negotiator, err := login.NewNegotiator(rejectingAuthenticator{err: errors.New("no")})
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}
	if _, err := negotiator.Negotiate(t.Context(), client); !errors.Is(err, login.ErrAuthenticationRejected) {
		t.Fatalf("Negotiate error = %v, want a rejected authentication", err)
	}
}

// TestNegotiateHonoursCancellationOnProtocol775 replays the timeout path.
func TestNegotiateHonoursCancellationOnProtocol775(t *testing.T) {
	client, server := modernLoginPair(t)

	go serveModernLogin(t, server, modernScript{stall: true})

	negotiator, err := login.NewNegotiator(offlineTester(t))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	if _, err := negotiator.Negotiate(ctx, client); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}
