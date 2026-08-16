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
