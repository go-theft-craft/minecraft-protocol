package login_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
	"github.com/go-theft-craft/minecraft-protocol/login"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

// acceptorKey generates the server key pair for one test. No test loads a key
// from disk, so no fixture in this repository holds private material.
func acceptorKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}

	return key
}

// recordingVerifier is the session-server stand-in. It records what the
// acceptor asked it, which is how the tests assert that the Verifier is the
// only outbound edge the acceptor has.
type recordingVerifier struct {
	profile login.Profile
	err     error

	calls    int
	username java.Username
	hash     java.ServerHash
}

func (v *recordingVerifier) Verify(
	_ context.Context,
	username java.Username,
	hash java.ServerHash,
) (login.Profile, error) {
	v.calls++
	v.username = username
	v.hash = hash

	if v.err != nil {
		return login.Profile{}, v.err
	}

	profile := v.profile
	if profile.Name.IsZero() {
		profile.Name = username
	}

	return profile, nil
}

func onlineVerifier(t *testing.T) *recordingVerifier {
	t.Helper()

	identity, err := java.ParseUUID("069a79f4-44e9-4726-a5be-fca90e38aaf5")
	if err != nil {
		t.Fatalf("ParseUUID: %v", err)
	}

	return &recordingVerifier{profile: login.Profile{UUID: identity}}
}

// negotiateResult is what the client half reported, carried back from the
// goroutine it ran in.
type negotiateResult struct {
	profile login.Profile
	err     error
}

// negotiate runs the real client negotiator against the acceptor. The two
// halves driving each other is the point of this file: it is the test that
// could not exist if they lived in different repositories.
func negotiate(t *testing.T, stream *protocol.Stream) <-chan negotiateResult {
	t.Helper()

	results := make(chan negotiateResult, 1)
	negotiator, err := login.NewNegotiator(offlineTester(t))
	if err != nil {
		t.Fatalf("NewNegotiator: %v", err)
	}

	go func() {
		profile, err := negotiator.Negotiate(t.Context(), stream)
		results <- negotiateResult{profile: profile, err: err}
	}()

	return results
}

func awaitNegotiation(t *testing.T, results <-chan negotiateResult) negotiateResult {
	t.Helper()

	select {
	case result := <-results:
		return result
	case <-time.After(10 * time.Second):
		t.Fatal("the client negotiator never returned")

		return negotiateResult{}
	}
}

func newAcceptor(t *testing.T, key *rsa.PrivateKey, options ...login.AcceptorOption) *login.Acceptor {
	t.Helper()

	acceptor, err := login.NewAcceptor(key, options...)
	if err != nil {
		t.Fatalf("NewAcceptor: %v", err)
	}

	return acceptor
}

func TestAcceptCompletesAnOfflineLogin(t *testing.T) {
	client, server := loginPair(t)
	results := negotiate(t, client)

	profile, err := newAcceptor(t, acceptorKey(t)).Accept(t.Context(), server)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if profile.Name.String() != "tester" {
		t.Fatalf("profile name = %q, want %q", profile.Name, "tester")
	}
	if profile.UUID.IsZero() {
		t.Fatal("an offline login must still carry a UUID")
	}

	// The offline UUID is the vanilla derivation, so a player keeps the same
	// identity across a migration that changes nothing else.
	if profile.UUID != login.OfflineUUID(profile.Name) {
		t.Fatalf("UUID = %s, want the offline derivation %s", profile.UUID, login.OfflineUUID(profile.Name))
	}
	// Version 3, RFC 4122 variant.
	if got := profile.UUID[6] & 0xf0; got != 0x30 {
		t.Fatalf("UUID version nibble = %#x, want 0x30", got)
	}
	if got := profile.UUID[8] & 0xc0; got != 0x80 {
		t.Fatalf("UUID variant bits = %#x, want 0x80", got)
	}

	result := awaitNegotiation(t, results)
	if result.err != nil {
		t.Fatalf("Negotiate: %v", result.err)
	}
	if result.profile.UUID != profile.UUID {
		t.Fatalf("client saw UUID %s, server sent %s", result.profile.UUID, profile.UUID)
	}

	assertState(t, server, v1_8.StatePlay)
	assertState(t, client, v1_8.StatePlay)
	assertPipeline(t, server, "encryption.enabled", "false")
	assertPipeline(t, server, "compression.enabled", "false")
}

