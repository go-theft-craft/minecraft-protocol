package protocol

// LoginRole names the part a packet plays in a login sequence.
//
// It exists so a login driver can be written once against roles rather than
// against one version's concrete packet types. Protocol 47 and protocol 775
// disagree about packet names, packet IDs, and which states exist, but they
// agree about the parts: a server asks for encryption, a client answers, a
// server may set compression, and a server eventually reports success.
type LoginRole string

const (
	// RoleLoginStart is the serverbound packet that opens a login.
	RoleLoginStart LoginRole = "login_start"
	// RoleEncryptionRequest is the clientbound packet carrying the server's
	// public key and verify token.
	RoleEncryptionRequest LoginRole = "encryption_request"
	// RoleEncryptionResponse is the serverbound packet carrying the encrypted
	// session key and verify token.
	RoleEncryptionResponse LoginRole = "encryption_response"
	// RoleSetCompression is the clientbound packet that sets the compression
	// threshold mid-login.
	RoleSetCompression LoginRole = "set_compression"
	// RoleLoginSuccess is the clientbound packet that completes a login.
	RoleLoginSuccess LoginRole = "login_success"
	// RoleLoginAcknowledged is the serverbound packet a client sends to
	// accept a login success, which is what moves a modern connection out of
	// login. Protocol 47 has no such packet: success moves it directly.
	RoleLoginAcknowledged LoginRole = "login_acknowledged"
	// RoleConfigurationFinished tags both halves of the handshake that ends
	// configuration: the server states it is done, the client answers, and
	// the answer is what moves both sides to play.
	RoleConfigurationFinished LoginRole = "configuration_finished"
)

// LoginRoles reports which part of a login a packet plays.
//
// It is an optional interface rather than a Session method, on the same
// reasoning as SensitivePackets: a session for a protocol with no login
// sequence should not have to answer for one. A session that does not
// implement it has no login roles, and a packet with no role reports false.
type LoginRoles interface {
	LoginRole(State, Direction, int32) (LoginRole, bool)
}
