package protocol

import (
	"io"
)

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
	// RoleClient creates a codec from the client endpoint's perspective.
	RoleClient Role = iota + 1
	// RoleServer creates a codec from the server endpoint's perspective.
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

// Packet retains both decoded and raw access to one packet body.
type Packet struct {
	State     State
	Direction Direction
	ID        int32
	Name      string
	Value     any
	Payload   []byte
}

// Codec owns stateful encoding and decoding for one connection.
type Codec interface {
	Read(io.Reader) (Packet, error)
	Write(io.Writer, Packet) error
}

// Protocol creates per-connection codecs for one immutable version.
type Protocol interface {
	ID() string
	Edition() Edition
	Version() Version
	NewCodec(Role, Limits) (Codec, error)
}
