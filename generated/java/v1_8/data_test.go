package v1_8

import (
	"bytes"
	"errors"
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

func TestProtocolDescriptorAndExplicitStates(t *testing.T) {
	descriptor := Protocol()
	if descriptor.ID() != "java/1.8.9" || descriptor.Edition() != protocol.EditionJava || descriptor.Version() != Version() {
		t.Fatalf("Protocol() metadata = %q, %q, %+v", descriptor.ID(), descriptor.Edition(), descriptor.Version())
	}

	limits := protocolLimits(t)
	codec, err := descriptor.NewCodec(protocol.RoleClient, limits)
	if err != nil {
		t.Fatalf("NewCodec() error = %v", err)
	}
	if codec.State() != StateHandshaking {
		t.Fatalf("initial state = %q, want %q", codec.State(), StateHandshaking)
	}
	for _, state := range []protocol.State{StateHandshaking, StateStatus, StateLogin, StatePlay} {
		if err := codec.SetState(state); err != nil {
			t.Fatalf("SetState(%q) error = %v", state, err)
		}
		if codec.State() != state {
			t.Fatalf("State() = %q, want %q", codec.State(), state)
		}
	}
	if err := codec.SetState(protocol.State("configuration")); err == nil {
		t.Fatal("SetState(configuration) error = nil")
	}
	if codec.State() != StatePlay {
		t.Fatalf("invalid SetState changed state to %q", codec.State())
	}

	if _, err := descriptor.NewCodec(protocol.Role(0), limits); err == nil {
		t.Fatal("NewCodec(invalid role) error = nil")
	}
	if _, err := descriptor.NewCodec(protocol.RoleClient, protocol.Limits{}); !errors.Is(err, java.ErrInvalidLimits) {
		t.Fatalf("NewCodec(invalid limits) error = %v, want ErrInvalidLimits", err)
	}
}

func TestProtocolCodecKnownPacketAndEnvelopeValidation(t *testing.T) {
	limits := protocolLimits(t)
	server, err := Protocol().NewCodec(protocol.RoleServer, limits)
	if err != nil {
		t.Fatal(err)
	}
	client, err := Protocol().NewCodec(protocol.RoleClient, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.SetState(StateStatus); err != nil {
		t.Fatal(err)
	}
	if err := client.SetState(StateStatus); err != nil {
		t.Fatal(err)
	}

	var wire bytes.Buffer
	wantValue := &StatusClientboundServerInfo{Response: `{"version":{"name":"1.8.9","protocol":47}}`}
	wantPacket := protocol.Packet{
		State: StateStatus, Direction: protocol.DirectionClientbound,
		ID: wantValue.PacketID(), Value: wantValue,
	}
	if err := server.Write(&wire, wantPacket); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	got, err := client.Read(&wire)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	gotValue, ok := got.Value.(*StatusClientboundServerInfo)
	if !ok || gotValue.Response != wantValue.Response {
		t.Fatalf("Read() Value = %#v", got.Value)
	}
	if got.State != StateStatus || got.Direction != protocol.DirectionClientbound || got.ID != 0 || got.Name != "server_info" {
		t.Fatalf("Read() envelope = %+v", got)
	}
	if server.State() != StateStatus || client.State() != StateStatus {
		t.Fatalf("codec changed state automatically: server=%q client=%q", server.State(), client.State())
	}

	invalid := []protocol.Packet{
		{State: StateLogin, Direction: protocol.DirectionServerbound, ID: 0, Value: &StatusServerboundPingStart{}},
		{State: StateStatus, Direction: protocol.DirectionClientbound, ID: 0, Value: &StatusServerboundPingStart{}},
		{State: StateStatus, Direction: protocol.DirectionServerbound, ID: 1, Value: &StatusServerboundPingStart{}},
		{State: StateStatus, Direction: protocol.DirectionServerbound, ID: 0, Value: &StatusClientboundServerInfo{}},
		{State: StateStatus, Direction: protocol.DirectionServerbound, ID: 0, Value: &LoginServerboundLoginStart{}},
	}
	for index, packet := range invalid {
		var output bytes.Buffer
		if err := client.Write(&output, packet); err == nil {
			t.Errorf("Write(invalid envelope %d) error = nil", index)
		}
		if output.Len() != 0 {
			t.Errorf("Write(invalid envelope %d) wrote %d bytes", index, output.Len())
		}
	}
}

func TestProtocolCodecUnknownPacketOwnershipAndRawWrite(t *testing.T) {
	limits := protocolLimits(t)
	codec, err := Protocol().NewCodec(protocol.RoleClient, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := codec.SetState(StateStatus); err != nil {
		t.Fatal(err)
	}

	var wire bytes.Buffer
	if err := java.WriteRawPacket(&wire, limits, protocol.Packet{ID: 0x7f, Payload: []byte{1, 2, 3}}); err != nil {
		t.Fatal(err)
	}
	packet, err := codec.Read(&wire)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	unknown, ok := packet.Value.(protocol.UnknownPacket)
	if !ok {
		t.Fatalf("Read() Value = %T, want protocol.UnknownPacket", packet.Value)
	}
	if packet.State != StateStatus || packet.Direction != protocol.DirectionClientbound || packet.ID != 0x7f || packet.Name != "" {
		t.Fatalf("Read() unknown envelope = %+v", packet)
	}
	packet.Payload[0] = 9
	if unknown.Payload[0] != 1 {
		t.Fatal("UnknownPacket.Payload aliases Packet.Payload")
	}
	unknown.Payload[1] = 9
	if packet.Payload[1] != 2 {
		t.Fatal("Packet.Payload aliases UnknownPacket.Payload")
	}

	for _, test := range []struct {
		name    string
		packet  protocol.Packet
		payload []byte
	}{
		{
			name: "raw envelope",
			packet: protocol.Packet{
				State: StateStatus, Direction: protocol.DirectionServerbound, ID: 0x7e,
				Payload: []byte{4, 5},
			},
			payload: []byte{4, 5},
		},
		{
			name: "unknown value",
			packet: protocol.Packet{
				State: StateStatus, Direction: protocol.DirectionServerbound, ID: 0x7f,
				Value: protocol.UnknownPacket{Payload: []byte{6, 7}}, Payload: []byte{0xff},
			},
			payload: []byte{6, 7},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			if err := codec.Write(&output, test.packet); err != nil {
				t.Fatalf("Write() error = %v", err)
			}
			got, err := java.ReadRawPacket(&output, limits)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != test.packet.ID || !bytes.Equal(got.Payload, test.payload) {
				t.Fatalf("raw packet = %+v, want ID %d payload %x", got, test.packet.ID, test.payload)
			}
		})
	}
}

func TestProtocolCodecRejectsKnownPacketTrailingBytes(t *testing.T) {
	limits := protocolLimits(t)
	codec, err := Protocol().NewCodec(protocol.RoleServer, limits)
	if err != nil {
		t.Fatal(err)
	}
	if err := codec.SetState(StateStatus); err != nil {
		t.Fatal(err)
	}

	var wire bytes.Buffer
	if err := java.WriteRawPacket(&wire, limits, protocol.Packet{ID: 0, Payload: []byte{0xff}}); err != nil {
		t.Fatal(err)
	}
	if _, err := codec.Read(&wire); !errors.Is(err, java.ErrTrailingBytes) {
		t.Fatalf("Read() error = %v, want ErrTrailingBytes", err)
	}
}

func protocolLimits(t *testing.T) protocol.Limits {
	t.Helper()
	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatal(err)
	}
	return limits
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
