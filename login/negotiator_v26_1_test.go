package login_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"sync"
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
	knownPacks       bool
	stall            bool
	// observedSecret receives the shared secret the server decrypted, so a
	// test can prove the client's own observations never carried it.
	observedSecret func([]byte)
}

func startModernStream(
	t *testing.T,
	conn net.Conn,
	role protocol.Role,
	options ...protocol.StreamOption,
) *protocol.Stream {
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
	}, options...)
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

func modernLoginPair(t *testing.T, clientOptions ...protocol.StreamOption) (*protocol.Stream, *protocol.Stream) {
	t.Helper()

	clientConn, serverConn := net.Pipe()

	return startModernStream(t, clientConn, protocol.RoleClient, clientOptions...),
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

	if script.encrypt && !serveModernEncryption(t, stream, script) {
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

	if script.knownPacks && !serveModernKnownPacks(t, stream) {
		return
	}

	if !writeModernPacket(t, stream, v26_1.StateConfiguration, &v26_1.ConfigurationClientboundFinishConfiguration{}) {
		return
	}
	if _, err := stream.Read(ctx); err != nil {
		return
	}
}

// serveModernKnownPacks plays the pack negotiation the way a real server does
// it: nothing else is sent until the client's answer arrives.
//
// A live 26.1 server stopped here against a negotiator that treated the offer
// as configuration content to pass through, and the connection looked healthy
// while never reaching play. The scripted server withholds the finish
// handshake for the same reason, so the same omission fails here.
func serveModernKnownPacks(t *testing.T, stream *protocol.Stream) bool {
	t.Helper()

	if !writeModernPacket(t, stream, v26_1.StateConfiguration, &v26_1.ConfigurationClientboundSelectKnownPacks{
		Packs: []v26_1.ConfigurationClientboundSelectKnownPacksPacksItem{
			{Namespace: "minecraft", ID: "core", Version: "26.1"},
		},
	}) {
		return false
	}

	packet, err := stream.Read(t.Context())
	if err != nil {
		t.Errorf("read the known-packs answer: %v", err)

		return false
	}
	if _, ok := packet.Value.(*v26_1.ConfigurationServerboundSelectKnownPacks); !ok {
		t.Errorf("answered known packs with %T, want *v26_1.ConfigurationServerboundSelectKnownPacks", packet.Value)

		return false
	}

	return true
}

func serveModernEncryption(t *testing.T, stream *protocol.Stream, script modernScript) bool {
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
	if script.observedSecret != nil {
		script.observedSecret(secret.Reveal())
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
		{name: "known packs", script: modernScript{configurationRun: true, knownPacks: true}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			client, server := modernLoginPair(t)

			go serveModernLogin(t, server, test.script)

			negotiator, err := login.NewNegotiator(offlineTester(t))
			if err != nil {
				t.Fatalf("NewNegotiator: %v", err)
			}
			// Bounded, because the failure this table guards against is a
			// step the negotiator never answers: without a deadline the
			// regression is a hang until the package timeout rather than a
			// named failure on the subtest that caused it.
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()

			profile, err := negotiator.Negotiate(ctx, client)
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

// keySafetySink keeps every observation a stream produced, so a test can check
// what a capture would have held.
type keySafetySink struct {
	mutex   sync.Mutex
	records []protocol.Observation
}

func (s *keySafetySink) Observe(_ context.Context, observation protocol.Observation) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	s.records = append(s.records, observation)

	return nil
}

func (s *keySafetySink) all() []protocol.Observation {
	s.mutex.Lock()
	defer s.mutex.Unlock()

	return append([]protocol.Observation(nil), s.records...)
}

// TestObservationsWithholdTheKeyExchangeOnProtocol775 checks the property the
// redaction machinery exists for, against a real key rather than a marker.
//
// The raw record is written before its frame is decoded, so redaction there
// cannot come from the decoded packet. A stream that got this wrong would
// still pass every packet-level redaction test while writing the shared secret
// to disk in the frame beside it.
func TestObservationsWithholdTheKeyExchangeOnProtocol775(t *testing.T) {
	var (
		secretMutex sync.Mutex
		secret      []byte
	)

	sink := &keySafetySink{}
	client, server := modernLoginPair(t, protocol.WithObservationSink(sink))

	go serveModernLogin(t, server, modernScript{
		encrypt: true,
		observedSecret: func(material []byte) {
			secretMutex.Lock()
			defer secretMutex.Unlock()

			secret = append([]byte(nil), material...)
		},
	})

	negotiator, err := login.NewNegotiator(offlineTester(t))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	if _, err := negotiator.Negotiate(ctx, client); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}

	secretMutex.Lock()
	material := secret
	secretMutex.Unlock()

	if len(material) == 0 {
		t.Fatal("the server never decrypted a shared secret, so this proves nothing")
	}

	var redacted int
	for _, record := range sink.all() {
		if bytes.Contains(record.Bytes, material) {
			t.Fatalf(
				"the session key reached the sink in a %q record at sequence %d",
				record.Stage,
				record.Sequence,
			)
		}
		if record.Redacted {
			redacted++

			if len(record.Bytes) != 0 {
				t.Errorf("record %d is marked redacted but carries %d bytes", record.Sequence, len(record.Bytes))
			}
			// The secret record is the exception, and deliberately: its
			// material is never read unless disclosure was asked for, so
			// there is no length to report without materializing the key.
			if record.Stage != protocol.ObservationSecret && record.OriginalLen == 0 {
				t.Errorf("redacted record %d does not report the size it withheld", record.Sequence)
			}
		}
	}

	// Both halves of the exchange, each as a raw frame and as a packet, plus
	// the secret record the control produces: the request inbound, the
	// response outbound, twice over, and the switch itself.
	if redacted < 5 {
		t.Fatalf("only %d records were redacted, want the key exchange withheld in both stages", redacted)
	}
}
