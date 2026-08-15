package data

import "slices"

// SoundID identifies a Minecraft sound event on the wire.
type SoundID int

// Sound describes a Minecraft sound event.
type Sound struct {
	ID   SoundID
	Name string
}

// Sounds is a collection of Minecraft sound events.
type Sounds []Sound

// Clone returns sounds that do not alias the source.
func (s Sounds) Clone() Sounds { return slices.Clone(s) }
