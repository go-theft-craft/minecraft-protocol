package data

import (
	"fmt"
	"sort"
)

// SetOptions supplies the registries and values stored in a Set. NewSet clones
// mutable values and raw datasets. Registry implementations retain their own
// ownership contract.
type SetOptions struct {
	Blocks          BlockRegistry
	Items           ItemRegistry
	Entities        EntityRegistry
	Biomes          BiomeRegistry
	Effects         EffectRegistry
	Enchantments    EnchantmentRegistry
	Foods           FoodRegistry
	Particles       ParticleRegistry
	Instruments     InstrumentRegistry
	Attributes      AttributeRegistry
	Windows         WindowRegistry
	Materials       MaterialRegistry
	Recipes         RecipeRegistry
	Language        LanguageRegistry
	CollisionShapes CollisionShapes
	Physics         Physics
	Protocol        Protocol
	Version         Version
	Raw             []RawDataset

	// The fields below describe datasets only some versions publish. A
	// version that has no such dataset leaves the field nil or zero, and the
	// matching accessor answers with the same emptiness rather than an error:
	// "this version does not publish sounds" is a fact about the version, not
	// a failure to build the set.
	Sounds      SoundRegistry
	MapIcons    MapIconRegistry
	BlockLoot   BlockLootRegistry
	EntityLoot  EntityLootRegistry
	Commands    CommandTree
	LoginPacket LoginPacket
	Tints       Tints
}

// Set is an immutable collection of game-data registries and values.
type Set struct {
	blocks          BlockRegistry
	items           ItemRegistry
	entities        EntityRegistry
	biomes          BiomeRegistry
	effects         EffectRegistry
	enchantments    EnchantmentRegistry
	foods           FoodRegistry
	particles       ParticleRegistry
	instruments     InstrumentRegistry
	attributes      AttributeRegistry
	windows         WindowRegistry
	materials       MaterialRegistry
	recipes         RecipeRegistry
	language        LanguageRegistry
	collisionShapes CollisionShapes
	physics         Physics
	protocol        Protocol
	version         Version
	raw             map[string]RawDataset
	sounds          SoundRegistry
	mapIcons        MapIconRegistry
	blockLoot       BlockLootRegistry
	entityLoot      EntityLootRegistry
	commands        CommandTree
	loginPacket     LoginPacket
	tints           Tints
}

// NewSet creates a Set that does not retain caller-owned mutable values.
func NewSet(options SetOptions) (*Set, error) {
	raw := make(map[string]RawDataset, len(options.Raw))
	for _, dataset := range options.Raw {
		if dataset.Name == "" {
			return nil, fmt.Errorf("%w: empty name", ErrInvalidDataset)
		}
		if _, exists := raw[dataset.Name]; exists {
			return nil, fmt.Errorf("%w: duplicate name %q", ErrInvalidDataset, dataset.Name)
		}

		raw[dataset.Name] = dataset.Clone()
	}

	return &Set{
		blocks:          options.Blocks,
		items:           options.Items,
		entities:        options.Entities,
		biomes:          options.Biomes,
		effects:         options.Effects,
		enchantments:    options.Enchantments,
		foods:           options.Foods,
		particles:       options.Particles,
		instruments:     options.Instruments,
		attributes:      options.Attributes,
		windows:         options.Windows,
		materials:       options.Materials,
		recipes:         options.Recipes,
		language:        options.Language,
		collisionShapes: options.CollisionShapes.Clone(),
		physics:         options.Physics.Clone(),
		protocol:        options.Protocol.Clone(),
		version:         options.Version,
		raw:             raw,
		sounds:          options.Sounds,
		mapIcons:        options.MapIcons,
		blockLoot:       options.BlockLoot,
		entityLoot:      options.EntityLoot,
		commands:        options.Commands.Clone(),
		loginPacket:     options.LoginPacket.Clone(),
		tints:           options.Tints.Clone(),
	}, nil
}

