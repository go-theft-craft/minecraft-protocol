package data

import "slices"

// EffectID identifies a Minecraft status effect.
type EffectID int

// Effect describes a Minecraft status effect.
type Effect struct {
	ID          EffectID
	Name        string
	DisplayName string
	Type        string
}

// Effects is a collection of Minecraft status effects.
type Effects []Effect

// Clone returns effects that do not alias the source.
func (e Effects) Clone() Effects { return slices.Clone(e) }
