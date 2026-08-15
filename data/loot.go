package data

import "slices"

// LootDrop describes one item a loot table can yield, together with the
// conditions upstream records for it.
//
// A condition that is absent upstream is false here, because every condition
// upstream publishes is published only when it holds: silkTouch and
// noSilkTouch appear as true or not at all. BlockAge is different — age zero
// is a real growth stage — so its presence is carried separately.
type LootDrop struct {
	Item       string
	DropChance float64
	// MinStackSize and MaxStackSize bound the stack size the drop yields.
	// Either bound may be absent, which is upstream's open end rather than a
	// zero: three drops in Java 26.1 publish a range as [0, null] or
	// [null, 1]. A bound is meaningful only when its Has flag is true.
	MinStackSize    int
	HasMinStackSize bool
	MaxStackSize    int
	HasMaxStackSize bool
	// SilkTouch requires a silk-touch tool, NoSilkTouch forbids one.
	SilkTouch   bool
	NoSilkTouch bool
	// BlockAge is the growth stage the block must have reached for the drop.
	// It is meaningful only when HasBlockAge is true.
	BlockAge    int
	HasBlockAge bool
	// PlayerKill requires that a player dealt the killing blow. It appears in
	// entity loot only.
	PlayerKill bool
}

// LootDrops is a collection of loot table drops.
type LootDrops []LootDrop

// Clone returns loot drops that do not alias the source.
func (l LootDrops) Clone() LootDrops { return slices.Clone(l) }

// BlockLoot describes what breaking a block yields.
type BlockLoot struct {
	Block string
	Drops LootDrops
}

// Clone returns block loot whose mutable fields do not alias the source.
func (b BlockLoot) Clone() BlockLoot {
	clone := b
	clone.Drops = b.Drops.Clone()

	return clone
}

// BlockLootTables is a collection of block loot tables.
type BlockLootTables []BlockLoot

// Clone returns block loot tables whose mutable fields do not alias the source.
func (b BlockLootTables) Clone() BlockLootTables {
	if b == nil {
		return nil
	}

	clone := make(BlockLootTables, len(b))
	for index := range clone {
		clone[index] = b[index].Clone()
	}

	return clone
}

// EntityLoot describes what killing an entity yields.
type EntityLoot struct {
	Entity string
	Drops  LootDrops
}

// Clone returns entity loot whose mutable fields do not alias the source.
func (e EntityLoot) Clone() EntityLoot {
	clone := e
	clone.Drops = e.Drops.Clone()

	return clone
}

// EntityLootTables is a collection of entity loot tables.
type EntityLootTables []EntityLoot

// Clone returns entity loot tables whose mutable fields do not alias the source.
func (e EntityLootTables) Clone() EntityLootTables {
	if e == nil {
		return nil
	}

	clone := make(EntityLootTables, len(e))
	for index := range clone {
		clone[index] = e[index].Clone()
	}

	return clone
}