// Blocks returns the block registry supplied to NewSet.
func (s *Set) Blocks() BlockRegistry { return s.blocks }

// Items returns the item registry supplied to NewSet.
func (s *Set) Items() ItemRegistry { return s.items }

// Entities returns the entity registry supplied to NewSet.
func (s *Set) Entities() EntityRegistry { return s.entities }

// Biomes returns the biome registry supplied to NewSet.
func (s *Set) Biomes() BiomeRegistry { return s.biomes }

// Effects returns the effect registry supplied to NewSet.
func (s *Set) Effects() EffectRegistry { return s.effects }

// Enchantments returns the enchantment registry supplied to NewSet.
func (s *Set) Enchantments() EnchantmentRegistry { return s.enchantments }

// Foods returns the food registry supplied to NewSet.
func (s *Set) Foods() FoodRegistry { return s.foods }

// Particles returns the particle registry supplied to NewSet.
func (s *Set) Particles() ParticleRegistry { return s.particles }

// Instruments returns the instrument registry supplied to NewSet.
func (s *Set) Instruments() InstrumentRegistry { return s.instruments }

// Attributes returns the attribute registry supplied to NewSet.
func (s *Set) Attributes() AttributeRegistry { return s.attributes }

// Windows returns the window registry supplied to NewSet.
func (s *Set) Windows() WindowRegistry { return s.windows }

// Materials returns the material registry supplied to NewSet.
func (s *Set) Materials() MaterialRegistry { return s.materials }

// Recipes returns the recipe registry supplied to NewSet.
func (s *Set) Recipes() RecipeRegistry { return s.recipes }

// Language returns the language registry supplied to NewSet.
func (s *Set) Language() LanguageRegistry { return s.language }

// CollisionShapes returns a clone owned by the caller.
func (s *Set) CollisionShapes() CollisionShapes { return s.collisionShapes.Clone() }

// Physics returns a clone owned by the caller.
func (s *Set) Physics() Physics { return s.physics.Clone() }

// Protocol returns a clone owned by the caller.
func (s *Set) Protocol() Protocol { return s.protocol.Clone() }

// Sounds returns the sound registry supplied to NewSet, or nil for a version
// that publishes no sounds.
func (s *Set) Sounds() SoundRegistry { return s.sounds }

// MapIcons returns the map-marker registry supplied to NewSet, or nil for a
// version that publishes none.
func (s *Set) MapIcons() MapIconRegistry { return s.mapIcons }

// BlockLoot returns the block loot registry supplied to NewSet, or nil for a
// version that publishes no loot tables.
func (s *Set) BlockLoot() BlockLootRegistry { return s.blockLoot }

// EntityLoot returns the entity loot registry supplied to NewSet, or nil for a
// version that publishes no loot tables.
func (s *Set) EntityLoot() EntityLootRegistry { return s.entityLoot }

// Commands returns a clone of the command tree owned by the caller. A version
// that publishes no command tree returns the zero value.
func (s *Set) Commands() CommandTree { return s.commands.Clone() }

// LoginPacket returns a clone of the sample login packet owned by the caller.
// A version that publishes no sample returns the zero value.
func (s *Set) LoginPacket() LoginPacket { return s.loginPacket.Clone() }

// Tints returns a clone of the tint categories owned by the caller. A version
// that publishes no tints returns nil.
func (s *Set) Tints() Tints { return s.tints.Clone() }

// Version returns the protocol version.
func (s *Set) Version() Version { return s.version }

// Raw returns a clone of a raw dataset owned by the caller.
func (s *Set) Raw(name string) (RawDataset, bool) {
	dataset, ok := s.raw[name]
	if !ok {
		return RawDataset{}, false
	}

	return dataset.Clone(), true
}

// DatasetNames returns the raw dataset names in sorted order. The caller owns
// the returned slice.
func (s *Set) DatasetNames() []string {
	names := make([]string, 0, len(s.raw))
	for name := range s.raw {
		names = append(names, name)
	}
	sort.Strings(names)

	return names
}
