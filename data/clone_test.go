package data

import "testing"

func TestMutableCloneIsolation(t *testing.T) {
	tests := []struct {
		name string
		test func(*testing.T)
	}{
		{
			name: "block",
			test: func(t *testing.T) {
				source := Block{
					Hardness:     float64Pointer(1.5),
					Drops:        Drops{{ID: 1}},
					HarvestTools: HarvestToolSet{257: true},
					Variations:   Variations{{Metadata: 1}},
				}
				clone := source.Clone()

				*clone.Hardness = 2
				clone.Drops[0].ID = 2
				clone.HarvestTools[257] = false
				clone.Variations[0].Metadata = 2

				if *source.Hardness != 1.5 || source.Drops[0].ID != 1 || !source.HarvestTools[257] || source.Variations[0].Metadata != 1 {
					t.Fatal("Block clone modified source")
				}
			},
		},
		{
			name: "item",
			test: func(t *testing.T) {
				source := Item{
					EnchantCategories: []string{"armor"},
					RepairWith:        []string{"iron_ingot"},
					Variations:        Variations{{Metadata: 1}},
				}
				clone := source.Clone()

				clone.EnchantCategories[0] = "weapon"
				clone.RepairWith[0] = "gold_ingot"
				clone.Variations[0].Metadata = 2

				if source.EnchantCategories[0] != "armor" || source.RepairWith[0] != "iron_ingot" || source.Variations[0].Metadata != 1 {
					t.Fatal("Item clone modified source")
				}
			},
		},
		{
			name: "entity",
			test: func(t *testing.T) {
				source := Entity{Width: float64Pointer(0.6), Height: float64Pointer(1.8)}
				clone := source.Clone()

				*clone.Width = 1
				*clone.Height = 2

				if *source.Width != 0.6 || *source.Height != 1.8 {
					t.Fatal("Entity clone modified source")
				}
			},
		},
		{
			name: "enchantment",
			test: func(t *testing.T) {
				source := Enchantment{Exclude: []string{"sharpness"}}
				clone := source.Clone()

				clone.Exclude[0] = "smite"

				if source.Exclude[0] != "sharpness" {
					t.Fatal("Enchantment clone modified source")
				}
			},
		},
		{
			name: "food",
			test: func(t *testing.T) {
				source := Food{Variations: Variations{{Metadata: 1}}}
				clone := source.Clone()

				clone.Variations[0].Metadata = 2

				if source.Variations[0].Metadata != 1 {
					t.Fatal("Food clone modified source")
				}
			},
		},
		{
			name: "window",
			test: func(t *testing.T) {
				source := Window{
					Slots:      []WindowSlot{{Name: "input"}},
					Properties: []string{"fuel"},
					OpenedWith: []WindowOpener{{Type: "block", ID: 1}},
				}
				clone := source.Clone()

				clone.Slots[0].Name = "output"
				clone.Properties[0] = "progress"
				clone.OpenedWith[0].ID = 2

				if source.Slots[0].Name != "input" || source.Properties[0] != "fuel" || source.OpenedWith[0].ID != 1 {
					t.Fatal("Window clone modified source")
				}
			},
		},
		{
			name: "material",
			test: func(t *testing.T) {
				source := Material{ToolSpeeds: ToolSpeedIndex{1: 1.5}}
				clone := source.Clone()

				clone.ToolSpeeds[1] = 2

				if source.ToolSpeeds[1] != 1.5 {
					t.Fatal("Material clone modified source")
				}
			},
		},
		{
			name: "recipe",
			test: func(t *testing.T) {
				source := Recipe{
					Ingredients: RecipeIngredients{{ID: 1}},
					InShape: RecipeShape{
						RecipeIngredients{{ID: 2}},
						RecipeIngredients{{ID: 3}},
					},
				}
				clone := source.Clone()

				clone.Ingredients[0].ID = 4
				clone.InShape[0][0].ID = 4
				clone.InShape[1] = RecipeIngredients{{ID: 5}}

				if source.Ingredients[0].ID != 1 || source.InShape[0][0].ID != 2 || source.InShape[1][0].ID != 3 {
					t.Fatal("Recipe clone modified source")
				}
			},
		},
		{
			name: "collision shapes",
			test: func(t *testing.T) {
				source := CollisionShapes{
					Blocks: BlockShapeIndex{
						"stone": ShapeIDs{1},
						"dirt":  ShapeIDs{2},
					},
					Shapes: BoundingBoxIndex{
						1: BoundingBoxes{{MinX: 1}},
						2: BoundingBoxes{{MinX: 2}},
					},
				}
				clone := source.Clone()

				clone.Blocks["stone"][0] = 2
				clone.Blocks["dirt"][0] = 3
				clone.Blocks["grass"] = ShapeIDs{4}
				clone.Shapes[1][0].MinX = 2
				clone.Shapes[2][0].MinX = 3
				clone.Shapes[3] = BoundingBoxes{{MinX: 4}}

				if source.Blocks["stone"][0] != 1 || source.Blocks["dirt"][0] != 2 || len(source.Blocks) != 2 || source.Shapes[1][0].MinX != 1 || source.Shapes[2][0].MinX != 2 || len(source.Shapes) != 2 {
					t.Fatal("CollisionShapes clone modified source")
				}
			},
		},
		{
			name: "protocol",
			test: func(t *testing.T) {
				source := Protocol{
					Types: ProtocolTypes{"varint": "native"},
					Phases: ProtocolPhases{
						"play": {
							ToClient: ProtocolDirection{Packets: Packets{{Name: "client_packet", Fields: PacketFields{{Name: "client_field"}}}}},
							ToServer: ProtocolDirection{Packets: Packets{{Name: "server_packet", Fields: PacketFields{{Name: "server_field"}}}}},
						},
					},
				}
				clone := source.Clone()

				clone.Types["varint"] = "changed"
				clone.Phases["status"] = ProtocolPhase{}
				phase := clone.Phases["play"]
				phase.ToClient.Packets[0].Name = "changed_client_packet"
				phase.ToClient.Packets[0].Fields[0].Name = "changed_client_field"
				phase.ToServer.Packets[0].Name = "changed_server_packet"
				phase.ToServer.Packets[0].Fields[0].Name = "changed_server_field"
				clone.Phases["play"] = phase

				sourcePhase := source.Phases["play"]
				if source.Types["varint"] != "native" || len(source.Phases) != 1 || sourcePhase.ToClient.Packets[0].Name != "client_packet" || sourcePhase.ToClient.Packets[0].Fields[0].Name != "client_field" || sourcePhase.ToServer.Packets[0].Name != "server_packet" || sourcePhase.ToServer.Packets[0].Fields[0].Name != "server_field" {
					t.Fatal("Protocol clone modified source")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.test)
	}
}

func TestMutableClonePreservesNil(t *testing.T) {
	tests := []struct {
		name string
		test func(*testing.T)
	}{
		{
			name: "block",
			test: func(t *testing.T) {
				clone := (Block{}).Clone()
				if clone.Hardness != nil || clone.Drops != nil || clone.HarvestTools != nil || clone.Variations != nil {
					t.Fatal("Block clone did not preserve nil fields")
				}
			},
		},
		{
			name: "item",
			test: func(t *testing.T) {
				clone := (Item{}).Clone()
				if clone.EnchantCategories != nil || clone.RepairWith != nil || clone.Variations != nil {
					t.Fatal("Item clone did not preserve nil fields")
				}
			},
		},
		{
			name: "entity",
			test: func(t *testing.T) {
				clone := (Entity{}).Clone()
				if clone.Width != nil || clone.Height != nil {
					t.Fatal("Entity clone did not preserve nil fields")
				}
			},
		},
		{
			name: "enchantment",
			test: func(t *testing.T) {
				if clone := (Enchantment{}).Clone(); clone.Exclude != nil {
					t.Fatal("Enchantment clone did not preserve nil fields")
				}
			},
		},
		{
			name: "food",
			test: func(t *testing.T) {
				if clone := (Food{}).Clone(); clone.Variations != nil {
					t.Fatal("Food clone did not preserve nil fields")
				}
			},
		},
		{
			name: "window",
			test: func(t *testing.T) {
				clone := (Window{}).Clone()
				if clone.Slots != nil || clone.Properties != nil || clone.OpenedWith != nil {
					t.Fatal("Window clone did not preserve nil fields")
				}
			},
		},
		{
			name: "material",
			test: func(t *testing.T) {
				if clone := (Material{}).Clone(); clone.ToolSpeeds != nil {
					t.Fatal("Material clone did not preserve nil fields")
				}
			},
		},
		{
			name: "recipe",
			test: func(t *testing.T) {
				clone := (Recipe{}).Clone()
				if clone.Ingredients != nil || clone.InShape != nil {
					t.Fatal("Recipe clone did not preserve nil fields")
				}
			},
		},
		{
			name: "collision shapes",
			test: func(t *testing.T) {
				clone := (CollisionShapes{}).Clone()
				if clone.Blocks != nil || clone.Shapes != nil {
					t.Fatal("CollisionShapes clone did not preserve nil fields")
				}
			},
		},
		{
			name: "protocol",
			test: func(t *testing.T) {
				clone := (Protocol{}).Clone()
				if clone.Types != nil || clone.Phases != nil {
					t.Fatal("Protocol clone did not preserve nil fields")
				}

				directionClone := (ProtocolDirection{}).Clone()
				if directionClone.Packets != nil {
					t.Fatal("ProtocolDirection clone did not preserve nil packets")
				}

				packetClone := (Packet{}).Clone()
				if packetClone.Fields != nil {
					t.Fatal("Packet clone did not preserve nil fields")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.test)
	}
}

func TestNamedCollectionCloneIsolation(t *testing.T) {
	tests := []struct {
		name string
		test func(*testing.T)
	}{
		{
			name: "packet fields",
			test: func(t *testing.T) {
				source := PacketFields{{Name: "field"}}
				clone := source.Clone()
				clone[0].Name = "changed"
				if source[0].Name != "field" {
					t.Fatal("PacketFields clone modified source")
				}
			},
		},
		{
			name: "packets",
			test: func(t *testing.T) {
				source := Packets{{Fields: PacketFields{{Name: "field"}}}}
				clone := source.Clone()
				clone[0].Fields[0].Name = "changed"
				if source[0].Fields[0].Name != "field" {
					t.Fatal("Packets clone modified source")
				}
			},
		},
		{
			name: "recipe ingredients",
			test: func(t *testing.T) {
				source := RecipeIngredients{{ID: 1}}
				clone := source.Clone()
				clone[0].ID = 2
				if source[0].ID != 1 {
					t.Fatal("RecipeIngredients clone modified source")
				}
			},
		},
		{
			name: "recipe shape",
			test: func(t *testing.T) {
				source := RecipeShape{RecipeIngredients{{ID: 1}}}
				clone := source.Clone()
				clone[0][0].ID = 2
				if source[0][0].ID != 1 {
					t.Fatal("RecipeShape clone modified source")
				}
			},
		},
		{
			name: "recipes",
			test: func(t *testing.T) {
				source := Recipes{{Ingredients: RecipeIngredients{{ID: 1}}}}
				clone := source.Clone()
				clone[0].Ingredients[0].ID = 2
				if source[0].Ingredients[0].ID != 1 {
					t.Fatal("Recipes clone modified source")
				}
			},
		},
		{
			name: "recipe index",
			test: func(t *testing.T) {
				source := RecipeIndex{1: {{Ingredients: RecipeIngredients{{ID: 1}}}}}
				clone := source.Clone()
				clone[1][0].Ingredients[0].ID = 2
				if source[1][0].Ingredients[0].ID != 1 {
					t.Fatal("RecipeIndex clone modified source")
				}
			},
		},
		{
			name: "drops",
			test: func(t *testing.T) {
				source := Drops{{ID: 1}}
				clone := source.Clone()
				clone[0].ID = 2
				if source[0].ID != 1 {
					t.Fatal("Drops clone modified source")
				}
			},
		},
		{
			name: "variations",
			test: func(t *testing.T) {
				source := Variations{{Metadata: 1}}
				clone := source.Clone()
				clone[0].Metadata = 2
				if source[0].Metadata != 1 {
					t.Fatal("Variations clone modified source")
				}
			},
		},
		{
			name: "harvest tool set",
			test: func(t *testing.T) {
				source := HarvestToolSet{1: true}
				clone := source.Clone()
				clone[1] = false
				if !source[1] {
					t.Fatal("HarvestToolSet clone modified source")
				}
			},
		},
		{
			name: "blocks",
			test: func(t *testing.T) {
				source := Blocks{{Variations: Variations{{Metadata: 1}}}}
				clone := source.Clone()
				clone[0].Variations[0].Metadata = 2
				if source[0].Variations[0].Metadata != 1 {
					t.Fatal("Blocks clone modified source")
				}
			},
		},
		{
			name: "items",
			test: func(t *testing.T) {
				source := Items{{Variations: Variations{{Metadata: 1}}}}
				clone := source.Clone()
				clone[0].Variations[0].Metadata = 2
				if source[0].Variations[0].Metadata != 1 {
					t.Fatal("Items clone modified source")
				}
			},
		},
		{
			name: "entities",
			test: func(t *testing.T) {
				source := Entities{{Width: float64Pointer(1)}}
				clone := source.Clone()
				*clone[0].Width = 2
				if *source[0].Width != 1 {
					t.Fatal("Entities clone modified source")
				}
			},
		},
		{
			name: "biomes",
			test: func(t *testing.T) {
				source := Biomes{{Name: "plains"}}
				clone := source.Clone()
				clone[0].Name = "desert"
				if source[0].Name != "plains" {
					t.Fatal("Biomes clone modified source")
				}
			},
		},
		{
			name: "effects",
			test: func(t *testing.T) {
				source := Effects{{Name: "speed"}}
				clone := source.Clone()
				clone[0].Name = "slowness"
				if source[0].Name != "speed" {
					t.Fatal("Effects clone modified source")
				}
			},
		},
		{
			name: "enchantments",
			test: func(t *testing.T) {
				source := Enchantments{{Exclude: []string{"sharpness"}}}
				clone := source.Clone()
				clone[0].Exclude[0] = "smite"
				if source[0].Exclude[0] != "sharpness" {
					t.Fatal("Enchantments clone modified source")
				}
			},
		},
		{
			name: "foods",
			test: func(t *testing.T) {
				source := Foods{{Variations: Variations{{Metadata: 1}}}}
				clone := source.Clone()
				clone[0].Variations[0].Metadata = 2
				if source[0].Variations[0].Metadata != 1 {
					t.Fatal("Foods clone modified source")
				}
			},
		},
		{
			name: "particles",
			test: func(t *testing.T) {
				source := Particles{{Name: "smoke"}}
				clone := source.Clone()
				clone[0].Name = "flame"
				if source[0].Name != "smoke" {
					t.Fatal("Particles clone modified source")
				}
			},
		},
		{
			name: "instruments",
			test: func(t *testing.T) {
				source := Instruments{{Name: "harp"}}
				clone := source.Clone()
				clone[0].Name = "bass"
				if source[0].Name != "harp" {
					t.Fatal("Instruments clone modified source")
				}
			},
		},
		{
			name: "attributes",
			test: func(t *testing.T) {
				source := Attributes{{Name: "health"}}
				clone := source.Clone()
				clone[0].Name = "speed"
				if source[0].Name != "health" {
					t.Fatal("Attributes clone modified source")
				}
			},
		},
		{
			name: "windows",
			test: func(t *testing.T) {
				source := Windows{{Slots: []WindowSlot{{Name: "input"}}}}
				clone := source.Clone()
				clone[0].Slots[0].Name = "output"
				if source[0].Slots[0].Name != "input" {
					t.Fatal("Windows clone modified source")
				}
			},
		},
		{
			name: "materials",
			test: func(t *testing.T) {
				source := Materials{{ToolSpeeds: ToolSpeedIndex{1: 1}}}
				clone := source.Clone()
				clone[0].ToolSpeeds[1] = 2
				if source[0].ToolSpeeds[1] != 1 {
					t.Fatal("Materials clone modified source")
				}
			},
		},
		{
			name: "tool speed index",
			test: func(t *testing.T) {
				source := ToolSpeedIndex{1: 1}
				clone := source.Clone()
				clone[1] = 2
				if source[1] != 1 {
					t.Fatal("ToolSpeedIndex clone modified source")
				}
			},
		},
		{
			name: "protocol types",
			test: func(t *testing.T) {
				source := ProtocolTypes{"varint": "native"}
				clone := source.Clone()
				clone["varint"] = "changed"
				if source["varint"] != "native" {
					t.Fatal("ProtocolTypes clone modified source")
				}
			},
		},
		{
			name: "protocol phases",
			test: func(t *testing.T) {
				source := ProtocolPhases{"play": {ToClient: ProtocolDirection{Packets: Packets{{Fields: PacketFields{{Name: "field"}}}}}}}
				clone := source.Clone()
				phase := clone["play"]
				phase.ToClient.Packets[0].Fields[0].Name = "changed"
				clone["play"] = phase
				if source["play"].ToClient.Packets[0].Fields[0].Name != "field" {
					t.Fatal("ProtocolPhases clone modified source")
				}
			},
		},
		{
			name: "shape IDs",
			test: func(t *testing.T) {
				source := ShapeIDs{1}
				clone := source.Clone()
				clone[0] = 2
				if source[0] != 1 {
					t.Fatal("ShapeIDs clone modified source")
				}
			},
		},
		{
			name: "bounding boxes",
			test: func(t *testing.T) {
				source := BoundingBoxes{{MinX: 1}}
				clone := source.Clone()
				clone[0].MinX = 2
				if source[0].MinX != 1 {
					t.Fatal("BoundingBoxes clone modified source")
				}
			},
		},
		{
			name: "language",
			test: func(t *testing.T) {
				source := Language{"item.example": "Example"}
				clone := source.Clone()
				clone["item.example"] = "Changed"
				if source["item.example"] != "Example" {
					t.Fatal("Language clone modified source")
				}
			},
		},
		{
			name: "block shape index",
			test: func(t *testing.T) {
				source := BlockShapeIndex{"stone": {1}}
				clone := source.Clone()
				clone["stone"][0] = 2
				if source["stone"][0] != 1 {
					t.Fatal("BlockShapeIndex clone modified source")
				}
			},
		},
		{
			name: "bounding box index",
			test: func(t *testing.T) {
				source := BoundingBoxIndex{1: {{MinX: 1}}}
				clone := source.Clone()
				clone[1][0].MinX = 2
				if source[1][0].MinX != 1 {
					t.Fatal("BoundingBoxIndex clone modified source")
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, test.test)
	}
}

func TestNamedCollectionClonePreservesNil(t *testing.T) {
	if PacketFields(nil).Clone() != nil {
		t.Fatal("PacketFields clone did not preserve nil")
	}
	if Packets(nil).Clone() != nil {
		t.Fatal("Packets clone did not preserve nil")
	}
	if Drops(nil).Clone() != nil {
		t.Fatal("Drops clone did not preserve nil")
	}
	if Variations(nil).Clone() != nil {
		t.Fatal("Variations clone did not preserve nil")
	}
	if RecipeIngredients(nil).Clone() != nil {
		t.Fatal("RecipeIngredients clone did not preserve nil")
	}
	if RecipeShape(nil).Clone() != nil {
		t.Fatal("RecipeShape clone did not preserve nil")
	}
	if Recipes(nil).Clone() != nil {
		t.Fatal("Recipes clone did not preserve nil")
	}
	if RecipeIndex(nil).Clone() != nil {
		t.Fatal("RecipeIndex clone did not preserve nil")
	}
	if ShapeIDs(nil).Clone() != nil {
		t.Fatal("ShapeIDs clone did not preserve nil")
	}
	if BoundingBoxes(nil).Clone() != nil {
		t.Fatal("BoundingBoxes clone did not preserve nil")
	}
	if BlockShapeIndex(nil).Clone() != nil {
		t.Fatal("BlockShapeIndex clone did not preserve nil")
	}
	if BoundingBoxIndex(nil).Clone() != nil {
		t.Fatal("BoundingBoxIndex clone did not preserve nil")
	}
	if HarvestToolSet(nil).Clone() != nil {
		t.Fatal("HarvestToolSet clone did not preserve nil")
	}
	if ToolSpeedIndex(nil).Clone() != nil {
		t.Fatal("ToolSpeedIndex clone did not preserve nil")
	}
	if ProtocolTypes(nil).Clone() != nil {
		t.Fatal("ProtocolTypes clone did not preserve nil")
	}
	if ProtocolPhases(nil).Clone() != nil {
		t.Fatal("ProtocolPhases clone did not preserve nil")
	}
	if Language(nil).Clone() != nil {
		t.Fatal("Language clone did not preserve nil")
	}
	if Blocks(nil).Clone() != nil {
		t.Fatal("Blocks clone did not preserve nil")
	}
	if Items(nil).Clone() != nil {
		t.Fatal("Items clone did not preserve nil")
	}
	if Entities(nil).Clone() != nil {
		t.Fatal("Entities clone did not preserve nil")
	}
	if Biomes(nil).Clone() != nil {
		t.Fatal("Biomes clone did not preserve nil")
	}
	if Effects(nil).Clone() != nil {
		t.Fatal("Effects clone did not preserve nil")
	}
	if Enchantments(nil).Clone() != nil {
		t.Fatal("Enchantments clone did not preserve nil")
	}
	if Foods(nil).Clone() != nil {
		t.Fatal("Foods clone did not preserve nil")
	}
	if Particles(nil).Clone() != nil {
		t.Fatal("Particles clone did not preserve nil")
	}
	if Instruments(nil).Clone() != nil {
		t.Fatal("Instruments clone did not preserve nil")
	}
	if Attributes(nil).Clone() != nil {
		t.Fatal("Attributes clone did not preserve nil")
	}
	if Windows(nil).Clone() != nil {
		t.Fatal("Windows clone did not preserve nil")
	}
	if Materials(nil).Clone() != nil {
		t.Fatal("Materials clone did not preserve nil")
	}
}

func TestNamedFieldContracts(t *testing.T) {
	block := Block{ID: BlockID(1), Drops: Drops{{ID: ItemID(2), Metadata: Metadata(3)}}, HarvestTools: HarvestToolSet{ItemID(4): true}, Variations: Variations{{Metadata: Metadata(5)}}}
	item := Item{ID: ItemID(6), Variations: Variations{{Metadata: Metadata(7)}}}
	entity := Entity{ID: EntityID(8), InternalID: EntityInternalID(9)}
	biome := Biome{ID: BiomeID(10)}
	effect := Effect{ID: EffectID(11)}
	enchantment := Enchantment{ID: EnchantmentID(12)}
	food := Food{ID: ItemID(13), Variations: Variations{{Metadata: Metadata(14)}}}
	particle := Particle{ID: ParticleID(15)}
	instrument := Instrument{ID: InstrumentID(16)}
	window := Window{ID: WindowID("window")}
	material := Material{ToolSpeeds: ToolSpeedIndex{ItemID(17): 1}}
	recipe := Recipe{Ingredients: RecipeIngredients{{ID: ItemID(18), Metadata: Metadata(19)}}, Result: RecipeResult{ID: ItemID(20), Metadata: Metadata(21)}}
	shapes := CollisionShapes{Blocks: BlockShapeIndex{"stone": ShapeIDs{ShapeID(22)}}, Shapes: BoundingBoxIndex{ShapeID(22): BoundingBoxes{{}}}}
	protocol := Protocol{Phases: ProtocolPhases{"play": {ToClient: ProtocolDirection{Packets: Packets{{ID: PacketID(23)}}}}}}
	version := Version{Protocol: ProtocolNumber(24)}

	if block.ID != 1 || block.Drops[0].ID != 2 || block.Drops[0].Metadata != 3 || !block.HarvestTools[4] || block.Variations[0].Metadata != 5 || item.ID != 6 || item.Variations[0].Metadata != 7 || entity.ID != 8 || entity.InternalID != 9 || biome.ID != 10 || effect.ID != 11 || enchantment.ID != 12 || food.ID != 13 || food.Variations[0].Metadata != 14 || particle.ID != 15 || instrument.ID != 16 || window.ID != "window" || material.ToolSpeeds[17] != 1 || recipe.Ingredients[0].ID != 18 || recipe.Ingredients[0].Metadata != 19 || recipe.Result.ID != 20 || recipe.Result.Metadata != 21 || shapes.Blocks["stone"][0] != 22 || len(shapes.Shapes[22]) != 1 || protocol.Phases["play"].ToClient.Packets[0].ID != 23 || version.Protocol != 24 {
		t.Fatal("named field contracts did not retain typed values")
	}
}

func float64Pointer(value float64) *float64 {
	return &value
}