func TestAcceptCompletesAnOnlineLogin(t *testing.T) {
	client, server := loginPair(t)
	verifier := onlineVerifier(t)
	results := negotiate(t, client)

	acceptor := newAcceptor(t, acceptorKey(t),
		login.WithVerifier(verifier),
		login.WithServerID("gtc"),
	)

	profile, err := acceptor.Accept(t.Context(), server)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if profile.UUID != verifier.profile.UUID {
		t.Fatalf("profile UUID = %s, want the verifier's %s", profile.UUID, verifier.profile.UUID)
	}

	if verifier.calls != 1 {
		t.Fatalf("verifier called %d times, want 1", verifier.calls)
	}
	if verifier.username.String() != "tester" {
		t.Fatalf("verifier saw username %q, want %q", verifier.username, "tester")
	}
	if verifier.hash.IsZero() {
		t.Fatal("the verifier must receive the hash the client computed")
	}

	result := awaitNegotiation(t, results)
	if result.err != nil {
		t.Fatalf("Negotiate: %v", result.err)
	}

	// The cipher has to install on both sides at the same frame boundary, or
	// the next frame is unreadable on one of them.
	assertPipeline(t, server, "encryption.enabled", "true")
	assertPipeline(t, client, "encryption.enabled", "true")
	assertState(t, server, v1_8.StatePlay)
	assertState(t, client, v1_8.StatePlay)

	// An encrypted stream still carries traffic afterwards, which is the only
	// proof the two ciphers agree.
	exchangeChat(t, server, client, "encrypted")
}

// The hash covers the server ID, so two logins under different IDs must
// produce different hashes. This is what a wrong or ignored server ID would
// break, and it cannot be asserted against a literal because the session key
// is fresh per connection.
func TestAcceptHashesTheServerID(t *testing.T) {
	hashes := make([]java.ServerHash, 0, 2)

	for _, serverID := range []string{"first", "second"} {
		client, server := loginPair(t)
		verifier := onlineVerifier(t)
		results := negotiate(t, client)

		acceptor := newAcceptor(t, acceptorKey(t),
			login.WithVerifier(verifier),
			login.WithServerID(serverID),
		)
		if _, err := acceptor.Accept(t.Context(), server); err != nil {
			t.Fatalf("Accept(%q): %v", serverID, err)
		}
		if result := awaitNegotiation(t, results); result.err != nil {
			t.Fatalf("Negotiate(%q): %v", serverID, result.err)
		}

		hashes = append(hashes, verifier.hash)
	}

	if hashes[0] == hashes[1] {
		t.Fatal("the server ID does not reach the login hash")
	}
}

func TestAcceptNegotiatesCompression(t *testing.T) {
	client, server := loginPair(t)
	results := negotiate(t, client)

	acceptor := newAcceptor(t, acceptorKey(t), login.WithCompressionThreshold(16))
	if _, err := acceptor.Accept(t.Context(), server); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if result := awaitNegotiation(t, results); result.err != nil {
		t.Fatalf("Negotiate: %v", result.err)
	}

	assertPipeline(t, server, "compression.enabled", "true")
	assertPipeline(t, client, "compression.enabled", "true")

	// One packet below the threshold and one above it. Both cross intact, so
	// both envelope forms round-trip.
	exchangeChat(t, server, client, "hi")
	exchangeChat(t, server, client, strings.Repeat("long ", 64))
}

func TestAcceptSkipsCompressionWhenDisabled(t *testing.T) {
	client, server := loginPair(t)
	results := negotiate(t, client)

	acceptor := newAcceptor(t, acceptorKey(t), login.WithCompressionThreshold(-1))
	if _, err := acceptor.Accept(t.Context(), server); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if result := awaitNegotiation(t, results); result.err != nil {
		t.Fatalf("Negotiate: %v", result.err)
	}

	assertPipeline(t, server, "compression.enabled", "false")
	assertPipeline(t, client, "compression.enabled", "false")
	exchangeChat(t, server, client, strings.Repeat("long ", 64))
}

