package data

import (
	"errors"
	"reflect"
	"testing"
)

type fakeBlockRegistry struct{}

func (*fakeBlockRegistry) ByID(BlockID) (Block, bool)  { return Block{}, false }
func (*fakeBlockRegistry) ByName(string) (Block, bool) { return Block{}, false }
func (*fakeBlockRegistry) All() Blocks                 { return nil }

type fakeItemRegistry struct{}

func (*fakeItemRegistry) ByID(ItemID) (Item, bool)   { return Item{}, false }
func (*fakeItemRegistry) ByName(string) (Item, bool) { return Item{}, false }
func (*fakeItemRegistry) All() Items                 { return nil }

type fakeEntityRegistry struct{}

func (*fakeEntityRegistry) ByID(EntityID) (Entity, bool) { return Entity{}, false }
func (*fakeEntityRegistry) ByName(string) (Entity, bool) { return Entity{}, false }
func (*fakeEntityRegistry) All() Entities                { return nil }

type fakeBiomeRegistry struct{}

func (*fakeBiomeRegistry) ByID(BiomeID) (Biome, bool)  { return Biome{}, false }
func (*fakeBiomeRegistry) ByName(string) (Biome, bool) { return Biome{}, false }
func (*fakeBiomeRegistry) All() Biomes                 { return nil }

type fakeEffectRegistry struct{}

func (*fakeEffectRegistry) ByID(EffectID) (Effect, bool) { return Effect{}, false }
func (*fakeEffectRegistry) ByName(string) (Effect, bool) { return Effect{}, false }
func (*fakeEffectRegistry) All() Effects                 { return nil }

type fakeEnchantmentRegistry struct{}

func (*fakeEnchantmentRegistry) ByID(EnchantmentID) (Enchantment, bool) {
	return Enchantment{}, false
}
func (*fakeEnchantmentRegistry) ByName(string) (Enchantment, bool) { return Enchantment{}, false }
func (*fakeEnchantmentRegistry) All() Enchantments                 { return nil }

type fakeFoodRegistry struct{}

func (*fakeFoodRegistry) ByID(ItemID) (Food, bool)   { return Food{}, false }
func (*fakeFoodRegistry) ByName(string) (Food, bool) { return Food{}, false }
func (*fakeFoodRegistry) All() Foods                 { return nil }

type fakeParticleRegistry struct{}

func (*fakeParticleRegistry) ByID(ParticleID) (Particle, bool) { return Particle{}, false }
func (*fakeParticleRegistry) ByName(string) (Particle, bool)   { return Particle{}, false }
func (*fakeParticleRegistry) All() Particles                   { return nil }

type fakeInstrumentRegistry struct{}

func (*fakeInstrumentRegistry) ByID(InstrumentID) (Instrument, bool) {
	return Instrument{}, false
}
func (*fakeInstrumentRegistry) ByName(string) (Instrument, bool) { return Instrument{}, false }
func (*fakeInstrumentRegistry) All() Instruments                 { return nil }

type fakeAttributeRegistry struct{}

func (*fakeAttributeRegistry) ByName(string) (Attribute, bool)     { return Attribute{}, false }
func (*fakeAttributeRegistry) ByResource(string) (Attribute, bool) { return Attribute{}, false }
func (*fakeAttributeRegistry) All() Attributes                     { return nil }

type fakeWindowRegistry struct{}

func (*fakeWindowRegistry) ByID(WindowID) (Window, bool) { return Window{}, false }
func (*fakeWindowRegistry) ByName(string) (Window, bool) { return Window{}, false }
func (*fakeWindowRegistry) All() Windows                 { return nil }

type fakeMaterialRegistry struct{}

func (*fakeMaterialRegistry) ByName(string) (Material, bool) { return Material{}, false }
func (*fakeMaterialRegistry) All() Materials                 { return nil }

type fakeRecipeRegistry struct{}

