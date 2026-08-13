package data

import "slices"

// BiomeID identifies a Minecraft biome.
type BiomeID int

// Biome describes a Minecraft biome.
type Biome struct {
	ID            BiomeID
	Name          string
	NameLegacy    string
	DisplayName   string
	Category      string
	Temperature   float64
	Precipitation string
	Depth         float64
	Dimension     string
	Color         int
	Rainfall      float64
}

// Biomes is a collection of Minecraft biomes.
type Biomes []Biome

// Clone returns biomes that do not alias the source.
func (b Biomes) Clone() Biomes { return slices.Clone(b) }
