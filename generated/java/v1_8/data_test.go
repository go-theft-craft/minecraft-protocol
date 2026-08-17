package v1_8

import (
	"bytes"
	"errors"
	"math"
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
	session, err := descriptor.NewSession(protocol.RoleClient, limits)
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if session.State() != StateHandshaking {
		t.Fatalf("initial state = %q, want %q", session.State(), StateHandshaking)
	}
	if session.Role() != protocol.RoleClient {
		t.Fatalf("Role() = %d, want %d", session.Role(), protocol.RoleClient)
	}
	if session.Limits() != limits {
		t.Fatal("Limits() did not return the limits the session was built with")
	}
	if session.Framer() == nil {
		t.Fatal("Framer() = nil")
	}
	for _, state := range []protocol.State{StateHandshaking, StateStatus, StateLogin, StatePlay} {
		if err := session.ValidateState(state); err != nil {
			t.Fatalf("ValidateState(%q) error = %v", state, err)
		}
		session.SetState(state)
		if session.State() != state {
			t.Fatalf("State() = %q, want %q", session.State(), state)
		}
	}
	if err := session.ValidateState(protocol.State("configuration")); err == nil {
		t.Fatal("ValidateState(configuration) error = nil")
	}
	if session.State() != StatePlay {
		t.Fatalf("rejected ValidateState changed state to %q", session.State())
	}

	if _, err := descriptor.NewSession(protocol.Role(0), limits); err == nil {
		t.Fatal("NewSession(invalid role) error = nil")
	}
	if _, err := descriptor.NewSession(protocol.RoleClient, protocol.Limits{}); !errors.Is(err, java.ErrInvalidLimits) {
		t.Fatalf("NewSession(invalid limits) error = %v, want ErrInvalidLimits", err)
	}
}

func TestProtocolSessionDirectionsFollowRole(t *testing.T) {
	limits := protocolLimits(t)

	client, err := Protocol().NewSession(protocol.RoleClient, limits)
	if err != nil {
		t.Fatal(err)
	}
	if client.Inbound() != protocol.DirectionClientbound || client.Outbound() != protocol.DirectionServerbound {
		t.Fatalf("client directions = %d, %d", client.Inbound(), client.Outbound())
	}

	server, err := Protocol().NewSession(protocol.RoleServer, limits)
	if err != nil {
		t.Fatal(err)
	}
	if server.Inbound() != protocol.DirectionServerbound || server.Outbound() != protocol.DirectionClientbound {
		t.Fatalf("server directions = %d, %d", server.Inbound(), server.Outbound())
	}
}

func TestProtocolSessionSnapshotIsIndependent(t *testing.T) {
	session := newTestSession(t, protocol.RoleClient, StateLogin)

	snapshot := session.Snapshot()
	if snapshot.State != StateLogin {
		t.Fatalf("Snapshot() state = %q, want %q", snapshot.State, StateLogin)
	}
	want := map[string]string{
		"compression.enabled":   "false",
		"compression.threshold": "0",
		"compression.policy":    "strict",
	}
	for key, value := range want {
		if snapshot.Pipeline[key] != value {
			t.Errorf("Snapshot() pipeline[%q] = %q, want %q", key, snapshot.Pipeline[key], value)
		}
	}

	snapshot.Pipeline["compression.enabled"] = "true"
	if session.Snapshot().Pipeline["compression.enabled"] != "false" {
		t.Fatal("Snapshot() shares its pipeline map between calls")
	}
}

