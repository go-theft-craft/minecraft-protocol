package login

import (
	"context"
	"crypto/md5" //nolint:gosec // G501: the offline UUID derivation is MD5 by definition.
	"crypto/rand"
	"crypto/rsa"
	"fmt"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

// VerifyTokenBytes is the length of the token a server puts in its encryption
// request. Vanilla sends four bytes and vanilla clients return exactly what
// they were sent, so a longer token would work but would not be what a real
// client has ever seen.
const VerifyTokenBytes = 4

// Acceptor runs the server half of the login sequence.
//
// It is the counterpart to Negotiator, and the two are tested against each
// other. Everything the sequence needs beyond the wire — deciding whether an
// account really joined — is the Verifier's, so the acceptor itself makes no
// request of its own.
type Acceptor struct {
	key       *rsa.PrivateKey
	verifier  Verifier
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
// compression, then success. Compression comes last before success because a
// client applies the threshold the moment it reads the packet, and success is
// the first packet that has to cross under the new setting on both sides.
func (a *Acceptor) Accept(ctx context.Context, stream *protocol.Stream) (Profile, error) {
	if stream == nil {
		return Profile{}, fmt.Errorf("%w: nil stream", protocol.ErrInvalidStream)
	}

	username, err := a.readLoginStart(ctx, stream)
	if err != nil {
		return Profile{}, err
	}

	profile := Profile{Name: username, UUID: OfflineUUID(username)}
	if a.verifier != nil {
		profile, err = a.exchangeKeys(ctx, stream, username)
		if err != nil {
			// The client is still readable here, so it learns why rather
			// than watching the socket close.
			a.disconnect(ctx, stream, "Login failed")

			return Profile{}, err
		}
	}

	if a.compress {
		if err := a.write(ctx, stream, &v1_8.LoginClientboundCompress{Threshold: a.threshold}); err != nil {
			return Profile{}, fmt.Errorf("write set compression: %w", err)
		}
	}

	success := &v1_8.LoginClientboundSuccess{
		UUID:     profile.UUID.String(),
		Username: profile.Name.String(),
	}
	if err := a.write(ctx, stream, success); err != nil {
		return Profile{}, fmt.Errorf("write login success: %w", err)
	}

	// The success packet proposes the transition to play and the stream
	// commits it after the write. Confirming it here means a caller never
	// starts a play session on a stream that is still in login.
	snapshot, err := stream.Snapshot(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("confirm play state: %w", err)
	}
	if snapshot.State != v1_8.StatePlay {
		return Profile{}, fmt.Errorf(
			"%w: state is %q after login success, want %q",
			ErrUnexpectedLoginPacket,
			snapshot.State,
			v1_8.StatePlay,
		)
	}

	return profile, nil
}

// readLoginStart reads the packet that opens a login and validates the only
// field it carries. The peer is entirely unauthenticated at this point, so the
// name is parsed before anything else uses it.
func (a *Acceptor) readLoginStart(ctx context.Context, stream *protocol.Stream) (java.Username, error) {
	packet, err := stream.Read(ctx)
	if err != nil {
		return java.Username{}, fmt.Errorf("read login start: %w", err)
	}

	start, ok := packet.Value.(*v1_8.LoginServerboundLoginStart)
	if !ok {
		return java.Username{}, fmt.Errorf(
			"%w: ID %#x in state %q",
			ErrUnexpectedLoginPacket,
			packet.ID,
			packet.State,
		)
	}

	username, err := java.ParseUsername(start.Username)
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

	request := &v1_8.LoginClientboundEncryptionBegin{
		ServerID:    a.serverID,
		PublicKey:   encoded,
		VerifyToken: token,
	}
	if err := a.write(ctx, stream, request); err != nil {
		return Profile{}, fmt.Errorf("write encryption request: %w", err)
	}

	packet, err := stream.Read(ctx)
	if err != nil {
		return Profile{}, fmt.Errorf("read encryption response: %w", err)
	}
	response, ok := packet.Value.(*v1_8.LoginServerboundEncryptionBegin)
	if !ok {
		return Profile{}, fmt.Errorf(
			"%w: ID %#x in state %q",
			ErrUnexpectedLoginPacket,
			packet.ID,
			packet.State,
		)
	}

	// The token is checked before the session key is decrypted, so a client
	// that cannot prove it answered this request never has its key adopted.
	if err := java.VerifyToken(a.key, token, response.VerifyToken); err != nil {
		return Profile{}, fmt.Errorf("verify token: %w", err)
	}

	secret, err := java.DecryptSharedSecret(a.key, response.SharedSecret)
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

// write sends one clientbound login packet.
func (a *Acceptor) write(ctx context.Context, stream *protocol.Stream, value any) error {
	identified, ok := value.(interface{ PacketID() int32 })
	if !ok {
		return fmt.Errorf("%w: %T has no packet ID", ErrUnexpectedLoginPacket, value)
	}

	return stream.Write(ctx, protocol.Packet{
		State:     v1_8.StateLogin,
		Direction: protocol.DirectionClientbound,
		ID:        identified.PacketID(),
		Value:     value,
	})
}

// disconnect tells the client why the login ended. Its own failure is
// discarded: the login has already failed, and a client that cannot be written
// to is a client that is already gone.
func (a *Acceptor) disconnect(ctx context.Context, stream *protocol.Stream, reason string) {
	_ = a.write(ctx, stream, &v1_8.LoginClientboundDisconnect{
		Reason: fmt.Sprintf("{%q:%q}", "text", reason),
	})
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
