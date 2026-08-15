package login

import (
	"context"
	"errors"
	"fmt"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

var (
	// ErrInvalidAuthenticator reports an authenticator that cannot be used.
	ErrInvalidAuthenticator = errors.New("invalid authenticator")
	// ErrAuthenticationRejected reports that the authenticator refused. It
	// always wraps the authenticator's own error.
	ErrAuthenticationRejected = errors.New("authentication rejected")
	// ErrLoginDisconnected reports a disconnect packet during login.
	ErrLoginDisconnected = errors.New("server disconnected during login")
	// ErrUnexpectedLoginPacket reports a packet the login state does not
	// allow at that point.
	ErrUnexpectedLoginPacket = errors.New("unexpected packet during login")
	// ErrInvalidLoginField reports a peer-supplied login field that failed
	// validation. Login is the one exchange where the peer is entirely
	// unauthenticated, so every field it sends is checked before use.
	ErrInvalidLoginField = errors.New("invalid login field")
)

// MaxServerIDBytes is the longest server ID an encryption request may carry.
// It is the protocol's own bound, not a guess.
const MaxServerIDBytes = 20

// Profile identifies the account a login presents.
//
// Both fields are types that cannot hold an invalid value, so a Profile is
// itself proof that validation ran. There is no validate method to forget to
// call.
type Profile struct {
	Name java.Username
	UUID java.UUID
}

// Authenticator proves account ownership during login.
//
// Join receives the server hash and must prove to the session server that this
// account is joining. It performs whatever network work that requires; this
// package makes no request of its own. An offline authenticator does nothing.
type Authenticator interface {
	Profile() Profile
	Join(ctx context.Context, hash java.ServerHash) error
}

// Offline is an authenticator for a server that does not verify accounts.
// Build it with NewOffline, which validates the name.
type Offline struct {
	name java.Username
}

// NewOffline validates an account name and returns an offline authenticator.
func NewOffline(name string) (Offline, error) {
	parsed, err := java.ParseUsername(name)
	if err != nil {
		return Offline{}, fmt.Errorf("offline authenticator: %w", err)
	}

	return Offline{name: parsed}, nil
}

// Profile implements Authenticator.
func (o Offline) Profile() Profile { return Profile{Name: o.name} }

// Join implements Authenticator and does nothing, because there is nobody to
// tell.
func (Offline) Join(context.Context, java.ServerHash) error { return nil }

// Verifier is the server half of authentication: it confirms that the account
// claiming this username really joined with this server hash.
//
// This package defines the interface and never implements it, because
// implementing it means calling a session server, which is a consumer's job.
type Verifier interface {
	Verify(ctx context.Context, username java.Username, hash java.ServerHash) (Profile, error)
}

// Negotiator runs the client half of the login sequence.
type Negotiator struct {
	authenticator Authenticator
}

// NewNegotiator validates the authenticator and returns a negotiator.
func NewNegotiator(authenticator Authenticator) (*Negotiator, error) {
	if authenticator == nil {
		return nil, fmt.Errorf("%w: nil authenticator", ErrInvalidAuthenticator)
	}
	if authenticator.Profile().Name.IsZero() {
		return nil, fmt.Errorf("%w: profile has no name", ErrInvalidAuthenticator)
	}

	return &Negotiator{authenticator: authenticator}, nil
}

// Negotiate runs the login sequence and returns the profile the server
// confirmed.
//
// It calls stream.Read, so it owns inbound delivery until it returns. A caller
// that reads concurrently would steal packets the sequence needs. The stream
// must already be started and already be in the login state, which is what the
// handshake packet puts it in.
//
// On return the stream is in the play state, encryption is active if the
// server asked for it, and the caller resumes reading.
func (n *Negotiator) Negotiate(ctx context.Context, stream *protocol.Stream) (Profile, error) {
	if stream == nil {
		return Profile{}, fmt.Errorf("%w: nil stream", protocol.ErrInvalidStream)
	}

	profile := n.authenticator.Profile()
	start := protocol.Packet{
		State:     v1_8.StateLogin,
		Direction: protocol.DirectionServerbound,
		ID:        v1_8.LoginServerboundLoginStart{}.PacketID(),
		Value:     &v1_8.LoginServerboundLoginStart{Username: profile.Name.String()},
	}
	if err := stream.Write(ctx, start); err != nil {
		return Profile{}, fmt.Errorf("write login start: %w", err)
	}

	for {
		packet, err := stream.Read(ctx)
		if err != nil {
			return Profile{}, fmt.Errorf("read login packet: %w", err)
		}

		switch value := packet.Value.(type) {
		case *v1_8.LoginClientboundEncryptionBegin:
			if err := n.exchangeKeys(ctx, stream, value); err != nil {
				return Profile{}, err
			}

		case *v1_8.LoginClientboundCompress:
			// The session proposes this transition and the stream commits it
			// before the packet is delivered here. Nothing to do.

		case *v1_8.LoginClientboundSuccess:
			// Both fields come from an unauthenticated peer, so neither is
			// trusted into a Profile without checking.
			identity, err := java.ParseUUID(value.UUID)
			if err != nil {
				return Profile{}, fmt.Errorf("login success UUID: %w", err)
			}
			name, err := java.ParseUsername(value.Username)
			if err != nil {
				return Profile{}, fmt.Errorf("login success username: %w", err)
			}

			return Profile{Name: name, UUID: identity}, nil

		case *v1_8.LoginClientboundDisconnect:
			return Profile{}, fmt.Errorf("%w: %s", ErrLoginDisconnected, value.Reason)

		default:
			return Profile{}, fmt.Errorf(
				"%w: ID %#x in state %q",
				ErrUnexpectedLoginPacket,
				packet.ID,
				packet.State,
			)
		}
	}
}

// exchangeKeys answers one encryption request and enables the cipher.
//
// The ordering is the whole point. Stream.Write returns only after the frame
// has reached the transport, and Stream.Control queues behind it on the same
// coordinator, so the response itself is never encrypted and every later byte
// is.
func (n *Negotiator) exchangeKeys(
	ctx context.Context,
	stream *protocol.Stream,
	request *v1_8.LoginClientboundEncryptionBegin,
) error {
	if err := validateEncryptionRequest(request); err != nil {
		return err
	}

	key, err := java.ParseServerPublicKey(request.PublicKey)
	if err != nil {
		return fmt.Errorf("parse server key: %w", err)
	}

	secret, err := java.NewSharedSecret()
	if err != nil {
		return fmt.Errorf("generate session key: %w", err)
	}

	hash, err := java.ComputeServerHash(request.ServerID, secret, key)
	if err != nil {
		return fmt.Errorf("compute server hash: %w", err)
	}
	if err := n.authenticator.Join(ctx, hash); err != nil {
		return fmt.Errorf("%w: %w", ErrAuthenticationRejected, err)
	}

	encryptedSecret, err := java.EncryptToServerKey(key, secret.Reveal())
	if err != nil {
		return fmt.Errorf("encrypt session key: %w", err)
	}
	encryptedToken, err := java.EncryptToServerKey(key, request.VerifyToken)
	if err != nil {
		return fmt.Errorf("encrypt verify token: %w", err)
	}

	response := protocol.Packet{
		State:     v1_8.StateLogin,
		Direction: protocol.DirectionServerbound,
		ID:        v1_8.LoginServerboundEncryptionBegin{}.PacketID(),
		Value: &v1_8.LoginServerboundEncryptionBegin{
			SharedSecret: encryptedSecret,
			VerifyToken:  encryptedToken,
		},
	}
	if err := stream.Write(ctx, response); err != nil {
		return fmt.Errorf("write encryption response: %w", err)
	}

	if err := stream.Control(ctx, java.EncryptionControl{Secret: secret}); err != nil {
		return fmt.Errorf("enable encryption: %w", err)
	}

	return nil
}

// validateEncryptionRequest checks every field of an encryption request before
// any of it is used. The public key is checked by ParseServerPublicKey; these
// are the bounds that parser has no opinion about.
func validateEncryptionRequest(request *v1_8.LoginClientboundEncryptionBegin) error {
	if len(request.ServerID) > MaxServerIDBytes {
		return fmt.Errorf(
			"%w: server ID is %d bytes, limit %d",
			ErrInvalidLoginField,
			len(request.ServerID),
			MaxServerIDBytes,
		)
	}
	if len(request.PublicKey) == 0 {
		return fmt.Errorf("%w: empty public key", ErrInvalidLoginField)
	}
	if len(request.VerifyToken) == 0 {
		return fmt.Errorf("%w: empty verify token", ErrInvalidLoginField)
	}

	return nil
}