func (*fakeRecipeRegistry) ByID(ItemID) Recipes { return nil }
func (*fakeRecipeRegistry) All() RecipeIndex    { return nil }

type fakeLanguageRegistry struct{}

func (*fakeLanguageRegistry) Get(string) (string, bool) { return "", false }
func (*fakeLanguageRegistry) All() Language             { return nil }

var (
	_ BlockRegistry       = (*fakeBlockRegistry)(nil)
	_ ItemRegistry        = (*fakeItemRegistry)(nil)
	_ EntityRegistry      = (*fakeEntityRegistry)(nil)
	_ BiomeRegistry       = (*fakeBiomeRegistry)(nil)
	_ EffectRegistry      = (*fakeEffectRegistry)(nil)
	_ EnchantmentRegistry = (*fakeEnchantmentRegistry)(nil)
	_ FoodRegistry        = (*fakeFoodRegistry)(nil)
	_ ParticleRegistry    = (*fakeParticleRegistry)(nil)
	_ InstrumentRegistry  = (*fakeInstrumentRegistry)(nil)
	_ AttributeRegistry   = (*fakeAttributeRegistry)(nil)
	_ WindowRegistry      = (*fakeWindowRegistry)(nil)
	_ MaterialRegistry    = (*fakeMaterialRegistry)(nil)
	_ RecipeRegistry      = (*fakeRecipeRegistry)(nil)
	_ LanguageRegistry    = (*fakeLanguageRegistry)(nil)
)

func TestRegistryInterfaces(t *testing.T) {
	t.Log("compile-time assignments verify every registry interface")
}

func TestRawDatasetOwnership(t *testing.T) {
	input := []RawDataset{
		{Name: "zeta", Path: "zeta.json", MediaType: "application/json", Data: []byte("zeta")},
		{Name: "alpha", Path: "alpha.json", MediaType: "application/json", Data: []byte("alpha")},
	}

	set, err := NewSet(SetOptions{Raw: input})
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}

	if got, want := set.DatasetNames(), []string{"alpha", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DatasetNames() = %v, want %v", got, want)
	}

	input[0].Data[0] = 'X'
	stored, ok := set.Raw("zeta")
	if !ok {
		t.Fatal("Raw(zeta) returned false")
	}
	if got, want := string(stored.Data), "zeta"; got != want {
		t.Fatalf("Raw(zeta).Data = %q, want %q", got, want)
	}

	stored.Data[0] = 'Y'
	again, ok := set.Raw("zeta")
	if !ok {
		t.Fatal("second Raw(zeta) returned false")
	}
	if got, want := string(again.Data), "zeta"; got != want {
		t.Fatalf("second Raw(zeta).Data = %q, want %q", got, want)
	}

	names := set.DatasetNames()
	names[0] = "changed"
	if got, want := set.DatasetNames(), []string{"alpha", "zeta"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("DatasetNames() after caller mutation = %v, want %v", got, want)
	}

	if _, ok := set.Raw("missing"); ok {
		t.Fatal("Raw(missing) returned true")
	}
}

func TestRawDatasetValidation(t *testing.T) {
	tests := []struct {
		name string
		raw  []RawDataset
	}{
		{name: "empty name", raw: []RawDataset{{Name: ""}}},
		{name: "duplicate name", raw: []RawDataset{{Name: "blocks"}, {Name: "blocks"}}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := NewSet(SetOptions{Raw: test.raw})
			if !errors.Is(err, ErrInvalidDataset) {
				t.Fatalf("NewSet() error = %v, want ErrInvalidDataset", err)
			}
		})
	}
}

