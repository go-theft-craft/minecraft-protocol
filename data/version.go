package data

import "maps"

// ProtocolNumber identifies a Minecraft wire protocol version.
type ProtocolNumber int32

// Version describes a Minecraft protocol version.
type Version struct {
	Protocol         ProtocolNumber
	MinecraftVersion string
	MajorVersion     string
}

// Language maps language keys to translated strings.
type Language map[string]string

// Clone returns a language map that does not alias the source.
func (l Language) Clone() Language { return maps.Clone(l) }