func TestProtocolSessionKnownPacketAndEnvelopeValidation(t *testing.T) {
	limits := protocolLimits(t)
	server := newTestSession(t, protocol.RoleServer, StateStatus)
	client := newTestSession(t, protocol.RoleClient, StateStatus)

	wantValue := &StatusClientboundServerInfo{Response: `{"version":{"name":"1.8.9","protocol":47}}`}
	wantPacket := protocol.Packet{
		State: StateStatus, Direction: protocol.DirectionClientbound,
		ID: wantValue.PacketID(), Value: wantValue,
	}
	payload, err := server.EncodeFrame(wantPacket)
	if err != nil {
		t.Fatalf("EncodeFrame() error = %v", err)
	}

	got, err := client.DecodeFrame(payload)
	if err != nil {
		t.Fatalf("DecodeFrame() error = %v", err)
	}
	gotValue, ok := got.Value.(*StatusClientboundServerInfo)
	if !ok || gotValue.Response != wantValue.Response {
		t.Fatalf("DecodeFrame() Value = %#v", got.Value)
	}
	if got.State != StateStatus || got.Direction != protocol.DirectionClientbound || got.ID != 0 || got.Name != "server_info" {
		t.Fatalf("DecodeFrame() envelope = %+v", got)
	}
	if server.State() != StateStatus || client.State() != StateStatus {
		t.Fatalf("session changed state during coding: server=%q client=%q", server.State(), client.State())
	}
	_ = limits

	invalid := []protocol.Packet{
		{State: StateLogin, Direction: protocol.DirectionServerbound, ID: 0, Value: &StatusServerboundPingStart{}},
		{State: StateStatus, Direction: protocol.DirectionClientbound, ID: 0, Value: &StatusServerboundPingStart{}},
		{State: StateStatus, Direction: protocol.DirectionServerbound, ID: 1, Value: &StatusServerboundPingStart{}},
		{State: StateStatus, Direction: protocol.DirectionServerbound, ID: 0, Value: &StatusClientboundServerInfo{}},
		{State: StateStatus, Direction: protocol.DirectionServerbound, ID: 0, Value: &LoginServerboundLoginStart{}},
	}
	for index, packet := range invalid {
		if _, err := client.EncodeFrame(packet); err == nil {
			t.Errorf("EncodeFrame(invalid envelope %d) error = nil", index)
		}
	}
}

func TestProtocolSessionUnknownPacketOwnershipAndRawEncode(t *testing.T) {
	limits := protocolLimits(t)
	session := newTestSession(t, protocol.RoleClient, StateStatus)

	body, err := java.JoinPacketBody(protocol.Packet{ID: 0x7f, Payload: []byte{1, 2, 3}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	packet, err := session.DecodeFrame(body)
	if err != nil {
		t.Fatalf("DecodeFrame() error = %v", err)
	}
	unknown, ok := packet.Value.(protocol.UnknownPacket)
	if !ok {
		t.Fatalf("DecodeFrame() Value = %T, want protocol.UnknownPacket", packet.Value)
	}
	if packet.State != StateStatus || packet.Direction != protocol.DirectionClientbound || packet.ID != 0x7f || packet.Name != "" {
		t.Fatalf("DecodeFrame() unknown envelope = %+v", packet)
	}
	packet.Payload[0] = 9
	if unknown.Payload[0] != 1 {
		t.Fatal("UnknownPacket.Payload aliases Packet.Payload")
	}
	unknown.Payload[1] = 9
	if packet.Payload[1] != 2 {
		t.Fatal("Packet.Payload aliases UnknownPacket.Payload")
	}

	// A queued packet must not change when the frame buffer it came from is
	// reused, so the decoded payload owns its bytes.
	body[1] = 0xee
	if packet.Payload[0] != 9 {
		t.Fatal("DecodeFrame() payload aliases the frame buffer")
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
			encoded, err := session.EncodeFrame(test.packet)
			if err != nil {
				t.Fatalf("EncodeFrame() error = %v", err)
			}
			got, err := java.SplitPacketBody(encoded)
			if err != nil {
				t.Fatal(err)
			}
			if got.ID != test.packet.ID || !bytes.Equal(got.Payload, test.payload) {
				t.Fatalf("packet body = %+v, want ID %d payload %x", got, test.packet.ID, test.payload)
			}
		})
	}
}

func TestProtocolSessionRejectsKnownPacketTrailingBytes(t *testing.T) {
	limits := protocolLimits(t)
	session := newTestSession(t, protocol.RoleServer, StateStatus)

	body, err := java.JoinPacketBody(protocol.Packet{ID: 0, Payload: []byte{0xff}}, limits)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.DecodeFrame(body); !errors.Is(err, java.ErrTrailingBytes) {
		t.Fatalf("DecodeFrame() error = %v, want ErrTrailingBytes", err)
	}
}

