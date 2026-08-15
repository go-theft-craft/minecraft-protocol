package data

import "slices"

// LoginPacket is the sample play-login packet upstream publishes for a
// version. It is a recorded example rather than a description of the wire
// format: the codecs describe the format, and this is one server's answer,
// useful as a fixture and as a source of registry contents.
type LoginPacket struct {
	EntityID            int32
	IsHardcore          bool
	WorldNames          []string
	MaxPlayers          int
	ViewDistance        int
	SimulationDistance  int
	ReducedDebugInfo    bool
	EnableRespawnScreen bool
	DoLimitedCrafting   bool
	WorldState          LoginWorldState
	EnforcesSecureChat  bool
	// DimensionCodec is the registry payload as upstream published it, kept as
	// JSON bytes. It is a quarter of a megabyte of registry contents that no
	// typed model here would describe more faithfully than the bytes do.
	DimensionCodec []byte
}

// Clone returns a LoginPacket whose mutable fields do not alias the source.
func (l LoginPacket) Clone() LoginPacket {
	clone := l
	clone.WorldNames = slices.Clone(l.WorldNames)
	clone.DimensionCodec = slices.Clone(l.DimensionCodec)

	return clone
}

// LoginWorldState describes the world a player logs into.
type LoginWorldState struct {
	Dimension int
	Name      string
	// HashedSeed is the seed hash as the two 32-bit halves upstream publishes.
	HashedSeed       [2]int32
	Gamemode         string
	PreviousGamemode int
	IsDebug          bool
	IsFlat           bool
	PortalCooldown   int
	SeaLevel         int
}
