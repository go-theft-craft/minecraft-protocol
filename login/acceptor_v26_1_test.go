package login_test

import (
	"context"
	"sync/atomic"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v26_1 "github.com/go-theft-craft/minecraft-protocol/generated/java/v26_1"
	"github.com/go-theft-craft/minecraft-protocol/login"
)

// The acceptor and the negotiator have driven each other over one connection
// since P3b, and until now only at protocol 47. This file is the same pair at
// 775, and it is the first thing in this repository that can serve a modern
// login: while it did not pass, server's generated 775 command trees were
// rendered for a client that could never have reached play to receive them.

func TestAcceptCompletesAnOfflineLoginOn775(t *testing.T) {
	client, server := modernLoginPair(t)
	results := negotiate(t, client)

	profile, err := newAcceptor(t, acceptorKey(t)).Accept(t.Context(), server)
	if err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if profile.Name.String() != "tester" {
		t.Fatalf("profile name = %q, want %q", profile.Name, "tester")
	}
	if profile.UUID != login.OfflineUUID(profile.Name) {
		t.Fatalf("UUID = %s, want the offline derivation %s", profile.UUID, login.OfflineUUID(profile.Name))
	}

	result := awaitNegotiation(t, results)
	if result.err != nil {
		t.Fatalf("Negotiate: %v", result.err)
	}
	// The identity has to survive a round trip the two versions encode
	// differently: 47 sends dashed text and 775 sends sixteen bytes.
	if result.profile.UUID != profile.UUID {
		t.Fatalf("client saw UUID %s, server sent %s", result.profile.UUID, profile.UUID)
	}

	// Play, by the long route. A 775 login success moves nobody: the
	// acknowledgement takes both sides to configuration and the finish
	// handshake takes both to play.
	assertState(t, server, v26_1.StatePlay)
	assertState(t, client, v26_1.StatePlay)
}

func TestAcceptCompletesAnEncryptedLoginOn775(t *testing.T) {
	client, server := modernLoginPair(t)
	verifier := onlineVerifier(t)
	results := negotiate(t, client)

	acceptor := newAcceptor(
		t, acceptorKey(t),
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

	result := awaitNegotiation(t, results)
	if result.err != nil {
		t.Fatalf("Negotiate: %v", result.err)
	}

	// Every frame after the exchange crosses encrypted, and on 775 that is
	// four more of them — the acknowledgement, the finish handshake, and its
	// answer. Reaching play at all is the proof the two ciphers agree.
	assertPipeline(t, server, "encryption.enabled", "true")
	assertPipeline(t, client, "encryption.enabled", "true")
	assertState(t, server, v26_1.StatePlay)
	assertState(t, client, v26_1.StatePlay)
}

func TestAcceptNegotiatesCompressionOn775(t *testing.T) {
	client, server := modernLoginPair(t)
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
}

func TestAcceptRunsTheConfigurationStepOnceOn775(t *testing.T) {
	client, server := modernLoginPair(t)
	results := negotiate(t, client)

	var calls atomic.Int32
	var observed protocol.State

	acceptor := newAcceptor(t, acceptorKey(t), login.WithConfiguration(
		func(ctx context.Context, stream *protocol.Stream) error {
			calls.Add(1)

			snapshot, err := stream.Snapshot(ctx)
			if err != nil {
				return err
			}
			observed = snapshot.State

			// Registry data is what a real server sends here, and none of it
			// belongs to this module. One packet is enough to prove the seam
			// opens at the moment a server can use it.
			value := &v26_1.ConfigurationClientboundFeatureFlags{Features: []string{"minecraft:vanilla"}}

			return stream.Write(ctx, protocol.Packet{
				State:     v26_1.StateConfiguration,
				Direction: protocol.DirectionClientbound,
				ID:        value.PacketID(),
				Value:     value,
			})
		}))

	if _, err := acceptor.Accept(t.Context(), server); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if result := awaitNegotiation(t, results); result.err != nil {
		t.Fatalf("Negotiate: %v", result.err)
	}

	// Once. A step run twice sends a registry twice, and a step run before the
	// acknowledgement writes into a state the client has not entered yet.
	if got := calls.Load(); got != 1 {
		t.Fatalf("the configuration step ran %d times, want 1", got)
	}
	if observed != v26_1.StateConfiguration {
		t.Fatalf("the step saw state %q, want %q", observed, v26_1.StateConfiguration)
	}
	assertState(t, server, v26_1.StatePlay)
	assertState(t, client, v26_1.StatePlay)
}

func TestAcceptRunsNoConfigurationStepOn47(t *testing.T) {
	client, server := loginPair(t)
	results := negotiate(t, client)

	var calls atomic.Int32
	acceptor := newAcceptor(t, acceptorKey(t), login.WithConfiguration(
		func(context.Context, *protocol.Stream) error {
			calls.Add(1)

			return nil
		}))

	if _, err := acceptor.Accept(t.Context(), server); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	if result := awaitNegotiation(t, results); result.err != nil {
		t.Fatalf("Negotiate: %v", result.err)
	}

	// Protocol 47 has no configuration state. A step that ran anyway would
	// write clientbound packets into login, and the failure would read as a
	// codec defect rather than a sequencing one.
	if got := calls.Load(); got != 0 {
		t.Fatalf("the configuration step ran %d times on protocol 47, want 0", got)
	}
}