func TestProtocolSessionCompressionControl(t *testing.T) {
	limits := protocolLimits(t)
	session := newTestSession(t, protocol.RoleServer, StatePlay)

	control := java.CompressionControl{Enabled: true, Threshold: 8, Policy: java.StrictCompression}
	if err := session.ValidateControl(control); err != nil {
		t.Fatalf("ValidateControl() error = %v", err)
	}
	session.ApplyControl(control)

	snapshot := session.Snapshot()
	if snapshot.Pipeline["compression.enabled"] != "true" ||
		snapshot.Pipeline["compression.threshold"] != "8" ||
		snapshot.Pipeline["compression.policy"] != "strict" {
		t.Fatalf("Snapshot() pipeline = %v", snapshot.Pipeline)
	}

	// A compressed round trip proves the control reached both directions.
	client := newTestSession(t, protocol.RoleClient, StatePlay)
	client.ApplyControl(control)

	want := &PlayClientboundKickDisconnect{Reason: `{"text":"` + string(bytes.Repeat([]byte{'x'}, 64)) + `"}`}
	payload, err := session.EncodeFrame(protocol.Packet{
		State: StatePlay, Direction: protocol.DirectionClientbound,
		ID: want.PacketID(), Value: want,
	})
	if err != nil {
		t.Fatalf("EncodeFrame() error = %v", err)
	}
	declared, _, err := java.ReadVarInt(bytes.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	if declared == 0 {
		t.Fatal("EncodeFrame() left a packet above the threshold uncompressed")
	}

	got, err := client.DecodeFrame(payload)
	if err != nil {
		t.Fatalf("DecodeFrame() error = %v", err)
	}
	value, ok := got.Value.(*PlayClientboundKickDisconnect)
	if !ok || value.Reason != want.Reason {
		t.Fatalf("DecodeFrame() Value = %#v", got.Value)
	}
	_ = limits
}

func TestProtocolSessionRejectsUnsupportedControls(t *testing.T) {
	session := newTestSession(t, protocol.RoleServer, StatePlay)

	if err := session.ValidateControl(protocol.StateControl{State: StatePlay}); err != nil {
		t.Fatalf("ValidateControl(state) error = %v", err)
	}
	if err := session.ValidateControl(protocol.StateControl{State: protocol.State("configuration")}); err == nil {
		t.Fatal("ValidateControl(unsupported state) error = nil")
	}
	if err := session.ValidateControl(nil); err == nil {
		t.Fatal("ValidateControl(nil) error = nil")
	}
	if err := session.ValidateControl(unsupportedControl{}); err == nil {
		t.Fatal("ValidateControl(unsupported) error = nil")
	}
	for _, control := range []java.CompressionControl{
		{Enabled: true, Threshold: -1, Policy: java.StrictCompression},
		{Enabled: true, Threshold: 8},
	} {
		if err := session.ValidateControl(control); err == nil {
			t.Fatalf("ValidateControl(%+v) error = nil", control)
		}
	}
}

type unsupportedControl struct{}

func (unsupportedControl) ControlName() string { return "test.unsupported" }

func newTestSession(t *testing.T, role protocol.Role, state protocol.State) protocol.Session {
	t.Helper()

	session, err := Protocol().NewSession(role, protocolLimits(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := session.ValidateState(state); err != nil {
		t.Fatal(err)
	}
	session.SetState(state)
	return session
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

func TestPhysicsDocumentRanges(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	physics := set.Physics()

	if physics.DefaultSlipperiness <= 0 || physics.DefaultSlipperiness > 1 {
		t.Fatalf("default slipperiness = %v, want a value in (0, 1]", physics.DefaultSlipperiness)
	}
	if len(physics.BlockSlipperiness) == 0 {
		t.Fatal("block slipperiness is empty")
	}
	for name, value := range physics.BlockSlipperiness {
		if value <= 0 || value > 1.2 {
			t.Fatalf("slipperiness for %s = %v, want a value in (0, 1.2]", name, value)
		}
	}

	for name, motion := range physics.EntityMotion {
		if motion.Gravity <= 0 || motion.Gravity > 1 {
			t.Fatalf("%s gravity = %v, want a value in (0, 1]", name, motion.Gravity)
		}
		if motion.HorizontalDrag <= 0 || motion.HorizontalDrag > 1 {
			t.Fatalf("%s horizontal drag = %v, want a value in (0, 1]", name, motion.HorizontalDrag)
		}
		if motion.VerticalDrag <= 0 || motion.VerticalDrag > 1 {
			t.Fatalf("%s vertical drag = %v, want a value in (0, 1]", name, motion.VerticalDrag)
		}
		if motion.StepHeight < 0 || motion.StepHeight > 2 {
			t.Fatalf("%s step height = %v, want a value in [0, 2]", name, motion.StepHeight)
		}
	}
	for _, required := range []string{"player", "item", "arrow"} {
		if _, ok := physics.EntityMotion[required]; !ok {
			t.Fatalf("entity motion is missing %s", required)
		}
	}
}

func TestSinTableMatchesSine(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	table := set.Physics().SinTable

	if len(table) == 0 || len(table)&(len(table)-1) != 0 {
		t.Fatalf("sin table length = %d, want a non-zero power of two", len(table))
	}
	for index := 0; index < len(table); index += len(table) / 64 {
		want := math.Sin(float64(index) * math.Pi * 2 / float64(len(table)))
		if math.Abs(float64(table[index])-want) > 1e-4 {
			t.Fatalf("sin table[%d] = %v, want approximately %v", index, table[index], want)
		}
	}
}

func TestPhysicsIsCallerOwned(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	first := set.Physics()
	first.BlockSlipperiness["ice"] = 0

	if set.Physics().BlockSlipperiness["ice"] == 0 {
		t.Fatal("Set.Physics returned an aliased index")
	}
}

// TestSlipperinessMatchesVanilla pins the values a movement kernel is most
// sensitive to. Ice and slime are the only 1.8.9 blocks that differ from the
// default, so a wrong registry or a wrong field would show up here.
func TestSlipperinessMatchesVanilla(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	physics := set.Physics()

	for _, test := range []struct {
		block string
		want  float64
	}{
		{"ice", 0.98},
		{"packed_ice", 0.98},
		{"slime", 0.8},
		{"stone", 0.6},
		{"soul_sand", 0.6},
	} {
		if got := physics.Slipperiness(test.block); got != test.want {
			t.Errorf("Slipperiness(%s) = %v, want %v", test.block, got, test.want)
		}
	}
	if got := physics.Slipperiness("a block that does not exist"); got != physics.DefaultSlipperiness {
		t.Errorf("unknown block slipperiness = %v, want the default", got)
	}
}

// TestBlockMovementAnswersTheGamesOwnRule pins the measurement a caller walks
// on. The three cases are the ones a guess gets wrong: a flower is not a wall,
// water is not a floor, and stone is both.
func TestBlockMovementAnswersTheGamesOwnRule(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	movement := set.BlockMovement()
	if movement == nil {
		t.Fatal("the measured version publishes no block movement registry")
	}

	for _, test := range []struct {
		name  string
		id    data.BlockID
		want  bool
		known bool
	}{
		{name: "air", id: 0, want: false, known: true},
		{name: "stone", id: 1, want: true, known: true},
		{name: "water", id: 9, want: false, known: true},
		{name: "sapling", id: 6, want: false, known: true},
		{name: "a block this version does not have", id: 4000, want: false, known: false},
	} {
		got, known := movement.ByID(test.id)
		if got != test.want || known != test.known {
			t.Errorf("ByID(%s) = %v, %v, want %v, %v", test.name, got, known, test.want, test.known)
		}
	}

	if got, want := len(movement.All()), 198; got != want {
		t.Errorf("measured blocks = %d, want %d", got, want)
	}
}

// TestBlockMovementReadsAChunkState pins the encoding, which is the trap.
//
// This version packs a chunk state as the block identifier shifted left four
// with the metadata below it, and packs it the other way round in
// Block.getStateId. A registry keyed the other way resolves every lookup and
// answers about the wrong block, so the assertion that matters is the one on a
// state carrying metadata: stone with any variant is still stone.
func TestBlockMovementReadsAChunkState(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	movement := set.BlockMovement()

	for _, test := range []struct {
		name  string
		state data.BlockStateID
		want  bool
		known bool
	}{
		{name: "stone", state: 1 << 4, want: true, known: true},
		{name: "polished andesite", state: 1<<4 | 6, want: true, known: true},
		{name: "air", state: 0, want: false, known: true},
		{name: "flowing water at level three", state: 8<<4 | 3, want: false, known: true},
		{name: "a state no block has", state: 4000 << 4, want: false, known: false},
	} {
		got, known := movement.ByState(test.state)
		if got != test.want || known != test.known {
			t.Errorf("ByState(%s) = %v, %v, want %v, %v", test.name, got, known, test.want, test.known)
		}
	}
}

// TestBlockMovementIsCallerOwned pins that a caller mutating the measurement it
// was handed cannot change what the next caller reads.
func TestBlockMovementIsCallerOwned(t *testing.T) {
	set, err := Data()
	if err != nil {
		t.Fatalf("Data: %v", err)
	}
	movement := set.BlockMovement()

	first := movement.All()
	first[1] = false

	if blocks, _ := movement.ByID(1); !blocks {
		t.Fatal("BlockMovement returned an aliased index")
	}
}
