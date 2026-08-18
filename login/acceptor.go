package login

import (
	"context"
	"crypto/md5" //nolint:gosec // G501: the offline UUID derivation is MD5 by definition.
	"crypto/rand"
	"crypto/rsa"
	"fmt"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

// VerifyTokenBytes is the length of the token a server puts in its encryption
// request. Vanilla sends four bytes and vanilla clients return exactly what
// they were sent, so a longer token would work but would not be what a real
// client has ever seen.
const VerifyTokenBytes = 4

// Acceptor runs the server half of the login sequence.
//
// It names no packet type and no version, for the same reason Negotiator does
// not: the stream's protocol says which part each packet plays and how to
// build the packets that play a part. That is what lets one acceptor serve
// protocol 47, whose login ends at success, and protocol 775, whose login
// continues through configuration.
//
// It is the counterpart to Negotiator, and the two are tested against each
// other on both protocols. Everything the sequence needs beyond the wire —
// deciding whether an account really joined, and what a configuration state
// should contain — belongs to the Verifier and the configuration step, so the
// acceptor itself makes no request of its own and sends no content of its own.
type Acceptor struct {
	key       *rsa.PrivateKey
	verifier  Verifier
	configure func(context.Context, *protocol.Stream) error
	serverID  string
	threshold int32
	compress  bool
}

// AcceptorOption configures an acceptor.
type AcceptorOption func(*Acceptor) error

// WithVerifier turns the login online: the acceptor performs the key exchange
// and asks the verifier whether the account joined. Without it the login is
// offline, no encryption is negotiated, and the profile is derived from the
// name the client claimed.
func WithVerifier(verifier Verifier) AcceptorOption {
	return func(a *Acceptor) error {
		if verifier == nil {
			return fmt.Errorf("%w: nil verifier", ErrInvalidAuthenticator)
		}
		a.verifier = verifier

		return nil
	}
}

// WithConfiguration sets the step the acceptor runs while the connection is in
// a configuration state, after the client has acknowledged the login success
// and before the packet that ends configuration.
//
// It exists because protocol 775 puts a state between login and play that a
// real client will not leave until it has the registries, tags, and data packs
// it needs, and all of that is game content rather than protocol. This package
// owns the sequence and has no registry to send, so the state is opened and
// handed to whoever does.
//
// The default sends nothing. That is enough for a client that answers what it
// is sent — which is what this repository's own negotiator does — and it is
// not enough for a vanilla client, which will wait in configuration for data
// that never arrives. A server that wants a vanilla client to reach play
// supplies this.
//
// It is never called on a protocol whose login ends at success. Protocol 47
// has no configuration state, and a step that ran anyway would write into
// login.
func WithConfiguration(step func(context.Context, *protocol.Stream) error) AcceptorOption {
	return func(a *Acceptor) error {
		if step == nil {
			return fmt.Errorf("%w: nil configuration step", ErrInvalidAuthenticator)
		}
		a.configure = step

		return nil
	}
}

// WithCompressionThreshold sets the compression threshold the acceptor offers
// during login. A negative threshold disables compression, which is also the
// default, and sends no packet at all.
func WithCompressionThreshold(threshold int) AcceptorOption {
	return func(a *Acceptor) error {
		if threshold < 0 {
			a.compress = false

			return nil
		}
		a.compress = true
		a.threshold = int32(threshold)

		return nil
	}
}

// WithServerID sets the server ID that goes into the encryption request and
// the login hash. Vanilla servers send an empty ID, which is the default.
func WithServerID(serverID string) AcceptorOption {
	return func(a *Acceptor) error {
		if len(serverID) > MaxServerIDBytes {
			return fmt.Errorf(
				"%w: server ID is %d bytes, limit %d",
				ErrInvalidLoginField,
				len(serverID),
				MaxServerIDBytes,
			)
		}
		a.serverID = serverID

		return nil
	}
}

// NewAcceptor validates the configuration and returns an acceptor. key is the
// server's RSA key pair; its public half goes to every client that logs in.
func NewAcceptor(key *rsa.PrivateKey, options ...AcceptorOption) (*Acceptor, error) {
	if key == nil {
		return nil, fmt.Errorf("%w: nil private key", java.ErrInvalidServerKey)
	}

	acceptor := &Acceptor{key: key}
	for _, option := range options {
		if err := option(acceptor); err != nil {
			return nil, err
		}
	}

	return acceptor, nil
}

// Accept runs the login sequence and returns the profile the client proved.
//
// It calls stream.Read, so it owns inbound delivery until it returns. The
// stream must already be started and already be in the login state, which is
// what the handshake packet puts it in.
//
// On return the session has reached play, encryption is active if a verifier
// was configured, and compression is active if a threshold was. The caller
// resumes reading.
//
// The order is fixed and every step depends on the one before it: login start,
// then the key exchange and verification if the login is online, then
// compression, then success, then whatever the protocol puts between success
// and play. Compression comes last before success because a client applies the
// threshold the moment it reads the packet, and success is the first packet
// that has to cross under the new setting on both sides.
func (a *Acceptor) Accept(ctx context.Context, stream *protocol.Stream) (Profile, error) {
	if stream == nil {
		return Profile{}, fmt.Errorf("%w: nil stream", protocol.ErrInvalidStream)
	}
	exchange, ok := stream.LoginExchange()
	if !ok {
		return Profile{}, fmt.Errorf("%w: no login exchange", ErrUnsupportedProtocol)
	}

	username, err := a.readLoginStart(ctx, stream, exchange)
	if err != nil {
		return Profile{}, err
	}

	profile := Profile{Name: username, UUID: OfflineUUID(username)}
	if a.verifier != nil {
		profile, err = a.exchangeKeys(ctx, stream, exchange, username)
		if err != nil {
			// The client is still readable here, so it learns why rather
			// than watching the socket close.
			a.disconnect(ctx, stream, exchange, "Login failed")

			return Profile{}, err
		}
	}

	if a.compress {
		packet, has := exchange.WriteSetCompression(a.threshold)
		if !has {
			return Profile{}, fmt.Errorf(
				"%w: a compression threshold was configured and the protocol has no packet for one",
				ErrUnexpectedLoginPacket,
			)
		}
		if err := stream.Write(ctx, packet); err != nil {
			return Profile{}, fmt.Errorf("write set compression: %w", err)
		}
	}

	success, err := exchange.WriteLoginSuccess(protocol.LoginIdentity{
		Username: profile.Name.String(),
		UUID:     profile.UUID.String(),
	})
	if err != nil {
		return Profile{}, fmt.Errorf("build login success: %w", err)
	}
	if err := stream.Write(ctx, success); err != nil {
		return Profile{}, fmt.Errorf("write login success: %w", err)
	}

	if err := a.reachPlay(ctx, stream, exchange); err != nil {
		return Profile{}, err
	}

	return profile, nil
}

// reachPlay takes the connection from login success to play.
//
// Which route that is belongs to the protocol. Protocol 47's login success is
// itself the transition and there is nothing left to do. A modern login is not
// over at success: the client acknowledges it, and the acknowledgement is what
// moves both sides to configuration; the server fills that state; and the
// finish handshake and its answer are what reach play.
//
// The two are told apart by asking the exchange whether the client has an
// acknowledgement to send, which is the same question the negotiator asks on
// the other side of the wire.
func (a *Acceptor) reachPlay(
	ctx context.Context,
	stream *protocol.Stream,
	exchange protocol.LoginExchange,
) error {
	if _, continues := exchange.Answer(protocol.RoleLoginAcknowledged); !continues {
		return a.confirmPlay(ctx, stream)
	}

	if err := a.await(ctx, stream, protocol.RoleLoginAcknowledged); err != nil {
		return err
	}

	if a.configure != nil {
		if err := a.configure(ctx, stream); err != nil {
			return fmt.Errorf("configure: %w", err)
		}
	}

	finished, has := exchange.Announce(protocol.RoleConfigurationFinished)
	if !has {
		return fmt.Errorf(
			"%w: the protocol continues past login success and states no way to finish configuration",
			ErrUnexpectedLoginPacket,
		)
	}
	if err := stream.Write(ctx, finished); err != nil {
		return fmt.Errorf("write finish configuration: %w", err)
	}
	if err := a.await(ctx, stream, protocol.RoleConfigurationFinished); err != nil {
		return err
	}

	return a.confirmPlay(ctx, stream)
}

// await reads until the client plays the part the sequence is waiting for.
//
// Configuration is a state a client fills with content of its own — its
// settings, its plugin channels, its cookie answers — and none of it plays a
// part in the sequence. Passing it through is deliberate. In login, a packet
// with no part is a protocol error, because there is nothing a client may
// legitimately say there that the sequence has no name for.
func (a *Acceptor) await(ctx context.Context, stream *protocol.Stream, want protocol.LoginRole) error {
	for {
		packet, err := stream.Read(ctx)
		if err != nil {
			return fmt.Errorf("read %s: %w", want, err)
		}

		role, known := stream.LoginRole(packet.State, packet.Direction, packet.ID)
		switch {
		case known && role == want:
			return nil

		case known:
			return fmt.Errorf(
				"%w: received the %s packet while waiting for %s",
				ErrUnexpectedLoginPacket,
				role,
				want,
			)

		case packet.State == stateConfiguration:
			continue

		default:
			return fmt.Errorf(
				"%w: ID %#x in state %q",
				ErrUnexpectedLoginPacket,
				packet.ID,
				packet.State,
			)
		}
	}
}

// confirmPlay checks that the sequence left the connection where it claims to.
//
// Every transition here is proposed by a packet and committed by the stream
// after that packet is fully read or written, so this asserts the result of
// the last one rather than performing it. Confirming it means a caller never
// starts a play session on a stream that is still in login or configuration.
func (a *Acceptor) confirmPlay(ctx context.Context, stream *protocol.Stream) error {
	snapshot, err := stream.Snapshot(ctx)
	if err != nil {
		return fmt.Errorf("confirm play state: %w", err)
	}
	if snapshot.State != statePlay {
		return fmt.Errorf(
			"%w: state is %q at the end of the login sequence, want %q",
			ErrUnexpectedLoginPacket,
			snapshot.State,
			statePlay,
		)
	}

	return nil
}

// readLoginStart reads the packet that opens a login and validates the only
// field it carries that this side will use. The peer is entirely
// unauthenticated at this point, so the name is parsed before anything else
// touches it.
//
// A protocol that carries a UUID here is read and the UUID discarded on
// purpose: offline mode derives one from the name and online mode takes the
// account's, so a value a client chose for itself never reaches a Profile.
func (a *Acceptor) readLoginStart(
	ctx context.Context,
	stream *protocol.Stream,
	exchange protocol.LoginExchange,
) (java.Username, error) {
	packet, err := stream.Read(ctx)
	if err != nil {
		return java.Username{}, fmt.Errorf("read login start: %w", err)
	}

	if role, known := stream.LoginRole(packet.State, packet.Direction, packet.ID); !known ||
		role != protocol.RoleLoginStart {
		return java.Username{}, fmt.Errorf(
			"%w: ID %#x in state %q",
			ErrUnexpectedLoginPacket,
			packet.ID,
			packet.State,
		)
	}

	identity, err := exchange.ReadLoginStart(packet)
	if err != nil {
		return java.Username{}, err
	}

	username, err := java.ParseUsername(identity.Username)
	if err != nil {
		return java.Username{}, fmt.Errorf("login start username: %w", err)
	}

	return username, nil
}

// exchangeKeys runs the encryption request and response, installs the cipher,
// and asks the verifier whether the account joined.
//
// The cipher is installed before the verifier is called, not after. The
// exchange is finished by then, and holding plaintext open across a network
// call to the session server would leave the connection unencrypted for as
// long as that call takes.
func (a *Acceptor) exchangeKeys(
	ctx context.Context,
	stream *protocol.Stream,
	exchange protocol.LoginExchange,
	username java.Username,
) (Profile, error) {
	encoded, err := java.EncodeServerPublicKey(&a.key.PublicKey)
	if err != nil {
		return Profile{}, fmt.Errorf("encode server key: %w", err)
	}

	token := make([]byte, VerifyTokenBytes)
	if _, err := rand.Read(token); err != nil {
		return Profile{}, fmt.Errorf("generate verify token: %w", err)
	}

	// A verifier is configured, so the join is wanted. A protocol that cannot
	// state that says nothing and means the same thing.
	request, err := exchange.WriteEncryptionRequest(protocol.EncryptionRequest{
		ServerID:           a.serverID,
		PublicKey:          encoded,
		VerifyToken:        token,
		ShouldAuthenticate: true,
	})
	if err != nil {
		return Profile{}, fmt.Errorf("build encryption request: %w", err)
	}
	if err := stream.Write(ctx, request); err != nil {
		return Profile{}, fmt.Errorf("write encryption request: %w", err)
	}

	packet, err := stream.Read(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("read encryption response: %w", err)
	}
	if role, known := stream.LoginRole(packet.State, packet.Direction, packet.ID); !known ||
		role != protocol.RoleEncryptionResponse {
		return Profile{}, fmt.Errorf(
			"%w: ID %#x in state %q",
			ErrUnexpectedLoginPacket,
			packet.ID,
			packet.State,
		)
	}
	encryptedSecret, encryptedToken, err := exchange.ReadEncryptionResponse(packet)
	if err != nil {
		return Profile{}, err
	}

	// The token is checked before the session key is decrypted, so a client
	// that cannot prove it answered this request never has its key adopted.
	if err := java.VerifyToken(a.key, token, encryptedToken); err != nil {
		return Profile{}, fmt.Errorf("verify token: %w", err)
	}

	secret, err := java.DecryptSharedSecret(a.key, encryptedSecret)
	if err != nil {
		return Profile{}, fmt.Errorf("decrypt session key: %w", err)
	}

	hash, err := java.ComputeServerHash(a.serverID, secret, &a.key.PublicKey)
	if err != nil {
		return Profile{}, fmt.Errorf("compute server hash: %w", err)
	}

	if err := stream.Control(ctx, java.EncryptionControl{Secret: secret}); err != nil {
		return Profile{}, fmt.Errorf("enable encryption: %w", err)
	}

	profile, err := a.verifier.Verify(ctx, username, hash)
	if err != nil {
		return Profile{}, fmt.Errorf("%w: %w", ErrAuthenticationRejected, err)
	}
	if profile.Name.IsZero() {
		profile.Name = username
	}
	if profile.UUID.IsZero() {
		return Profile{}, fmt.Errorf("%w: verifier returned no UUID", ErrInvalidLoginField)
	}

	return profile, nil
}

// disconnect tells the client why the login ended. Its own failure is
// discarded: the login has already failed, and a client that cannot be written
// to is a client that is already gone.
//
// It is only ever called while the connection is still in login, which is the
// one state every version renders a reason for the same way.
func (a *Acceptor) disconnect(
	ctx context.Context,
	stream *protocol.Stream,
	exchange protocol.LoginExchange,
	reason string,
) {
	packet, err := exchange.WriteLoginDisconnect(reason)
	if err != nil {
		return
	}
	_ = stream.Write(ctx, packet)
}

// OfflineUUID derives the identity a server without account verification gives
// a player: version 3 over "OfflinePlayer:<name>", which is what vanilla does.
//
// It is a derivation rather than a random value so a player keeps the same
// identity across restarts, and the same one every other implementation would
// give them.
func OfflineUUID(name java.Username) java.UUID {
	//nolint:gosec // G401: the derivation is defined in terms of MD5.
	sum := md5.Sum([]byte("OfflinePlayer:" + name.String()))
	sum[6] = (sum[6] & 0x0f) | 0x30 // version 3
	sum[8] = (sum[8] & 0x3f) | 0x80 // RFC 4122 variant

	return sum
}