func TestSetAccessors(t *testing.T) {
	blocks := &fakeBlockRegistry{}
	items := &fakeItemRegistry{}
	entities := &fakeEntityRegistry{}
	biomes := &fakeBiomeRegistry{}
	effects := &fakeEffectRegistry{}
	enchantments := &fakeEnchantmentRegistry{}
	foods := &fakeFoodRegistry{}
	particles := &fakeParticleRegistry{}
	instruments := &fakeInstrumentRegistry{}
	attributes := &fakeAttributeRegistry{}
	windows := &fakeWindowRegistry{}
	materials := &fakeMaterialRegistry{}
	recipes := &fakeRecipeRegistry{}
	language := &fakeLanguageRegistry{}
	shapes := CollisionShapes{
		Blocks: BlockShapeIndex{"stone": {1}},
		Shapes: BoundingBoxIndex{1: {{MinX: 1}}},
	}
	protocol := Protocol{
		Types: ProtocolTypes{"varint": "native"},
		Phases: ProtocolPhases{
			"play": {
				ToClient: ProtocolDirection{Packets: Packets{{Name: "packet", Fields: PacketFields{{Name: "field"}}}}},
			},
		},
	}
	version := Version{Protocol: 765, MinecraftVersion: "1.20.4", MajorVersion: "1.20"}

	set, err := NewSet(SetOptions{
		Blocks:          blocks,
		Items:           items,
		Entities:        entities,
		Biomes:          biomes,
		Effects:         effects,
		Enchantments:    enchantments,
		Foods:           foods,
		Particles:       particles,
		Instruments:     instruments,
		Attributes:      attributes,
		Windows:         windows,
		Materials:       materials,
		Recipes:         recipes,
		Language:        language,
		CollisionShapes: shapes,
		Protocol:        protocol,
		Version:         version,
	})
	if err != nil {
		t.Fatalf("NewSet() error = %v", err)
	}

	if set.Blocks() != blocks || set.Items() != items || set.Entities() != entities || set.Biomes() != biomes ||
		set.Effects() != effects || set.Enchantments() != enchantments || set.Foods() != foods ||
		set.Particles() != particles || set.Instruments() != instruments || set.Attributes() != attributes ||
		set.Windows() != windows || set.Materials() != materials || set.Recipes() != recipes || set.Language() != language {
		t.Fatal("typed registry accessor did not return the selected registry")
	}
	if got := set.Version(); got != version {
		t.Fatalf("Version() = %+v, want %+v", got, version)
	}

	shapes.Blocks["stone"][0] = 98
	shapes.Shapes[1][0].MinX = 98
	storedShapes := set.CollisionShapes()
	if storedShapes.Blocks["stone"][0] == 98 || storedShapes.Shapes[1][0].MinX == 98 {
		t.Fatal("NewSet retained caller-owned CollisionShapes")
	}
	returnedShapes := set.CollisionShapes()
	returnedShapes.Blocks["stone"][0] = 99
	returnedShapes.Shapes[1][0].MinX = 99
	laterShapes := set.CollisionShapes()
	if laterShapes.Blocks["stone"][0] == 99 || laterShapes.Shapes[1][0].MinX == 99 {
		t.Fatal("CollisionShapes returned mutable internal data")
	}

	protocol.Types["varint"] = "changed input"
	inputPhase := protocol.Phases["play"]
	inputPhase.ToClient.Packets[0].Fields[0].Name = "changed input field"
	protocol.Phases["play"] = inputPhase
	storedProtocol := set.Protocol()
	if storedProtocol.Types["varint"] == "changed input" || storedProtocol.Phases["play"].ToClient.Packets[0].Fields[0].Name == "changed input field" {
		t.Fatal("NewSet retained caller-owned Protocol")
	}
	returnedProtocol := set.Protocol()
	returnedProtocol.Types["varint"] = "changed output"
	phase := returnedProtocol.Phases["play"]
	phase.ToClient.Packets[0].Fields[0].Name = "changed field"
	returnedProtocol.Phases["play"] = phase
	laterProtocol := set.Protocol()
	if laterProtocol.Types["varint"] == "changed output" || laterProtocol.Phases["play"].ToClient.Packets[0].Fields[0].Name == "changed field" {
		t.Fatal("Protocol returned mutable internal data")
	}
}
