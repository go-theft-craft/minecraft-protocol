package v1_8

import (
	"reflect"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/data"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

func TestVersionAndRegistration(t *testing.T) {
	if got, want := Version(), (protocol.Version{Name: "1.8.9", Protocol: 47}); got != want {
		t.Fatalf("Version() = %+v, want %+v", got, want)
	}
	set, err := data.Load("java/1.8.9")
	if err != nil {
		t.Fatalf("data.Load() error = %v", err)
	}
	if got, want := set.Version(), (data.Version{Protocol: 47, MinecraftVersion: "1.8.8", MajorVersion: "1.8"}); got != want {
		t.Fatalf("Set.Version() = %+v, want %+v", got, want)
	}
	if MetadataEnd != 0x7F {
		t.Fatalf("MetadataEnd = %#x, want 0x7f", MetadataEnd)
	}
}

func TestRegistryCounts(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{
		"attributes": len(set.Attributes().All()), "biomes": len(set.Biomes().All()),
		"blocks": len(set.Blocks().All()), "effects": len(set.Effects().All()),
		"enchantments": len(set.Enchantments().All()), "entities": len(set.Entities().All()),
		"foods": len(set.Foods().All()), "instruments": len(set.Instruments().All()),
		"items": len(set.Items().All()), "materials": len(set.Materials().All()),
		"particles": len(set.Particles().All()), "windows": len(set.Windows().All()),
	}
	want := map[string]int{"blocks": 198, "items": 336, "entities": 58, "biomes": 62, "effects": 23, "enchantments": 25, "foods": 28, "particles": 42, "instruments": 5, "attributes": 12, "windows": 14, "materials": 8}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("registry counts = %v, want %v", counts, want)
	}
}

func TestCallerOwnedRegistryResults(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatal(err)
	}
	stone, ok := set.Blocks().ByID(1)
	if !ok || stone.Name != "stone" {
		t.Fatalf("block 1 = %+v, %v", stone, ok)
	}
	stone.Name = "changed"
	stone.HarvestTools[257] = false
	stone.Variations[0].DisplayName = "changed"
	stoneAgain, _ := set.Blocks().ByName("stone")
	if stoneAgain.Name != "stone" || !stoneAgain.HarvestTools[257] || stoneAgain.Variations[0].DisplayName == "changed" {
		t.Fatal("block lookup returned aliased data")
	}
	blocks := set.Blocks().All()
	blocks[1].Name = "changed"
	if current, _ := set.Blocks().ByID(1); current.Name != "stone" {
		t.Fatal("Blocks.All returned aliased data")
	}

	sword, ok := set.Items().ByID(276)
	if !ok || sword.Name != "diamond_sword" {
		t.Fatalf("item 276 = %+v, %v", sword, ok)
	}
	sword.RepairWith[0] = "changed"
	if current, _ := set.Items().ByName("diamond_sword"); current.RepairWith[0] == "changed" {
		t.Fatal("item lookup returned aliased data")
	}
}

func TestEntityRegistryPreservesTypedIDAndNameCollisions(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatal(err)
	}

	mob, ok := set.Entities().ByID(data.EntityTypeMob, 50)
	if !ok || mob.Name != "Creeper" {
		t.Fatalf("mob entity 50 = %+v, %v", mob, ok)
	}
	object, ok := set.Entities().ByID(data.EntityTypeObject, 50)
	if !ok || object.Name != "PrimedTnt" {
		t.Fatalf("object entity 50 = %+v, %v", object, ok)
	}

	for _, entity := range set.Entities().All() {
		byID, found := set.Entities().ByID(entity.Type, entity.ID)
		if !found || !reflect.DeepEqual(byID, entity) {
			t.Fatalf("entity %+v did not round-trip by typed ID: %+v, %v", entity, byID, found)
		}
		byName := set.Entities().ByName(entity.Name)
		if !containsEntity(byName, entity) {
			t.Fatalf("entity %+v missing from name lookup: %+v", entity, byName)
		}
	}

	minecarts := set.Entities().ByName("MinecartRideable")
	if len(minecarts) != 3 {
		t.Fatalf("MinecartRideable lookup returned %d entities, want 3", len(minecarts))
	}
	minecarts[0].Name = "changed"
	if current := set.Entities().ByName("MinecartRideable"); current[0].Name == "changed" {
		t.Fatal("entity name lookup returned aliased data")
	}
}

func TestBiomeRegistryPreservesDuplicateSourceRows(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatal(err)
	}

	byID := set.Biomes().ByID(161)
	if len(byID) != 2 || byID[0].DisplayName != "Mega Taiga Hills M" || byID[1].DisplayName != "Redwood Taiga Hills M" {
		t.Fatalf("biome 161 lookup = %+v", byID)
	}
	byName := set.Biomes().ByName("giant_spruce_taiga_hills")
	if !reflect.DeepEqual(byName, byID) {
		t.Fatalf("biome name lookup = %+v, want %+v", byName, byID)
	}

	for _, biome := range set.Biomes().All() {
		if !containsBiome(set.Biomes().ByID(biome.ID), biome) {
			t.Fatalf("biome %+v missing from ID lookup", biome)
		}
		if !containsBiome(set.Biomes().ByName(biome.Name), biome) {
			t.Fatalf("biome %+v missing from name lookup", biome)
		}
	}

	byID[0].DisplayName = "changed"
	if current := set.Biomes().ByID(161); current[0].DisplayName == "changed" {
		t.Fatal("biome lookup returned aliased data")
	}
}

