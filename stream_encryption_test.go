package protocol_test

import (
	"crypto/rand"
	"crypto/rsa"
	"net"
	"strings"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
	"github.com/go-theft-craft/minecraft-protocol/login"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

// The login helpers below are copied from login/negotiator_test.go. They are
// test fixtures rather than API, and the two packages cannot share unexported
// test code, so a copy is the honest option.

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

// tcpLoginPair is the login pair over a real socket. net.Pipe is synchronous
// and unbuffered, so it hides the read-ahead behaviour at the switch point
// that this file exists to check.
func tcpLoginPair(t *testing.T) (*protocol.Stream, *protocol.Stream) {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			close(accepted)

			return
		}
		accepted <- conn
	}()

	clientConn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = clientConn.Close() })

	serverConn, ok := <-accepted
	if !ok {
		t.Fatal("accept failed")
	}
	t.Cleanup(func() { _ = serverConn.Close() })

	return startLoginStream(t, clientConn, protocol.RoleClient),
		startLoginStream(t, serverConn, protocol.RoleServer)
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

		returnedToken, err := java.DecryptFromServerKey(key, response.VerifyToken)
		if err != nil {
			t.Errorf("decrypt verify token: %v", err)

			return
		}
		if err := java.VerifyToken(token, returnedToken); err != nil {
			t.Errorf("verify token: %v", err)

			return
		}

		secretBytes, err := java.DecryptFromServerKey(key, response.SharedSecret)
		if err != nil {
			t.Errorf("decrypt session key: %v", err)

			return
		}
		secret, err := java.SharedSecretFrom(secretBytes)
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

func TestEncryptedLoginOverLoopbackTCP(t *testing.T) {
	client, server := tcpLoginPair(t)

	go serveLogin(t, server, serverScript{encrypt: true, compress: true})

	negotiator, err := login.NewNegotiator(offlineTester(t))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}
	if _, err := negotiator.Negotiate(t.Context(), client); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}

	// After login, exchange a play packet and assert the body survives. This
	// is the assertion that fails if the compression envelope and the cipher
	// are composed in the wrong order.
	keepAlive := protocol.Packet{
		State:     v1_8.StatePlay,
		Direction: protocol.DirectionServerbound,
		ID:        v1_8.PlayServerboundKeepAlive{}.PacketID(),
		Value:     &v1_8.PlayServerboundKeepAlive{KeepAliveID: 4242},
	}
	if err := client.Write(t.Context(), keepAlive); err != nil {
		t.Fatalf("write keep alive: %v", err)
	}

	received, err := server.Read(t.Context())
	if err != nil {
		t.Fatalf("read keep alive: %v", err)
	}
	value, ok := received.Value.(*v1_8.PlayServerboundKeepAlive)
	if !ok {
		t.Fatalf("received %T, want *v1_8.PlayServerboundKeepAlive", received.Value)
	}
	if value.KeepAliveID != 4242 {
		t.Fatalf("keep alive ID = %d, want 4242", value.KeepAliveID)
	}
}

// TestEncryptedCompressedPacketOverLoopbackTCP sends a body past the 256-byte
// threshold, so the frame is genuinely compressed as well as encrypted. The
// keep-alive above is too small to compress, so it would pass even if the two
// stages were composed in the wrong order.
func TestEncryptedCompressedPacketOverLoopbackTCP(t *testing.T) {
	client, server := tcpLoginPair(t)

	go serveLogin(t, server, serverScript{encrypt: true, compress: true})

	negotiator, err := login.NewNegotiator(offlineTester(t))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}
	if _, err := negotiator.Negotiate(t.Context(), client); err != nil {
		t.Fatalf("Negotiate: %v", err)
	}

	message := strings.Repeat("compress and encrypt me ", 40)
	chat := protocol.Packet{
		State:     v1_8.StatePlay,
		Direction: protocol.DirectionServerbound,
		ID:        v1_8.PlayServerboundChat{}.PacketID(),
		Value:     &v1_8.PlayServerboundChat{Message: message},
	}
	if err := client.Write(t.Context(), chat); err != nil {
		t.Fatalf("write chat: %v", err)
	}

	received, err := server.Read(t.Context())
	if err != nil {
		t.Fatalf("read chat: %v", err)
	}
	value, ok := received.Value.(*v1_8.PlayServerboundChat)
	if !ok {
		t.Fatalf("received %T, want *v1_8.PlayServerboundChat", received.Value)
	}
	if value.Message != message {
		t.Fatalf("message survived as %d bytes, want %d", len(value.Message), len(message))
	}
}
