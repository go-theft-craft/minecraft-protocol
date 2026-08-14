package protocol

// Edition identifies a protocol family without imposing transport semantics.
type Edition string

const (
	// EditionJava identifies Java Edition protocols.
	EditionJava Edition = "java"
	// EditionBedrock identifies Bedrock Edition protocols.
	EditionBedrock Edition = "bedrock"
)

// Role selects packet directions from one endpoint's perspective.
type Role uint8

const (
	// RoleClient creates a session from the client endpoint's perspective.
	RoleClient Role = iota + 1
	// RoleServer creates a session from the server endpoint's perspective.
	RoleServer
)

// State identifies a protocol connection state.
type State string

// Direction identifies the flow of a packet.
type Direction uint8

const (
	// DirectionClientbound identifies packets sent to a client.
	DirectionClientbound Direction = iota + 1
	// DirectionServerbound identifies packets sent to a server.
	DirectionServerbound
)

// Version describes one protocol version.
type Version struct {
	Name     string
	Protocol int32
}

// UnknownPacket retains an unrecognized packet body. The payload returned by
// a Session is owned by the caller and does not alias Packet.Payload.
type UnknownPacket struct {
	Payload []byte
}

// Packet retains both decoded and raw access to one packet body.
type Packet struct {
	State     State
	Direction Direction
	ID        int32
	Name      string
	Value     any
	Payload   []byte
}

// Protocol creates per-connection sessions for one immutable version.
type Protocol interface {
	ID() string
	Edition() Edition
	Version() Version
	NewSession(Role, Limits) (Session, error)
}