// A rejected login must reach the client as a disconnect packet it can read,
// not as a closed socket it can only guess at.
func TestAcceptDisconnectsOnVerifierRejection(t *testing.T) {
	client, server := loginPair(t)
	verifier := onlineVerifier(t)
	verifier.err = errors.New("account did not join")
	results := negotiate(t, client)

	acceptor := newAcceptor(t, acceptorKey(t), login.WithVerifier(verifier))

	_, err := acceptor.Accept(t.Context(), server)
	if !errors.Is(err, login.ErrAuthenticationRejected) {
		t.Fatalf("error = %v, want ErrAuthenticationRejected", err)
	}
	if !errors.Is(err, verifier.err) {
		t.Fatalf("error must wrap the verifier cause, got %v", err)
	}

	result := awaitNegotiation(t, results)
	if !errors.Is(result.err, login.ErrLoginDisconnected) {
		t.Fatalf("client error = %v, want ErrLoginDisconnected", result.err)
	}
}

func TestAcceptRejectsAMalformedUsername(t *testing.T) {
	cases := []struct {
		name     string
		username string
	}{
		{name: "empty", username: ""},
		{name: "oversized", username: strings.Repeat("n", 17)},
		{name: "control character", username: "bad\nname"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client, server := loginPair(t)

			go func() {
				_ = client.Write(t.Context(), protocol.Packet{
					State:     v1_8.StateLogin,
					Direction: protocol.DirectionServerbound,
					ID:        v1_8.LoginServerboundLoginStart{}.PacketID(),
					Value:     &v1_8.LoginServerboundLoginStart{Username: testCase.username},
				})
			}()

			_, err := newAcceptor(t, acceptorKey(t)).Accept(t.Context(), server)
			if !errors.Is(err, java.ErrInvalidUsername) {
				t.Fatalf("error = %v, want ErrInvalidUsername", err)
			}
		})
	}
}

// A client that returns the wrong verify token is either broken or replaying
// somebody else's exchange. Either way the login ends before the session key
// is trusted.
func TestAcceptRejectsAVerifyTokenMismatch(t *testing.T) {
	client, server := loginPair(t)
	verifier := onlineVerifier(t)

	go func() {
		if err := client.Write(t.Context(), protocol.Packet{
			State:     v1_8.StateLogin,
			Direction: protocol.DirectionServerbound,
			ID:        v1_8.LoginServerboundLoginStart{}.PacketID(),
			Value:     &v1_8.LoginServerboundLoginStart{Username: "tester"},
		}); err != nil {
			return
		}

		packet, err := client.Read(t.Context())
		if err != nil {
			return
		}
		request, ok := packet.Value.(*v1_8.LoginClientboundEncryptionBegin)
		if !ok {
			return
		}
		key, err := java.ParseServerPublicKey(request.PublicKey)
		if err != nil {
			return
		}

		secret, err := java.NewSharedSecret()
		if err != nil {
			return
		}
		encryptedSecret, err := java.EncryptToServerKey(key, secret.Reveal())
		if err != nil {
			return
		}
		// One byte different from what the server sent.
		tampered := append([]byte(nil), request.VerifyToken...)
		tampered[0]++
		encryptedToken, err := java.EncryptToServerKey(key, tampered)
		if err != nil {
			return
		}

		_ = client.Write(t.Context(), protocol.Packet{
			State:     v1_8.StateLogin,
			Direction: protocol.DirectionServerbound,
			ID:        v1_8.LoginServerboundEncryptionBegin{}.PacketID(),
			Value: &v1_8.LoginServerboundEncryptionBegin{
				SharedSecret: encryptedSecret,
				VerifyToken:  encryptedToken,
			},
		})
	}()

	_, err := newAcceptor(t, acceptorKey(t), login.WithVerifier(verifier)).Accept(t.Context(), server)
	if !errors.Is(err, java.ErrVerifyTokenMismatch) {
		t.Fatalf("error = %v, want ErrVerifyTokenMismatch", err)
	}
	if verifier.calls != 0 {
		t.Fatal("the verifier must not be called after a token mismatch")
	}
}

func TestAcceptReportsAClientThatVanishes(t *testing.T) {
	client, server := loginPair(t)

	go func() {
		_ = client.Write(t.Context(), protocol.Packet{
			State:     v1_8.StateLogin,
			Direction: protocol.DirectionServerbound,
			ID:        v1_8.LoginServerboundLoginStart{}.PacketID(),
			Value:     &v1_8.LoginServerboundLoginStart{Username: "tester"},
		})
		_ = client.Close()
	}()

	acceptor := newAcceptor(t, acceptorKey(t), login.WithVerifier(onlineVerifier(t)))
	if _, err := acceptor.Accept(t.Context(), server); err == nil {
		t.Fatal("Accept returned nil for a client that disconnected mid-login")
	}
}

