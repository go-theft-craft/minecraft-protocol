package data

// BlockRegistry provides read-only block lookup. The caller owns returned
// collections and nested reference fields and may mutate them.
type BlockRegistry interface {
	ByID(BlockID) (Block, bool)
	ByName(string) (Block, bool)
	All() Blocks
}

// ItemRegistry provides read-only item lookup. The caller owns returned
// collections and nested reference fields and may mutate them.
type ItemRegistry interface {
	ByID(ItemID) (Item, bool)
	ByName(string) (Item, bool)
	All() Items
}

// EntityRegistry provides read-only entity lookup. The caller owns returned
// collections and nested reference fields and may mutate them.
type EntityRegistry interface {
	ByID(EntityType, EntityID) (Entity, bool)
	ByName(string) Entities
	All() Entities
}

// BiomeRegistry provides read-only biome lookup. The caller owns returned
// collections and nested reference fields and may mutate them.
type BiomeRegistry interface {
	ByID(BiomeID) Biomes
	ByName(string) Biomes
	All() Biomes
}

// EffectRegistry provides read-only effect lookup. The caller owns returned
// collections and nested reference fields and may mutate them.
type EffectRegistry interface {
	ByID(EffectID) (Effect, bool)
	ByName(string) (Effect, bool)
	All() Effects
}

// EnchantmentRegistry provides read-only enchantment lookup. The caller owns
// returned collections and nested reference fields and may mutate them.
type EnchantmentRegistry interface {
	ByID(EnchantmentID) (Enchantment, bool)
	ByName(string) (Enchantment, bool)
	All() Enchantments
}

// FoodRegistry provides read-only food lookup. The caller owns returned
// collections and nested reference fields and may mutate them.
type FoodRegistry interface {
	ByID(ItemID) (Food, bool)
	ByName(string) (Food, bool)
	All() Foods
}

// ParticleRegistry provides read-only particle lookup. The caller owns returned
// collections and nested reference fields and may mutate them.
type ParticleRegistry interface {
	ByID(ParticleID) (Particle, bool)
	ByName(string) (Particle, bool)
	All() Particles
}

// InstrumentRegistry provides read-only instrument lookup. The caller owns
// returned collections and nested reference fields and may mutate them.
type InstrumentRegistry interface {
	ByID(InstrumentID) (Instrument, bool)
	ByName(string) (Instrument, bool)
	All() Instruments
}

// AttributeRegistry provides read-only attribute lookup. The caller owns
// returned collections and nested reference fields and may mutate them.
type AttributeRegistry interface {
	ByName(string) (Attribute, bool)
	ByResource(string) (Attribute, bool)
	All() Attributes
}

// WindowRegistry provides read-only window lookup. The caller owns returned
// collections and nested reference fields and may mutate them.
type WindowRegistry interface {
	ByID(WindowID) (Window, bool)
	ByName(string) (Window, bool)
	All() Windows
}

// MaterialRegistry provides read-only material lookup. The caller owns returned
// collections and nested reference fields and may mutate them.
type MaterialRegistry interface {
	ByName(string) (Material, bool)
	All() Materials
}

// RecipeRegistry provides read-only recipe lookup. The caller owns returned
// collections and nested reference fields and may mutate them.
type RecipeRegistry interface {
	ByID(ItemID) Recipes
	All() RecipeIndex
}

// LanguageRegistry provides read-only translation lookup. The caller owns
// returned collections and nested reference fields and may mutate them.
type LanguageRegistry interface {
	Get(string) (string, bool)
	All() Language
}

// SoundRegistry provides read-only sound lookup. The caller owns returned
// collections and nested reference fields and may mutate them.
type SoundRegistry interface {
	ByID(SoundID) (Sound, bool)
	ByName(string) (Sound, bool)
	All() Sounds
}

// MapIconRegistry provides read-only map-marker lookup. The caller owns
// returned collections and nested reference fields and may mutate them.
type MapIconRegistry interface {
	ByID(MapIconID) (MapIcon, bool)
	ByName(string) (MapIcon, bool)
	All() MapIcons
}

// BlockLootRegistry provides read-only block loot lookup, keyed by block name.
// The caller owns returned collections and nested reference fields and may
// mutate them.
type BlockLootRegistry interface {
	ByBlock(string) (BlockLoot, bool)
	All() BlockLootTables
}

// EntityLootRegistry provides read-only entity loot lookup, keyed by entity
// name. The caller owns returned collections and nested reference fields and
// may mutate them.
type EntityLootRegistry interface {
	ByEntity(string) (EntityLoot, bool)
	All() EntityLootTables
}
