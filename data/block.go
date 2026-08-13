package data

import (
	"maps"
	"slices"
)

// BlockID identifies a Minecraft block.
type BlockID int

// ItemID identifies a Minecraft item.
type ItemID int

// Metadata identifies a Minecraft item or block variant.
type Metadata int

// Block describes a Minecraft block.
type Block struct {
	ID           BlockID
	Name         string
	DisplayName  string
	Hardness     *float64
	StackSize    int
	Diggable     bool
	BoundingBox  string
	Material     string
	Transparent  bool
	EmitLight    int
	FilterLight  int
	Resistance   float64
	Drops        Drops
	HarvestTools HarvestToolSet
	Variations   Variations
}

// Drop describes an item dropped by a block.
type Drop struct {
	ID       ItemID
	Metadata Metadata
	MinCount int
	MaxCount int
}

// Variation describes a metadata variant of a game-data value.
type Variation struct {
	Metadata    Metadata
	DisplayName string
}

// Clone returns a Block whose mutable fields do not alias the source.
func (b Block) Clone() Block {
	clone := b
	if b.Hardness != nil {
		hardness := *b.Hardness
		clone.Hardness = &hardness
	}
	clone.Drops = b.Drops.Clone()
	clone.HarvestTools = b.HarvestTools.Clone()
	clone.Variations = b.Variations.Clone()

	return clone
}

// Drops is a collection of block drops.
type Drops []Drop

// Clone returns drops that do not alias the source.
func (d Drops) Clone() Drops { return slices.Clone(d) }

// Variations is a collection of metadata variants.
type Variations []Variation

// Clone returns variations that do not alias the source.
func (v Variations) Clone() Variations { return slices.Clone(v) }

// HarvestToolSet maps item IDs to their block-harvesting capability.
type HarvestToolSet map[ItemID]bool

// Clone returns a harvest-tool set that does not alias the source.
func (h HarvestToolSet) Clone() HarvestToolSet { return maps.Clone(h) }

// Blocks is a collection of Minecraft blocks.
type Blocks []Block

// Clone returns blocks whose mutable fields do not alias the source.
func (b Blocks) Clone() Blocks {
	if b == nil {
		return nil
	}

	clone := make(Blocks, len(b))
	for index := range clone {
		clone[index] = b[index].Clone()
	}

	return clone
}
