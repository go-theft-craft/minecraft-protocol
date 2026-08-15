package login_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
	"github.com/go-theft-craft/minecraft-protocol/login"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

// rejectingAuthenticator fails Join, which is what an unowned account or an
// unreachable session server looks like to the negotiator.
type rejectingAuthenticator struct{ err error }

func (rejectingAuthenticator) Profile() login.Profile {
	name, _ := java.ParseUsername("tester")

	return login.Profile{Name: name}
}

func (a rejectingAuthenticator) Join(context.Context, java.ServerHash) error { return a.err }

// serverScript describes what the scripted server does. A zero script writes
// a plain, unencrypted, uncompressed success.
type serverScript struct {
	encrypt    bool
	compress   bool
	disconnect string
	stall      bool

	// Malformed-field overrides. Each is empty or false in a healthy script.
	serverID         string
	emptyPublicKey   bool
	emptyVerifyToken bool
	garbagePublicKey bool
	successUUID      string
	successUsername  string
	emptyUsername    bool
}

// loginPair builds a started client stream and a started server stream over an
// in-memory connection, both already in the login state.
func loginPair(t *testing.T) (*protocol.Stream, *protocol.Stream) {
	t.Helper()

	clientConn, serverConn := net.Pipe()
	client := startLoginStream(t, clientConn, protocol.RoleClient)
	server := startLoginStream(t, serverConn, protocol.RoleServer)

	return client, server
}

func startLoginStream(t *testing.T, conn net.Conn, role protocol.Role) *protocol.Stream {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("limits: %v", err)
	}
	session, err := v1_8.Protocol().NewSession(role, limits)
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
	// The handshake is not part of this test. Put both sides straight into
	// the login state, which is where the negotiator expects to begin.
	if err := stream.SetState(t.Context(), v1_8.StateLogin); err != nil {
		t.Fatalf("set state: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	return stream
}

// serveLogin plays the server half. It reports failures through t rather than
// returning them, because it runs in its own goroutine.
func serveLogin(t *testing.T, stream *protocol.Stream, script serverScript) {
	t.Helper()

	ctx := t.Context()

	if _, err := stream.Read(ctx); err != nil {
		return // The client gave up first, which several cases expect.
	}
	if script.stall {
		<-ctx.Done()

		return
	}
	if script.disconnect != "" {
		writeLoginPacket(t, stream, &v1_8.LoginClientboundDisconnect{Reason: script.disconnect})

		return
	}

	if script.encrypt {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			t.Errorf("generate key: %v", err)

			return
		}
		encoded, err := java.EncodeServerPublicKey(&key.PublicKey)
		if err != nil {
			t.Errorf("encode key: %v", err)

			return
		}

		switch {
		case script.emptyPublicKey:
			encoded = nil
		case script.garbagePublicKey:
			encoded = []byte("not a key")
		}

		token := []byte{0x01, 0x02, 0x03, 0x04}
		if script.emptyVerifyToken {
			token = nil
		}

		if !writeLoginPacket(t, stream, &v1_8.LoginClientboundEncryptionBegin{
			ServerID:    script.serverID,
			PublicKey:   encoded,
			VerifyToken: token,
		}) {
			return
		}

		packet, err := stream.Read(ctx)
		if err != nil {
			return // The client rejected the request, which is the point.
		}
		response, ok := packet.Value.(*v1_8.LoginServerboundEncryptionBegin)
		if !ok {
			t.Errorf("received %T, want *v1_8.LoginServerboundEncryptionBegin", packet.Value)

			return
		}

		if err := java.VerifyToken(key, token, response.VerifyToken); err != nil {
			t.Errorf("verify token: %v", err)

			return
		}

		secret, err := java.DecryptSharedSecret(key, response.SharedSecret)
		if err != nil {
			t.Errorf("adopt session key: %v", err)

			return
		}
		if err := stream.Control(ctx, java.EncryptionControl{Secret: secret}); err != nil {
			t.Errorf("enable server encryption: %v", err)

			return
		}
	}

	if script.compress {
		if !writeLoginPacket(t, stream, &v1_8.LoginClientboundCompress{Threshold: 256}) {
			return
		}
	}

	identity := script.successUUID
	if identity == "" {
		identity = "069a79f4-44e9-4726-a5be-fca90e38aaf5"
	}
	username := script.successUsername
	if username == "" && !script.emptyUsername {
		username = "tester"
	}

	writeLoginPacket(t, stream, &v1_8.LoginClientboundSuccess{
		UUID:     identity,
		Username: username,
	})
}

// writeLoginPacket sends one clientbound login packet and reports whether the
// script should continue.
//
// A write failure is not reported as a test failure. Every malformed-field
// case ends with the client abandoning the login, and the scripted server is
// mid-write or about to write when that happens, so a failed write there is
// the expected outcome rather than a defect. What the server sent wrong is
// asserted on the client side, which is the side under test.
func writeLoginPacket(t *testing.T, stream *protocol.Stream, value any) bool {
	t.Helper()

	identified, ok := value.(interface{ PacketID() int32 })
	if !ok {
		t.Errorf("%T has no PacketID", value)

		return false
	}

	err := stream.Write(t.Context(), protocol.Packet{
		State:     v1_8.StateLogin,
		Direction: protocol.DirectionClientbound,
		ID:        identified.PacketID(),
		Value:     value,
	})

	return err == nil
}