func TestBlockDropsPreserveFractionalCountsAndOmission(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatal(err)
	}

	stone, ok := set.Blocks().ByName("stone")
	if !ok || len(stone.Drops) != 1 {
		t.Fatalf("stone = %+v, %v", stone, ok)
	}
	if stone.Drops[0].HasMinCount || stone.Drops[0].HasMaxCount {
		t.Fatalf("stone drop counts must remain omitted: %+v", stone.Drops[0])
	}
	leaves, ok := set.Blocks().ByID(18)
	if !ok || len(leaves.Drops) == 0 || !leaves.Drops[0].HasMinCount || leaves.Drops[0].MinCount != 0 {
		t.Fatalf("leaves explicit zero minimum was not preserved: %+v, %v", leaves, ok)
	}

	gravel, ok := set.Blocks().ByName("gravel")
	if !ok || len(gravel.Drops) != 2 {
		t.Fatalf("gravel = %+v, %v", gravel, ok)
	}
	if !gravel.Drops[0].HasMinCount || gravel.Drops[0].MinCount != 0.9 || gravel.Drops[0].HasMaxCount {
		t.Fatalf("first gravel drop = %+v", gravel.Drops[0])
	}
	if !gravel.Drops[1].HasMinCount || gravel.Drops[1].MinCount != 0.1 || gravel.Drops[1].HasMaxCount {
		t.Fatalf("second gravel drop = %+v", gravel.Drops[1])
	}
}

func TestCakeRecipePreservesReturnedBucketsAndNullCells(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatal(err)
	}

	recipes := set.Recipes().ByID(354)
	if len(recipes) != 1 {
		t.Fatalf("cake recipes = %d, want 1", len(recipes))
	}
	out := recipes[0].OutShape
	if len(out) != 3 || len(out[0]) != 3 || len(out[1]) != 3 || len(out[2]) != 3 {
		t.Fatalf("cake output shape dimensions = %+v", out)
	}
	for column, cell := range out[0] {
		if cell.Ingredient == nil || cell.Ingredient.ID != 325 || cell.Ingredient.Metadata != -1 {
			t.Fatalf("cake output cell 0,%d = %+v", column, cell)
		}
	}
	for row := 1; row < len(out); row++ {
		for column, cell := range out[row] {
			if cell.Ingredient != nil {
				t.Fatalf("cake output cell %d,%d = %+v, want null", row, column, cell)
			}
		}
	}

	out[0][0].Ingredient.ID = 1
	out[1][0].Ingredient = &data.Ingredient{ID: 1}
	again := set.Recipes().ByID(354)[0].OutShape
	if again[0][0].Ingredient.ID != 325 || again[1][0].Ingredient != nil {
		t.Fatal("recipe output shape lookup returned aliased data")
	}
}

func TestCallerOwnedCompositeValues(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatal(err)
	}

	shapes := set.CollisionShapes()
	shapes.Blocks["stone"][0] = 999
	if set.CollisionShapes().Blocks["stone"][0] == 999 {
		t.Fatal("collision shapes alias internal data")
	}
	recipes := set.Recipes().All()
	for id, entries := range recipes {
		if len(entries) > 0 {
			entries[0].Result.ID = 999
			delete(recipes, id)
			if got := set.Recipes().ByID(id); len(got) == 0 || got[0].Result.ID == 999 {
				t.Fatal("recipes alias internal data")
			}
			break
		}
	}
	language := set.Language().All()
	for key := range language {
		language[key] = "changed"
		if value, _ := set.Language().Get(key); value == "changed" {
			t.Fatal("language aliases internal data")
		}
		break
	}
	dataProtocol := set.Protocol()
	dataProtocol.Types["varint"] = "changed"
	if set.Protocol().Types["varint"] == "changed" {
		t.Fatal("protocol aliases internal data")
	}
}

func TestDataCallsAreIndependent(t *testing.T) {
	first, err := Data()
	if err != nil {
		t.Fatal(err)
	}
	second, err := Data()
	if err != nil {
		t.Fatal(err)
	}
	if first == second || first.Blocks() == second.Blocks() {
		t.Fatal("Data calls did not return distinct sets and registries")
	}
	blocks := first.Blocks().All()
	blocks[1].Name = "changed"
	if current, _ := second.Blocks().ByID(1); current.Name != "stone" {
		t.Fatal("Data calls share registry values")
	}
}

func TestChatPacketValues(t *testing.T) {
	var _ java.PacketValue = ChatCB{}
	var _ java.PacketValue = ChatSB{}
	if got := (ChatCB{}).PacketID(); got != 0x02 {
		t.Fatalf("ChatCB.PacketID() = %#x", got)
	}
	if got := (ChatSB{}).PacketID(); got != 0x01 {
		t.Fatalf("ChatSB.PacketID() = %#x", got)
	}

	clientType := reflect.TypeFor[ChatCB]()
	serverType := reflect.TypeFor[ChatSB]()
	if got := clientType.Field(0).Tag.Get("mc"); got != "string" {
		t.Fatalf("ChatCB field 0 tag = %q", got)
	}
	if got := clientType.Field(1).Tag.Get("mc"); got != "i8" {
		t.Fatalf("ChatCB field 1 tag = %q", got)
	}
	if got := serverType.Field(0).Tag.Get("mc"); got != "string" {
		t.Fatalf("ChatSB field 0 tag = %q", got)
	}
}

func containsEntity(entities data.Entities, want data.Entity) bool {
	for _, entity := range entities {
		if reflect.DeepEqual(entity, want) {
			return true
		}
	}
	return false
}

func containsBiome(biomes data.Biomes, want data.Biome) bool {
	for _, biome := range biomes {
		if reflect.DeepEqual(biome, want) {
			return true
		}
	}
	return false
}
