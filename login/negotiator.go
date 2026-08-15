package login

import (
	"context"
	"errors"
	"fmt"

	protocol "github.com/go-theft-craft/minecraft-protocol"
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
	// ErrUnsupportedProtocol reports a stream whose protocol declares no
	// login sequence, so there is nothing for a negotiator to drive.
	ErrUnsupportedProtocol = errors.New("protocol declares no login sequence")
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
//
// It names no packet type and no version. The stream's protocol says which
// part each packet plays and how to build the packets that answer them, which
// is what lets one negotiator drive protocol 47, whose login ends at success,
// and protocol 775, whose login continues through configuration.
type Negotiator struct {
	authenticator Authenticator
	terminal      protocol.State
}

// NegotiatorOption configures a negotiator.
type NegotiatorOption func(*Negotiator)

// WithTerminalState stops the negotiation when the connection reaches a state
// before play.
//
// It exists because reaching play is not always what a caller wants. A modern
// login passes through configuration, where a server sends the registries and
// tags a client needs, and this negotiator does not read them: it answers the
// finish handshake and moves on. A caller that needs that content stops at
// configuration and drives the rest itself.
//
// A state the protocol never reaches is not an error here — the sequence
// simply runs to its end.
func WithTerminalState(state protocol.State) NegotiatorOption {
	return func(negotiator *Negotiator) { negotiator.terminal = state }
}

// NewNegotiator validates the authenticator and returns a negotiator.
func NewNegotiator(authenticator Authenticator, options ...NegotiatorOption) (*Negotiator, error) {
	if authenticator == nil {
		return nil, fmt.Errorf("%w: nil authenticator", ErrInvalidAuthenticator)
	}
	if authenticator.Profile().Name.IsZero() {
		return nil, fmt.Errorf("%w: profile has no name", ErrInvalidAuthenticator)
	}

	negotiator := &Negotiator{authenticator: authenticator}
	for _, option := range options {
		option(negotiator)
	}

	return negotiator, nil
}

// Negotiate runs the login sequence and returns the profile the server
// confirmed.
//
// It calls stream.Read, so it owns inbound delivery until it returns. A caller
// that reads concurrently would steal packets the sequence needs. The stream
// must already be started and already be in the login state, which is what the
// handshake packet puts it in.
//
// On return the connection has reached play — or the terminal state the caller
// asked for — encryption is active if the server asked for it, and the caller
// resumes reading.
func (n *Negotiator) Negotiate(ctx context.Context, stream *protocol.Stream) (Profile, error) {
	if stream == nil {
		return Profile{}, fmt.Errorf("%w: nil stream", protocol.ErrInvalidStream)
	}
	exchange, ok := stream.LoginExchange()
	if !ok {
		return Profile{}, fmt.Errorf("%w: no login exchange", ErrUnsupportedProtocol)
	}

	profile := n.authenticator.Profile()
	start, err := exchange.StartLogin(protocol.LoginIdentity{Username: profile.Name.String()})
	if err != nil {
		return Profile{}, fmt.Errorf("build login start: %w", err)
	}
	if err := stream.Write(ctx, start); err != nil {
		return Profile{}, fmt.Errorf("write login start: %w", err)
	}

	var confirmed Profile
	for {
		packet, err := stream.Read(ctx)
		if err != nil {
			return Profile{}, fmt.Errorf("read login packet: %w", err)
		}

		if reason, disconnected := exchange.DisconnectReason(packet); disconnected {
			if reason == "" {
				return Profile{}, ErrLoginDisconnected
			}

			return Profile{}, fmt.Errorf("%w: %s", ErrLoginDisconnected, reason)
		}

		role, known := stream.LoginRole(packet.State, packet.Direction, packet.ID)
		if !known {
			// Configuration is a state a server fills with content — the
			// registries, tags, and resource packs a client needs to play —
			// and none of it plays a part in the sequence. Passing it through
			// is deliberate: a caller that needs it stops at configuration and
			// reads it. In login, a packet with no part is a protocol error.
			if packet.State == stateConfiguration {
				continue
			}

			return Profile{}, fmt.Errorf(
				"%w: ID %#x in state %q",
				ErrUnexpectedLoginPacket,
				packet.ID,
				packet.State,
			)
		}

		switch role {
		case protocol.RoleEncryptionRequest:
			if err := n.exchangeKeys(ctx, stream, exchange, packet); err != nil {
				return Profile{}, err
			}

		case protocol.RoleSetCompression:
			// The session proposes this transition and the stream commits it
			// before the packet is delivered here. Nothing to do.

		case protocol.RoleLoginSuccess:
			identity, err := exchange.ReadLoginSuccess(packet)
			if err != nil {
				return Profile{}, err
			}
			confirmed, err = parseIdentity(identity)
			if err != nil {
				return Profile{}, err
			}

			// A protocol whose login ends at success has no acknowledgement
			// to send, and the session has already moved the stream on.
			answer, has := exchange.Answer(protocol.RoleLoginAcknowledged)
			if !has {
				return confirmed, nil
			}
			if n.stopsAt(stateLogin) {
				return confirmed, nil
			}
			if err := stream.Write(ctx, answer); err != nil {
				return Profile{}, fmt.Errorf("write login acknowledgement: %w", err)
			}
			if n.stopsAt(stateConfiguration) {
				return confirmed, nil
			}

		case protocol.RoleConfigurationFinished:
			answer, has := exchange.Answer(protocol.RoleConfigurationFinished)
			if !has {
				return Profile{}, fmt.Errorf(
					"%w: the protocol finishes configuration but supplies no answer",
					ErrUnexpectedLoginPacket,
				)
			}
			if err := stream.Write(ctx, answer); err != nil {
				return Profile{}, fmt.Errorf("write configuration acknowledgement: %w", err)
			}

			return confirmed, nil

		case protocol.RoleLoginStart, protocol.RoleEncryptionResponse, protocol.RoleLoginAcknowledged:
			// These are the parts this negotiator plays, not ones it is told
			// about. Seeing one inbound means a peer sent a packet from the
			// wrong side of the exchange.
			return Profile{}, fmt.Errorf(
				"%w: received the %s packet, which a client sends",
				ErrUnexpectedLoginPacket,
				role,
			)

		default:
			// Every other role belongs to a part of the sequence this
			// negotiator does not drive.
		}
	}
}

// stopsAt reports whether the caller asked to stop at a state.
func (n *Negotiator) stopsAt(state protocol.State) bool {
	return n.terminal != "" && n.terminal == state
}

// The two states a caller can ask to stop at are named here rather than
// imported, because importing a version package to name a state is the version
// dependency this negotiator exists without. Both names are the protocol's
// own, and a version that used different ones would report different states
// through the same interface.
const (
	stateLogin         protocol.State = "login"
	stateConfiguration protocol.State = "configuration"
)

// parseIdentity turns what a server said into a Profile.
//
// Both fields come from a peer that is still unauthenticated at this point, so
// neither is trusted into a Profile without checking.
func parseIdentity(identity protocol.LoginIdentity) (Profile, error) {
	name, err := java.ParseUsername(identity.Username)
	if err != nil {
		return Profile{}, fmt.Errorf("login success username: %w", err)
	}
	parsed, err := java.ParseUUID(identity.UUID)
	if err != nil {
		return Profile{}, fmt.Errorf("login success UUID: %w", err)
	}

	return Profile{Name: name, UUID: parsed}, nil
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
	exchange protocol.LoginExchange,
	packet protocol.Packet,
) error {
	request, err := exchange.ReadEncryptionRequest(packet)
	if err != nil {
		return err
	}
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
	// A server that does not ask for the join does not get one. Protocol 775
	// says so explicitly; protocol 47 has no way to say anything else.
	if request.ShouldAuthenticate {
		if err := n.authenticator.Join(ctx, hash); err != nil {
			return fmt.Errorf("%w: %w", ErrAuthenticationRejected, err)
		}
	}

	encryptedSecret, err := java.EncryptToServerKey(key, secret.Reveal())
	if err != nil {
		return fmt.Errorf("encrypt session key: %w", err)
	}
	encryptedToken, err := java.EncryptToServerKey(key, request.VerifyToken)
	if err != nil {
		return fmt.Errorf("encrypt verify token: %w", err)
	}

	response, err := exchange.WriteEncryptionResponse(encryptedSecret, encryptedToken)
	if err != nil {
		return fmt.Errorf("build encryption response: %w", err)
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
func validateEncryptionRequest(request protocol.EncryptionRequest) error {
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