// offlineTester is the authenticator every healthy case in this file uses.
func offlineTester(t *testing.T) login.Offline {
	t.Helper()

	authenticator, err := login.NewOffline("tester")
	if err != nil {
		t.Fatalf("NewOffline: %v", err)
	}

	return authenticator
}

func TestNegotiateCompletesAnEncryptedLogin(t *testing.T) {
	client, server := loginPair(t)

	go serveLogin(t, server, serverScript{encrypt: true, compress: true})

	negotiator, err := login.NewNegotiator(offlineTester(t))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}

	profile, err := negotiator.Negotiate(t.Context(), client)
	if err != nil {
		t.Fatalf("Negotiate: %v", err)
	}
	if profile.Name.String() != "tester" {
		t.Fatalf("profile name = %q, want %q", profile.Name, "tester")
	}
	if profile.UUID.IsZero() {
		t.Fatal("a successful login must carry a UUID")
	}

	snapshot, err := client.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.State != v1_8.StatePlay {
		t.Fatalf("state = %q, want %q", snapshot.State, v1_8.StatePlay)
	}
	if snapshot.Pipeline["encryption.enabled"] != "true" {
		t.Fatal("encryption must be enabled after a successful login")
	}
	if snapshot.Pipeline["compression.enabled"] != "true" {
		t.Fatal("the compression transition must still be applied by the session")
	}
}

func TestNegotiateReportsAuthenticatorRejection(t *testing.T) {
	client, server := loginPair(t)
	go serveLogin(t, server, serverScript{encrypt: true})

	refused := errors.New("account does not own the game")
	negotiator, err := login.NewNegotiator(rejectingAuthenticator{err: refused})
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}

	_, err = negotiator.Negotiate(t.Context(), client)
	if !errors.Is(err, login.ErrAuthenticationRejected) {
		t.Fatalf("error = %v, want ErrAuthenticationRejected", err)
	}
	if !errors.Is(err, refused) {
		t.Fatalf("error must wrap the authenticator cause, got %v", err)
	}
}

func TestNegotiateReportsALoginDisconnect(t *testing.T) {
	client, server := loginPair(t)
	go serveLogin(t, server, serverScript{disconnect: "banned"})

	negotiator, err := login.NewNegotiator(offlineTester(t))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}

	_, err = negotiator.Negotiate(t.Context(), client)
	if !errors.Is(err, login.ErrLoginDisconnected) {
		t.Fatalf("error = %v, want ErrLoginDisconnected", err)
	}
}

func TestNegotiateHonoursCancellation(t *testing.T) {
	client, server := loginPair(t)
	go serveLogin(t, server, serverScript{stall: true})

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	negotiator, err := login.NewNegotiator(offlineTester(t))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}

	if _, err := negotiator.Negotiate(ctx, client); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestNewOfflineRejectsAnInvalidName(t *testing.T) {
	if _, err := login.NewOffline(""); !errors.Is(err, java.ErrInvalidUsername) {
		t.Fatalf("error = %v, want ErrInvalidUsername", err)
	}
	if _, err := login.NewOffline(strings.Repeat("n", 17)); !errors.Is(err, java.ErrInvalidUsername) {
		t.Fatalf("error = %v, want ErrInvalidUsername", err)
	}
}

// A hostile or broken server is the reason every inbound login field is
// validated. Each case must fail before the negotiator acts on the field.
func TestNegotiateRejectsMalformedServerFields(t *testing.T) {
	cases := []struct {
		name   string
		script serverScript
		want   error
	}{
		{
			name:   "oversized server ID",
			script: serverScript{encrypt: true, serverID: strings.Repeat("x", 21)},
			want:   login.ErrInvalidLoginField,
		},
		{
			name:   "empty public key",
			script: serverScript{encrypt: true, emptyPublicKey: true},
			want:   login.ErrInvalidLoginField,
		},
		{
			name:   "empty verify token",
			script: serverScript{encrypt: true, emptyVerifyToken: true},
			want:   login.ErrInvalidLoginField,
		},
		{
			name:   "unparseable public key",
			script: serverScript{encrypt: true, garbagePublicKey: true},
			want:   java.ErrInvalidServerKey,
		},
		{
			name:   "malformed success UUID",
			script: serverScript{successUUID: "not-a-uuid"},
			want:   java.ErrInvalidUUID,
		},
		{
			name:   "oversized success username",
			script: serverScript{successUsername: strings.Repeat("n", 17)},
			want:   java.ErrInvalidUsername,
		},
		{
			name:   "empty success username",
			script: serverScript{emptyUsername: true},
			want:   java.ErrInvalidUsername,
		},
		{
			name:   "success username with a control character",
			script: serverScript{successUsername: "bad\nname"},
			want:   java.ErrInvalidUsername,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client, server := loginPair(t)
			go serveLogin(t, server, testCase.script)

			negotiator, err := login.NewNegotiator(offlineTester(t))
			if err != nil {
				t.Fatalf("NewNegotiator: %v", err)
			}

			if _, err := negotiator.Negotiate(t.Context(), client); !errors.Is(err, testCase.want) {
				t.Fatalf("error = %v, want %v", err, testCase.want)
			}
		})
	}
}
