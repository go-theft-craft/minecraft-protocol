// Package protocols resolves a protocol ID to a protocol.
//
// It exists because a capture header names a version as text — "java/26.1" —
// and something has to turn that back into code that can decode the frames.
//
// It is a separate package, and it is imported by `cmd/mcproto` and by
// consumers that deliberately want every version. Resolving through a global
// registry populated by package initialization was the obvious alternative and
// is the reason this package exists instead: an init-registered registry links
// every generated version into every program that imports the library, so a
// consumer that speaks only 1.8.9 would carry a megabyte of protocol 775
// tables it never calls.
package protocols

import (
	"fmt"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v1_8 "github.com/go-theft-craft/minecraft-protocol/generated/java/v1_8"
	v26_1 "github.com/go-theft-craft/minecraft-protocol/generated/java/v26_1"
)

// All returns every protocol this package knows, newest first.
func All() []protocol.Protocol {
	return []protocol.Protocol{v26_1.Protocol(), v1_8.Protocol()}
}

// IDs returns the protocol IDs, newest first, for a usage message or a
// completion list.
func IDs() []string {
	descriptors := All()
	ids := make([]string, len(descriptors))
	for index, descriptor := range descriptors {
		ids[index] = descriptor.ID()
	}

	return ids
}

// Resolve returns the protocol with this ID.
func Resolve(id string) (protocol.Protocol, bool) {
	for _, descriptor := range All() {
		if descriptor.ID() == id {
			return descriptor, true
		}
	}

	return nil, false
}

// Default returns the newest protocol. It is what a command uses when a caller
// states no version, and it changes when a newer version is added — which is
// why nothing that must keep working across releases should rely on it.
func Default() protocol.Protocol { return v26_1.Protocol() }

// Handshake builds the packet that opens a connection.
//
// It lives here because it is the one packet a version-neutral tool must build
// itself: the handshake is what selects the state where per-version exchanges
// take over, so nothing else can have built it yet. This package already knows
// every version, so it can do it with the generated types rather than by
// reflecting over field names that happen to match.
func Handshake(
	descriptor protocol.Protocol,
	host string,
	port uint16,
	nextState int32,
) (protocol.Packet, error) {
	switch descriptor.ID() {
	case v1_8.Protocol().ID():
		value := &v1_8.HandshakingServerboundSetProtocol{
			ProtocolVersion: descriptor.Version().Protocol,
			ServerHost:      host,
			ServerPort:      port,
			NextState:       nextState,
		}

		return protocol.Packet{
			State:     v1_8.StateHandshaking,
			Direction: protocol.DirectionServerbound,
			ID:        value.PacketID(),
			Name:      "set_protocol",
			Value:     value,
		}, nil

	case v26_1.Protocol().ID():
		value := &v26_1.HandshakingServerboundSetProtocol{
			ProtocolVersion: descriptor.Version().Protocol,
			ServerHost:      host,
			ServerPort:      port,
			NextState:       nextState,
		}

		return protocol.Packet{
			State:     v26_1.StateHandshaking,
			Direction: protocol.DirectionServerbound,
			ID:        value.PacketID(),
			Name:      "set_protocol",
			Value:     value,
		}, nil

	default:
		return protocol.Packet{}, fmt.Errorf("no handshake known for protocol %q", descriptor.ID())
	}
}

// HandshakeFields are the fields of the packet that opens a connection.
type HandshakeFields struct {
	ProtocolVersion int32
	ServerHost      string
	ServerPort      uint16
	NextState       int32
}

// ReadHandshake reads the fields of a handshake packet.
//
// It lives beside Handshake for the same reason: the handshake is the one
// packet that has to be understood before anything version-specific can be,
// because it is what says which version the peer intends to speak.
func ReadHandshake(packet protocol.Packet) (HandshakeFields, bool) {
	switch value := packet.Value.(type) {
	case *v1_8.HandshakingServerboundSetProtocol:
		return HandshakeFields{
			ProtocolVersion: value.ProtocolVersion,
			ServerHost:      value.ServerHost,
			ServerPort:      value.ServerPort,
			NextState:       value.NextState,
		}, true
	case *v26_1.HandshakingServerboundSetProtocol:
		return HandshakeFields{
			ProtocolVersion: value.ProtocolVersion,
			ServerHost:      value.ServerHost,
			ServerPort:      value.ServerPort,
			NextState:       value.NextState,
		}, true
	default:
		return HandshakeFields{}, false
	}
}

