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
	// Variations is how a block carried its variants before the flattening.
	// Java 26.1 leaves it empty.
	Variations Variations
	// DefaultState, MinStateID, MaxStateID, and States describe the block
	// states that replaced metadata variants in the flattening. Java 1.8
	// leaves them zero.
	DefaultState BlockStateID
	MinStateID   BlockStateID
	MaxStateID   BlockStateID
	States       BlockStates
}

// BlockStateID identifies one state of a Minecraft block. A block occupies the
// closed range MinStateID through MaxStateID, and DefaultState is the one it
// takes when nothing says otherwise.
type BlockStateID int

// BlockState describes one property a block state varies over.
type BlockState struct {
	Name string
	Type string
	// NumValues is how many values the property takes. It is published even
	// for properties whose Values upstream leaves implicit, such as bool.
	NumValues int
	Values    []string
}

// Clone returns a BlockState whose mutable fields do not alias the source.
func (b BlockState) Clone() BlockState {
	clone := b
	clone.Values = slices.Clone(b.Values)

	return clone
}

// BlockStates is a collection of block state properties.
type BlockStates []BlockState

// Clone returns block states whose mutable fields do not alias the source.
func (b BlockStates) Clone() BlockStates {
	if b == nil {
		return nil
	}

	clone := make(BlockStates, len(b))
	for index := range clone {
		clone[index] = b[index].Clone()
	}

	return clone
}

// Drop describes an item dropped by a block. MinCount and MaxCount retain the
// source values only when HasMinCount or HasMaxCount is true. The generated
// data does not infer defaults for omitted counts.
type Drop struct {
	ID          ItemID
	Metadata    Metadata
	MinCount    float64
	MaxCount    float64
	HasMinCount bool
	HasMaxCount bool
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
	clone.States = b.States.Clone()

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