// Cancellation has to be honoured at every phase, because a login that hangs
// holds a connection slot open for as long as the peer stays silent.
func TestAcceptHonoursCancellation(t *testing.T) {
	cases := []struct {
		name  string
		phase func(t *testing.T, client *protocol.Stream)
	}{
		{
			name:  "before login start",
			phase: func(*testing.T, *protocol.Stream) {},
		},
		{
			name: "after login start",
			phase: func(t *testing.T, client *protocol.Stream) {
				_ = client.Write(t.Context(), protocol.Packet{
					State:     v1_8.StateLogin,
					Direction: protocol.DirectionServerbound,
					ID:        v1_8.LoginServerboundLoginStart{}.PacketID(),
					Value:     &v1_8.LoginServerboundLoginStart{Username: "tester"},
				})
			},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			client, server := loginPair(t)
			testCase.phase(t, client)

			ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
			defer cancel()

			acceptor := newAcceptor(t, acceptorKey(t), login.WithVerifier(onlineVerifier(t)))
			if _, err := acceptor.Accept(ctx, server); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("error = %v, want context.DeadlineExceeded", err)
			}
		})
	}
}

func TestNewAcceptorValidatesItsConfiguration(t *testing.T) {
	if _, err := login.NewAcceptor(nil); !errors.Is(err, java.ErrInvalidServerKey) {
		t.Fatalf("error = %v, want ErrInvalidServerKey", err)
	}

	key := acceptorKey(t)
	long := strings.Repeat("x", login.MaxServerIDBytes+1)
	if _, err := login.NewAcceptor(key, login.WithServerID(long)); !errors.Is(err, login.ErrInvalidLoginField) {
		t.Fatalf("error = %v, want ErrInvalidLoginField", err)
	}
	if _, err := login.NewAcceptor(key, login.WithVerifier(nil)); !errors.Is(err, login.ErrInvalidAuthenticator) {
		t.Fatalf("error = %v, want ErrInvalidAuthenticator", err)
	}
}

func TestAcceptRejectsANilStream(t *testing.T) {
	if _, err := newAcceptor(t, acceptorKey(t)).Accept(t.Context(), nil); !errors.Is(err, protocol.ErrInvalidStream) {
		t.Fatalf("error = %v, want ErrInvalidStream", err)
	}
}

// The Verifier is the acceptor's only outbound edge. A session-server call
// hidden inside the acceptor would make every server that supplies its own
// verifier issue two requests, so the package must not import an HTTP client.
func TestAcceptorMakesNoRequestOfItsOwn(t *testing.T) {
	source, err := os.ReadFile("acceptor.go")
	if err != nil {
		t.Fatalf("read acceptor.go: %v", err)
	}
	if strings.Contains(string(source), "net/http") {
		t.Fatal("the acceptor must not make a request of its own")
	}
}

func assertState(t *testing.T, stream *protocol.Stream, want protocol.State) {
	t.Helper()

	snapshot, err := stream.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if snapshot.State != want {
		t.Fatalf("state = %q, want %q", snapshot.State, want)
	}
}

func assertPipeline(t *testing.T, stream *protocol.Stream, setting, want string) {
	t.Helper()

	snapshot, err := stream.Snapshot(t.Context())
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if got := snapshot.Pipeline[setting]; got != want {
		t.Fatalf("%s = %q, want %q", setting, got, want)
	}
}

// exchangeChat sends one play packet from the server to the client and asserts
// it arrives unchanged.
func exchangeChat(t *testing.T, server, client *protocol.Stream, message string) {
	t.Helper()

	sent := &v1_8.PlayClientboundChat{Message: message, Position: 1}

	errs := make(chan error, 1)
	go func() {
		errs <- server.Write(t.Context(), protocol.Packet{
			State:     v1_8.StatePlay,
			Direction: protocol.DirectionClientbound,
			ID:        sent.PacketID(),
			Value:     sent,
		})
	}()

	packet, err := client.Read(t.Context())
	if err != nil {
		t.Fatalf("read play packet: %v", err)
	}
	if err := <-errs; err != nil {
		t.Fatalf("write play packet: %v", err)
	}

	received, ok := packet.Value.(*v1_8.PlayClientboundChat)
	if !ok {
		t.Fatalf("received %T, want *v1_8.PlayClientboundChat", packet.Value)
	}
	if received.Message != message {
		t.Fatalf("message length %d, want %d", len(received.Message), len(message))
	}
}
