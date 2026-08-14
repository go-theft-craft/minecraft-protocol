package data

import (
	"maps"
	"slices"
)

// PacketID identifies a protocol packet.
type PacketID int32

// Protocol summarizes a Minecraft protocol schema. Complex arrays, switches,
// containers, and other structured definitions are represented as "complex".
type Protocol struct {
	Types  ProtocolTypes
	Phases ProtocolPhases
}

// ProtocolPhase describes protocol directions for a phase.
type ProtocolPhase struct {
	ToClient ProtocolDirection
	ToServer ProtocolDirection
}

// ProtocolDirection describes packets sent in one direction.
type ProtocolDirection struct {
	Packets Packets
}

// Packet describes a protocol packet.
type Packet struct {
	Name   string
	ID     PacketID
	Fields PacketFields
}

// PacketField describes a protocol packet field.
type PacketField struct {
	Name string
	Type string
}

// ProtocolTypes maps protocol type names to their definitions.
type ProtocolTypes map[string]string

// Clone returns protocol types that do not alias the source.
func (p ProtocolTypes) Clone() ProtocolTypes { return maps.Clone(p) }

// ProtocolPhases maps phase names to protocol directions.
type ProtocolPhases map[string]ProtocolPhase

// Clone returns protocol phases whose mutable fields do not alias the source.
func (p ProtocolPhases) Clone() ProtocolPhases {
	clone := maps.Clone(p)
	for name, phase := range clone {
		clone[name] = phase.Clone()
	}

	return clone
}

// PacketFields is a collection of fields in a protocol packet.
type PacketFields []PacketField

// Clone returns packet fields that do not alias the source.
func (p PacketFields) Clone() PacketFields {
	return slices.Clone(p)
}

// Packets is a collection of protocol packets.
type Packets []Packet

// Clone returns packets whose mutable fields do not alias the source.
func (p Packets) Clone() Packets {
	if p == nil {
		return nil
	}

	clone := make(Packets, len(p))
	for index := range clone {
		clone[index] = p[index].Clone()
	}

	return clone
}

// Clone returns a Protocol whose mutable fields do not alias the source.
func (p Protocol) Clone() Protocol {
	clone := p
	clone.Types = p.Types.Clone()
	clone.Phases = p.Phases.Clone()

	return clone
}

// Clone returns a ProtocolPhase whose mutable fields do not alias the source.
func (p ProtocolPhase) Clone() ProtocolPhase {
	clone := p
	clone.ToClient = p.ToClient.Clone()
	clone.ToServer = p.ToServer.Clone()

	return clone
}

// Clone returns a ProtocolDirection whose mutable fields do not alias the source.
func (p ProtocolDirection) Clone() ProtocolDirection {
	clone := p
	clone.Packets = p.Packets.Clone()

	return clone
}

// Clone returns a Packet whose mutable fields do not alias the source.
func (p Packet) Clone() Packet {
	clone := p
	clone.Fields = p.Fields.Clone()

	return clone
}
