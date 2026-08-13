package data

import "slices"

// ParticleID identifies a Minecraft particle.
type ParticleID int

// Particle describes a Minecraft particle.
type Particle struct {
	ID   ParticleID
	Name string
}

// Particles is a collection of Minecraft particles.
type Particles []Particle

// Clone returns particles that do not alias the source.
func (p Particles) Clone() Particles { return slices.Clone(p) }
