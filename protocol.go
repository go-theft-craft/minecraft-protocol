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

// PacketDescriptor resolves packet names and IDs for one protocol, without
// creating a session and without loading game data.
//
// A router registers handlers by name and dispatches by ID, so it needs the
// mapping before a connection exists. Reading it from a session would mean
// creating one to answer a question about the protocol rather than about the
// connection, and reading it from the game-data set would mean linking every
// registry a version publishes in order to look up a name.
//
// It is an optional interface, so a protocol that cannot name its packets
// stays usable by ID alone.
type PacketDescriptor interface {
	// PacketID resolves a packet name. It reports false for a name the
	// protocol does not define in that state and direction.
	PacketID(state State, direction Direction, name string) (int32, bool)
	// PacketName is the reverse, for reporting.
	PacketName(state State, direction Direction, id int32) (string, bool)
}