// StatusResponse builds a status response carrying one JSON document.
func StatusResponse(descriptor protocol.Protocol, document string) (protocol.Packet, error) {
	switch descriptor.ID() {
	case v1_8.Protocol().ID():
		value := &v1_8.StatusClientboundServerInfo{Response: document}

		return protocol.Packet{
			State:     v1_8.StateStatus,
			Direction: protocol.DirectionClientbound,
			ID:        value.PacketID(),
			Name:      "server_info",
			Value:     value,
		}, nil

	case v26_1.Protocol().ID():
		value := &v26_1.StatusClientboundServerInfo{Response: document}

		return protocol.Packet{
			State:     v26_1.StateStatus,
			Direction: protocol.DirectionClientbound,
			ID:        value.PacketID(),
			Name:      "server_info",
			Value:     value,
		}, nil

	default:
		return protocol.Packet{}, fmt.Errorf("no status response known for protocol %q", descriptor.ID())
	}
}

// PlayReply is the answer one packet requires from a client that wants the
// connection to keep going.
//
// A client that reads and never answers is not a client a server keeps talking
// to: it stops sending world data until a teleport is confirmed, and
// eventually disconnects a peer that ignores keepalives. A capture taken by
// such a client holds a connection that stalled, which is not the connection
// anybody wanted to record.
//
// This covers the two answers that gate progress and nothing else. It is not a
// client: it does not move, look, or interact.
func PlayReply(_ protocol.Protocol, packet protocol.Packet) (protocol.Packet, bool) {
	switch value := packet.Value.(type) {
	case *v1_8.PlayClientboundKeepAlive:
		reply := &v1_8.PlayServerboundKeepAlive{KeepAliveID: value.KeepAliveID}

		return serverbound(v1_8.StatePlay, reply.PacketID(), "keep_alive", reply), true

	case *v26_1.PlayClientboundKeepAlive:
		reply := &v26_1.PlayServerboundKeepAlive{KeepAliveID: value.KeepAliveID}

		return serverbound(v26_1.StatePlay, reply.PacketID(), "keep_alive", reply), true

	case *v26_1.PlayClientboundPosition:
		// Protocol 775 places a player with a teleport that carries an ID, and
		// a server sends no world data until the ID comes back.
		reply := &v26_1.PlayServerboundTeleportConfirm{TeleportID: value.TeleportID}

		return serverbound(v26_1.StatePlay, reply.PacketID(), "teleport_confirm", reply), true

	case *v26_1.ConfigurationClientboundKeepAlive:
		reply := &v26_1.ConfigurationServerboundKeepAlive{KeepAliveID: value.KeepAliveID}

		return serverbound(v26_1.StateConfiguration, reply.PacketID(), "keep_alive", reply), true

	default:
		return protocol.Packet{}, false
	}
}

func serverbound(state protocol.State, id int32, name string, value any) protocol.Packet {
	return protocol.Packet{
		State:     state,
		Direction: protocol.DirectionServerbound,
		ID:        id,
		Name:      name,
		Value:     value,
	}
}

// KeepAlive builds the clientbound keepalive a server sends to hold a
// connection open.
//
// A client disconnects itself when a server goes quiet, so anything that wants
// to keep a real client connected past the end of what it has to say needs
// this.
func KeepAlive(descriptor protocol.Protocol, id int64) (protocol.Packet, error) {
	switch descriptor.ID() {
	case v1_8.Protocol().ID():
		value := &v1_8.PlayClientboundKeepAlive{KeepAliveID: int32(id)}

		return clientbound(v1_8.StatePlay, value.PacketID(), "keep_alive", value), nil

	case v26_1.Protocol().ID():
		value := &v26_1.PlayClientboundKeepAlive{KeepAliveID: id}

		return clientbound(v26_1.StatePlay, value.PacketID(), "keep_alive", value), nil

	default:
		return protocol.Packet{}, fmt.Errorf("no keepalive known for protocol %q", descriptor.ID())
	}
}

func clientbound(state protocol.State, id int32, name string, value any) protocol.Packet {
	return protocol.Packet{
		State:     state,
		Direction: protocol.DirectionClientbound,
		ID:        id,
		Name:      name,
		Value:     value,
	}
}
