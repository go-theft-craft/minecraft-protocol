package protocol

// LoginIdentity is the account a login presents, or the one a server confirms.
//
// The UUID is text rather than bytes because that is the only form both
// protocols can state: protocol 47 sends dashed text on the wire and protocol
// 775 sends sixteen bytes, and text is what a caller can print, compare, and
// parse. It is empty when there is none to present, which is what an offline
// login does — the server assigns one from the name.
type LoginIdentity struct {
	Username string
	UUID     string
}

// EncryptionRequest is a server's request to encrypt the connection, with the
// fields a client needs to answer it.
type EncryptionRequest struct {
	ServerID    string
	PublicKey   []byte
	VerifyToken []byte
	// ShouldAuthenticate is whether the server expects the client to prove
	// account ownership to the session server before answering. Protocol 775
	// states it; protocol 47 has no such field and reports true, because a
	// protocol 47 server that asks for encryption always expects the join.
	ShouldAuthenticate bool
}

// LoginExchange builds and reads the packets of one protocol's login sequence.
//
// It exists so a login can be driven once rather than once per version.
// LoginRole says which part a packet plays; this says how to build the packet
// that plays a part, and how to read the fields of one that arrived. Both are
// needed: two protocols agree about the parts of a login and agree about
// almost nothing else — not packet names, not IDs, not field types, not even
// which states the sequence passes through.
//
// An implementation is stateless. It reads and writes no session state, so a
// caller may use it from its own goroutine while the stream runs.
type LoginExchange interface {
	// StartLogin builds the packet that opens a login.
	StartLogin(LoginIdentity) (Packet, error)
	// ReadEncryptionRequest reads a server's encryption request.
	ReadEncryptionRequest(Packet) (EncryptionRequest, error)
	// WriteEncryptionResponse builds the answer to one.
	WriteEncryptionResponse(secret, verifyToken []byte) (Packet, error)
	// ReadLoginSuccess reads the account a server confirmed.
	ReadLoginSuccess(Packet) (LoginIdentity, error)
	// Answer builds the packet that answers a step the server has taken:
	// RoleLoginAcknowledged after login success, RoleConfigurationFinished
	// after a server finishes configuration. A protocol without that step
	// reports false, which is how protocol 47 says its login ends at success.
	Answer(LoginRole) (Packet, bool)
	// DisconnectReason reports whether a packet ends the login, and carries
	// the reason when the protocol states it as text. A version whose reason
	// is a structured component reports the disconnect with an empty reason
	// rather than inventing a rendering of it.
	DisconnectReason(Packet) (string, bool)
}

// LoginExchanges is the optional interface a session implements when its
// protocol has a login sequence.
//
// It is optional on the same reasoning as LoginRoles and SensitivePackets: a
// session for a protocol with no login should not have to answer for one.
type LoginExchanges interface {
	LoginExchange() (LoginExchange, bool)
}
