package interop

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"

	protocol "github.com/go-theft-craft/minecraft-protocol"
	v26_1 "github.com/go-theft-craft/minecraft-protocol/generated/java/v26_1"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

const (
	protocolSchemaPath = "../source/java/26.1/data/protocol.json"
	npmSchemaPath      = "node/node_modules/minecraft-data/minecraft-data/data/pc/26.1/protocol.json"
	protodefPackage    = "node/node_modules/protodef/package.json"
	minecraftDataPkg   = "node/node_modules/minecraft-data/package.json"
)

// TestProtoDefRunsAgainstTheSameSchema is the premise the rest of this file
// rests on. Two implementations agreeing about different schemas would prove
// nothing, so the pinned tree and the npm package are compared byte for byte
// before any packet is encoded, and the mismatch is a failure naming both
// hashes rather than a skip.
func TestProtoDefRunsAgainstTheSameSchema(t *testing.T) {
	t.Parallel()

	pinned := fileDigest(t, protocolSchemaPath)
	shipped := fileDigest(t, npmSchemaPath)
	if pinned != shipped {
		t.Fatalf("protocol.json differs: pinned %s, npm %s", pinned, shipped)
	}

	for path, want := range map[string]string{
		protodefPackage:  "1.19.0",
		minecraftDataPkg: "3.113.1",
	} {
		var manifest struct {
			Name    string `json:"name"`
			Version string `json:"version"`
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}
		if err := json.Unmarshal(raw, &manifest); err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		if manifest.Version != want {
			t.Errorf("%s is %s, want the pinned %s", manifest.Name, manifest.Version, want)
		}
	}
}

func fileDigest(t *testing.T, path string) string {
	t.Helper()

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	sum := sha256.Sum256(raw)

	return hex.EncodeToString(sum[:])
}

// protodefRunner is the Node harness, kept alive across fixtures because
// compiling the schema for a state costs far more than any single packet.
type protodefRunner struct {
	mutex   sync.Mutex
	command *exec.Cmd
	stdin   io.WriteCloser
	stdout  *bufio.Reader
}

func startProtoDefRunner(t *testing.T) *protodefRunner {
	t.Helper()

	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node is not on PATH: %v", err)
	}
	schema, err := filepath.Abs(protocolSchemaPath)
	if err != nil {
		t.Fatalf("resolve schema: %v", err)
	}

	command := exec.Command("node", "node/protodef-runner.mjs", schema)
	command.Stderr = os.Stderr
	stdin, err := command.StdinPipe()
	if err != nil {
		t.Fatalf("stdin: %v", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("stdout: %v", err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start the ProtoDef runner: %v", err)
	}

	runner := &protodefRunner{command: command, stdin: stdin, stdout: bufio.NewReader(stdout)}
	t.Cleanup(func() {
		_, _ = io.WriteString(stdin, "{\"op\":\"stop\"}\n")
		_ = stdin.Close()
		_ = command.Wait()
	})

	return runner
}

type protodefResult struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error"`
	Hex    string          `json:"hex"`
	Name   string          `json:"name"`
	Params json.RawMessage `json:"params"`
}

func (r *protodefRunner) call(t *testing.T, command map[string]any) protodefResult {
	t.Helper()

	r.mutex.Lock()
	defer r.mutex.Unlock()

	encoded, err := json.Marshal(command)
	if err != nil {
		t.Fatalf("encode command: %v", err)
	}
	if _, err := r.stdin.Write(append(encoded, '\n')); err != nil {
		t.Fatalf("write command: %v", err)
	}
	line, err := r.stdout.ReadBytes('\n')
	if err != nil {
		t.Fatalf("read result: %v", err)
	}
	var result protodefResult
	if err := json.Unmarshal(line, &result); err != nil {
		t.Fatalf("parse result %q: %v", line, err)
	}

	return result
}

// packetCodec is what every generated packet type implements. The test needs
// it because it encodes and decodes packets without a session.
type packetCodec interface {
	PacketID() int32
	Encode(*java.Buffer) error
	Decode(*java.Buffer) error
}

func testLimits(t *testing.T) protocol.Limits {
	t.Helper()

	limits, err := protocol.NewLimits()
	if err != nil {
		t.Fatalf("NewLimits: %v", err)
	}

	return limits
}

// encodePacketBody encodes one packet the way a session would: the packet ID
// as a VarInt, then the body.
func encodePacketBody(t *testing.T, value packetCodec) []byte {
	t.Helper()

	limits := testLimits(t)
	buffer, err := java.NewWriteBuffer(limits)
	if err != nil {
		t.Fatalf("NewWriteBuffer: %v", err)
	}
	if err := value.Encode(buffer); err != nil {
		t.Fatalf("encode: %v", err)
	}
	body, err := java.JoinPacketBody(protocol.Packet{ID: value.PacketID(), Payload: buffer.Bytes()}, limits)
	if err != nil {
		t.Fatalf("join packet body: %v", err)
	}

	return body
}

// decodePacketBody decodes into target and checks that nothing is left over,
// which is what makes a field this side reads too little of a failure.
func decodePacketBody(t *testing.T, body []byte, target packetCodec) {
	t.Helper()

	limits := testLimits(t)
	raw, err := java.SplitPacketBody(body)
	if err != nil {
		t.Fatalf("split packet body: %v", err)
	}
	if raw.ID != target.PacketID() {
		t.Fatalf("packet ID = %#x, want %#x", raw.ID, target.PacketID())
	}
	buffer, err := java.NewReadBuffer(raw.Payload, limits)
	if err != nil {
		t.Fatalf("NewReadBuffer: %v", err)
	}
	if err := target.Decode(buffer); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := buffer.RequireEmpty("fixture"); err != nil {
		t.Fatalf("decode left bytes unread: %v", err)
	}
}

// differentialFixture is one packet compared across the two implementations.
type differentialFixture struct {
	state     string
	direction string
	// packetName is what upstream calls the packet. Asserting it catches a
	// packet that encodes to bytes the other side reads as something else,
	// which an ID or a state mistake would produce.
	packetName string
	// packetLabel names the subtest when two fixtures cover the same packet.
	packetLabel string
	value       packetCodec
	// fresh returns an empty value of the same type to decode into.
	fresh func() packetCodec
}

// TestProtocol775DifferentialFixtures compares the generated codecs against
// ProtoDef in both directions.
//
// For each fixture: Go encodes, ProtoDef decodes and re-encodes, and the bytes
// must match — which proves ProtoDef read every field Go wrote and agreed
// about all of them. Then Go decodes ProtoDef's bytes and re-encodes, and the
// value must be the one the fixture started with.
func TestProtocol775DifferentialFixtures(t *testing.T) {
	runner := startProtoDefRunner(t)

	for _, fixture := range differentialFixtures(t) {
		label := fixture.packetName
		if fixture.packetLabel != "" {
			label = fixture.packetLabel
		}
		t.Run(fixture.state+"/"+fixture.direction+"/"+label, func(t *testing.T) {
			goBytes := encodePacketBody(t, fixture.value)

			decoded := runner.call(t, map[string]any{
				"op":        "decode",
				"state":     fixture.state,
				"direction": fixture.direction,
				"hex":       hex.EncodeToString(goBytes),
			})
			if !decoded.OK {
				t.Fatalf("ProtoDef could not read what Go wrote: %s", decoded.Error)
			}
			if decoded.Name != fixture.packetName {
				t.Fatalf("ProtoDef read the packet as %q, want %q", decoded.Name, fixture.packetName)
			}

			encoded := runner.call(t, map[string]any{
				"op":        "encode",
				"state":     fixture.state,
				"direction": fixture.direction,
				"name":      fixture.packetName,
				"params":    decoded.Params,
			})
			if !encoded.OK {
				t.Fatalf("ProtoDef could not write what it read: %s", encoded.Error)
			}
			nodeBytes, err := hex.DecodeString(encoded.Hex)
			if err != nil {
				t.Fatalf("decode ProtoDef output: %v", err)
			}
			if !bytes.Equal(nodeBytes, goBytes) {
				t.Fatalf("ProtoDef re-encoded %x, Go wrote %x", nodeBytes, goBytes)
			}

			target := fixture.fresh()
			decodePacketBody(t, nodeBytes, target)
			if !reflect.DeepEqual(target, fixture.value) {
				t.Fatalf("Go read back %+v, want %+v", target, fixture.value)
			}
		})
	}
}

// emptyMatchers is the component matcher every item predicate carries. It is
// not optional on the wire: a predicate always states its matchers, and none
// is stated as empty lists rather than as an absent field.
func emptyMatchers() *v26_1.DataComponentMatchers {
	exact := v26_1.ExactComponentMatcher{}

	return &v26_1.DataComponentMatchers{ExactMatchers: &exact, PartialMatchers: []int32{}}
}

func holderSetTag(t *testing.T, tag string) *java.HolderSet[int32] {
	t.Helper()

	value := java.NewHolderSetTag[int32](tag)

	return &value
}

func holderSetIDs(t *testing.T, ids []int32) *java.HolderSet[int32] {
	t.Helper()

	value := java.NewHolderSetIDs(ids)

	return &value
}

func mustUUID(t *testing.T, text string) java.UUID {
	t.Helper()

	value, err := java.ParseUUID(text)
	if err != nil {
		t.Fatalf("ParseUUID(%q): %v", text, err)
	}

	return value
}

func mustText(t *testing.T, text string) java.NetworkNBT {
	t.Helper()

	value, err := java.NewNetworkNBTText(text, testLimits(t))
	if err != nil {
		t.Fatalf("NewNetworkNBTText: %v", err)
	}

	return value
}

func differentialFixtures(t *testing.T) []differentialFixture {
	t.Helper()

	identity := mustUUID(t, "069a79f4-44e9-4726-a5be-fca90e38aaf5")
	component := mustText(t, "closing")
	cookie := []byte{0x01, 0x02, 0x03}

	return []differentialFixture{
		{
			state: "handshaking", direction: "toServer", packetName: "set_protocol",
			value: &v26_1.HandshakingServerboundSetProtocol{
				ProtocolVersion: 775, ServerHost: "localhost", ServerPort: 25565, NextState: 2,
			},
			fresh: func() packetCodec { return &v26_1.HandshakingServerboundSetProtocol{} },
		},
		{
			state: "status", direction: "toServer", packetName: "ping_start",
			value: &v26_1.StatusServerboundPingStart{},
			fresh: func() packetCodec { return &v26_1.StatusServerboundPingStart{} },
		},
		{
			state: "status", direction: "toClient", packetName: "server_info",
			value: &v26_1.StatusClientboundServerInfo{Response: `{"description":"a server"}`},
			fresh: func() packetCodec { return &v26_1.StatusClientboundServerInfo{} },
		},
		{
			state: "status", direction: "toClient", packetName: "ping",
			value: &v26_1.StatusClientboundPing{Time: 1234567890},
			fresh: func() packetCodec { return &v26_1.StatusClientboundPing{} },
		},

		// Every login packet, in both directions.
		{
			state: "login", direction: "toClient", packetName: "disconnect",
			value: &v26_1.LoginClientboundDisconnect{Reason: `{"text":"nope"}`},
			fresh: func() packetCodec { return &v26_1.LoginClientboundDisconnect{} },
		},
		{
			state: "login", direction: "toClient", packetName: "encryption_begin",
			value: &v26_1.LoginClientboundEncryptionBegin{
				ServerID: "", PublicKey: []byte{0x30, 0x82, 0x01},
				VerifyToken: []byte{0x0a, 0x0b, 0x0c, 0x0d}, ShouldAuthenticate: true,
			},
			fresh: func() packetCodec { return &v26_1.LoginClientboundEncryptionBegin{} },
		},
		{
			state: "login", direction: "toClient", packetName: "success",
			value: &v26_1.LoginClientboundSuccess{
				UUID: identity, Username: "tester",
				Properties: []v26_1.LoginClientboundSuccessPropertiesItem{},
			},
			fresh: func() packetCodec { return &v26_1.LoginClientboundSuccess{} },
		},
		{
			state: "login", direction: "toClient", packetName: "compress",
			value: &v26_1.LoginClientboundCompress{Threshold: 256},
			fresh: func() packetCodec { return &v26_1.LoginClientboundCompress{} },
		},
		{
			state: "login", direction: "toClient", packetName: "login_plugin_request",
			value: &v26_1.LoginClientboundLoginPluginRequest{
				MessageID: 7, Channel: "minecraft:brand", Data: []byte{0x01, 0x02},
			},
			fresh: func() packetCodec { return &v26_1.LoginClientboundLoginPluginRequest{} },
		},
		{
			state: "login", direction: "toClient", packetName: "cookie_request",
			value: &v26_1.LoginClientboundCookieRequest{Cookie: "minecraft:session"},
			fresh: func() packetCodec { return &v26_1.LoginClientboundCookieRequest{} },
		},
		{
			state: "login", direction: "toServer", packetName: "login_start",
			value: &v26_1.LoginServerboundLoginStart{Username: "tester", PlayerUUID: identity},
			fresh: func() packetCodec { return &v26_1.LoginServerboundLoginStart{} },
		},
		{
			state: "login", direction: "toServer", packetName: "encryption_begin",
			value: &v26_1.LoginServerboundEncryptionBegin{
				SharedSecret: []byte{0x11, 0x22}, VerifyToken: []byte{0x33, 0x44},
			},
			fresh: func() packetCodec { return &v26_1.LoginServerboundEncryptionBegin{} },
		},
		{
			state: "login", direction: "toServer", packetName: "login_plugin_response",
			value: &v26_1.LoginServerboundLoginPluginResponse{MessageID: 7, Data: &cookie},
			fresh: func() packetCodec { return &v26_1.LoginServerboundLoginPluginResponse{} },
		},
		{
			state: "login", direction: "toServer", packetName: "login_acknowledged",
			value: &v26_1.LoginServerboundLoginAcknowledged{},
			fresh: func() packetCodec { return &v26_1.LoginServerboundLoginAcknowledged{} },
		},
		{
			state: "login", direction: "toServer", packetName: "cookie_response",
			value: &v26_1.LoginServerboundCookieResponse{Key: "minecraft:session", Value: &cookie},
			fresh: func() packetCodec { return &v26_1.LoginServerboundCookieResponse{} },
		},

		// Configuration, where a modern login spends most of its packets.
		{
			state: "configuration", direction: "toClient", packetName: "custom_payload",
			value: &v26_1.ConfigurationClientboundCustomPayload{Channel: "minecraft:brand", Data: []byte("vanilla")},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundCustomPayload{} },
		},
		{
			state: "configuration", direction: "toClient", packetName: "disconnect",
			value: &v26_1.ConfigurationClientboundDisconnect{Reason: component},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundDisconnect{} },
		},
		{
			state: "configuration", direction: "toClient", packetName: "finish_configuration",
			value: &v26_1.ConfigurationClientboundFinishConfiguration{},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundFinishConfiguration{} },
		},
		{
			state: "configuration", direction: "toClient", packetName: "keep_alive",
			value: &v26_1.ConfigurationClientboundKeepAlive{KeepAliveID: 987654321},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundKeepAlive{} },
		},
		{
			state: "configuration", direction: "toClient", packetName: "ping",
			value: &v26_1.ConfigurationClientboundPing{ID: 42},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundPing{} },
		},
		{
			state: "configuration", direction: "toClient", packetName: "reset_chat",
			value: &v26_1.ConfigurationClientboundResetChat{},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundResetChat{} },
		},
		{
			state: "configuration", direction: "toClient", packetName: "registry_data",
			value: &v26_1.ConfigurationClientboundRegistryData{
				ID: "minecraft:dimension_type",
				Entries: []v26_1.ConfigurationClientboundRegistryDataEntriesItem{
					{Key: "minecraft:overworld", Value: &component},
					{Key: "minecraft:the_nether"},
				},
			},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundRegistryData{} },
		},
		{
			state: "configuration", direction: "toClient", packetName: "remove_resource_pack",
			value: &v26_1.ConfigurationClientboundRemoveResourcePack{UUID: &identity},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundRemoveResourcePack{} },
		},
		{
			state: "configuration", direction: "toClient", packetName: "add_resource_pack",
			value: &v26_1.ConfigurationClientboundAddResourcePack{
				UUID: identity, URL: "https://example.invalid/pack.zip",
				Hash: "0123456789abcdef", Forced: true, PromptMessage: &component,
			},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundAddResourcePack{} },
		},
		{
			state: "configuration", direction: "toClient", packetName: "store_cookie",
			value: &v26_1.ConfigurationClientboundStoreCookie{Key: "minecraft:session", Value: cookie},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundStoreCookie{} },
		},
		{
			state: "configuration", direction: "toClient", packetName: "transfer",
			value: &v26_1.ConfigurationClientboundTransfer{Host: "example.invalid", Port: 25565},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundTransfer{} },
		},
		{
			state: "configuration", direction: "toClient", packetName: "feature_flags",
			value: &v26_1.ConfigurationClientboundFeatureFlags{Features: []string{"minecraft:vanilla"}},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundFeatureFlags{} },
		},
		{
			state: "configuration", direction: "toClient", packetName: "tags",
			value: &v26_1.ConfigurationClientboundTags{
				Tags: []v26_1.ConfigurationClientboundTagsTagsItem{
					{
						TagType: "minecraft:block",
						Tags: []v26_1.ConfigurationClientboundTagsTagsItemTagsItem{
							{TagName: "minecraft:logs", Entries: []int32{1, 2, 3}},
						},
					},
				},
			},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundTags{} },
		},
		{
			state: "configuration", direction: "toClient", packetName: "select_known_packs",
			value: &v26_1.ConfigurationClientboundSelectKnownPacks{
				Packs: []v26_1.ConfigurationClientboundSelectKnownPacksPacksItem{
					{Namespace: "minecraft", ID: "core", Version: "26.1"},
				},
			},
			fresh: func() packetCodec { return &v26_1.ConfigurationClientboundSelectKnownPacks{} },
		},
		{
			state: "configuration", direction: "toServer", packetName: "settings",
			value: &v26_1.ConfigurationServerboundSettings{
				Locale: "en_us", ViewDistance: 10, ChatFlags: 0, ChatColors: true,
				SkinParts: 0x7f, MainHand: 1, EnableTextFiltering: false,
				EnableServerListing: true, ParticleStatus: "all",
			},
			fresh: func() packetCodec { return &v26_1.ConfigurationServerboundSettings{} },
		},
		{
			state: "configuration", direction: "toServer", packetName: "custom_payload",
			value: &v26_1.ConfigurationServerboundCustomPayload{Channel: "minecraft:brand", Data: []byte("go")},
			fresh: func() packetCodec { return &v26_1.ConfigurationServerboundCustomPayload{} },
		},
		{
			state: "configuration", direction: "toServer", packetName: "finish_configuration",
			value: &v26_1.ConfigurationServerboundFinishConfiguration{},
			fresh: func() packetCodec { return &v26_1.ConfigurationServerboundFinishConfiguration{} },
		},
		{
			state: "configuration", direction: "toServer", packetName: "keep_alive",
			value: &v26_1.ConfigurationServerboundKeepAlive{KeepAliveID: 987654321},
			fresh: func() packetCodec { return &v26_1.ConfigurationServerboundKeepAlive{} },
		},
		{
			state: "configuration", direction: "toServer", packetName: "pong",
			value: &v26_1.ConfigurationServerboundPong{ID: 42},
			fresh: func() packetCodec { return &v26_1.ConfigurationServerboundPong{} },
		},
		{
			state: "configuration", direction: "toServer", packetName: "resource_pack_receive",
			value: &v26_1.ConfigurationServerboundResourcePackReceive{UUID: identity, Result: 3},
			fresh: func() packetCodec { return &v26_1.ConfigurationServerboundResourcePackReceive{} },
		},
		{
			state: "configuration", direction: "toServer", packetName: "select_known_packs",
			value: &v26_1.ConfigurationServerboundSelectKnownPacks{
				Packs: []v26_1.ConfigurationServerboundSelectKnownPacksPacksItem{
					{Namespace: "minecraft", ID: "core", Version: "26.1"},
				},
			},
			fresh: func() packetCodec { return &v26_1.ConfigurationServerboundSelectKnownPacks{} },
		},

		// Play, where the schema stops being flat.
		{
			state: "play", direction: "toClient", packetName: "bundle_delimiter",
			value: &v26_1.PlayClientboundBundleDelimiter{},
			fresh: func() packetCodec { return &v26_1.PlayClientboundBundleDelimiter{} },
		},
		{
			// Five entity-metadata serializer types in one packet, which is
			// what makes the terminated loop and its type mapper observable.
			state: "play", direction: "toClient", packetName: "entity_metadata",
			value: &v26_1.PlayClientboundEntityMetadata{
				EntityID: 42,
				Metadata: []v26_1.PlayClientboundEntityMetadataMetadataItem{
					{Key: 0, Type: "byte", Value: v26_1.PlayClientboundEntityMetadataMetadataItemValueSwitch{Byte: 0x20}},
					{Key: 1, Type: "int", Value: v26_1.PlayClientboundEntityMetadataMetadataItemValueSwitch{Int: 300}},
					{Key: 2, Type: "float", Value: v26_1.PlayClientboundEntityMetadataMetadataItemValueSwitch{Float: 0.5}},
					{Key: 3, Type: "string", Value: v26_1.PlayClientboundEntityMetadataMetadataItemValueSwitch{String: "hello"}},
					{Key: 4, Type: "boolean", Value: v26_1.PlayClientboundEntityMetadataMetadataItemValueSwitch{Boolean: true}},
				},
			},
			fresh: func() packetCodec { return &v26_1.PlayClientboundEntityMetadata{} },
		},
		{
			// A slot carrying data components, which reaches the recursive
			// part of the schema: a slot holds components, and a component can
			// hold a slot.
			state: "play", direction: "toClient", packetName: "set_slot",
			value: &v26_1.PlayClientboundSetSlot{
				WindowID: 0, StateID: 1, Slot: 36,
				Item: &v26_1.Slot{
					ItemCount: 2,
					AnonymousSwitch1: v26_1.SlotAnonymousSwitch1Switch{
						Default: v26_1.SlotAnonymousSwitch1SwitchDefault{
							ItemID:              1,
							AddedComponentCount: 2,
							Components: []*v26_1.SlotComponent{
								{Type: "max_stack_size", Data: v26_1.SlotComponentDataSwitch{MaxStackSize: 64}},
								{Type: "custom_name", Data: v26_1.SlotComponentDataSwitch{CustomName: component}},
							},
							RemoveComponents: []v26_1.SlotAnonymousSwitch1SwitchDefaultRemoveComponentsItem{},
						},
					},
				},
			},
			fresh: func() packetCodec { return &v26_1.PlayClientboundSetSlot{} },
		},
		{
			// The command tree, which exercises a bitfield whose members other
			// fields reference and a switch that discriminates on one of them.
			state: "play", direction: "toClient", packetName: "declare_commands",
			value: &v26_1.PlayClientboundDeclareCommands{
				Nodes: []v26_1.PlayClientboundDeclareCommandsNodesItem{
					{
						Flags:    v26_1.PlayClientboundDeclareCommandsNodesItemFlagsBits{CommandNodeType: 0},
						Children: []int32{1},
					},
					{
						Flags:    v26_1.PlayClientboundDeclareCommandsNodesItemFlagsBits{CommandNodeType: 1, HasCommand: 1},
						Children: []int32{},
						ExtraNodeData: v26_1.PlayClientboundDeclareCommandsNodesItemExtraNodeDataSwitch{
							Case1: v26_1.PlayClientboundDeclareCommandsNodesItemExtraNodeDataSwitchCase1{Name: "seed"},
						},
					},
				},
				RootIndex: 0,
			},
			fresh: func() packetCodec { return &v26_1.PlayClientboundDeclareCommands{} },
		},
		{
			// Both forms of a registry entry holder set, in the one place a
			// fixture can reach them without a whole world of context: an item
			// predicate names a tag, and another lists IDs.
			state: "play", direction: "toClient", packetName: "set_slot",
			packetLabel: "set_slot with holder sets",
			value: &v26_1.PlayClientboundSetSlot{
				WindowID: 0, StateID: 2, Slot: 0,
				Item: &v26_1.Slot{
					ItemCount: 1,
					AnonymousSwitch1: v26_1.SlotAnonymousSwitch1Switch{
						Default: v26_1.SlotAnonymousSwitch1SwitchDefault{
							ItemID:              1,
							AddedComponentCount: 2,
							Components: []*v26_1.SlotComponent{
								{Type: "can_place_on", Data: v26_1.SlotComponentDataSwitch{
									CanPlaceOn: v26_1.SlotComponentDataSwitchCanPlaceOn{
										Predicates: []*v26_1.ItemBlockPredicate{
											{BlockSet: holderSetTag(t, "minecraft:dirt"), Components: emptyMatchers()},
										},
									},
								}},
								{Type: "can_break", Data: v26_1.SlotComponentDataSwitch{
									CanBreak: v26_1.SlotComponentDataSwitchCanBreak{
										Predicates: []*v26_1.ItemBlockPredicate{
											{BlockSet: holderSetIDs(t, []int32{1, 2, 3}), Components: emptyMatchers()},
										},
									},
								}},
							},
							RemoveComponents: []v26_1.SlotAnonymousSwitch1SwitchDefaultRemoveComponentsItem{},
						},
					},
				},
			},
			fresh: func() packetCodec { return &v26_1.PlayClientboundSetSlot{} },
		},
		{
			state: "play", direction: "toServer", packetName: "keep_alive",
			value: &v26_1.PlayServerboundKeepAlive{KeepAliveID: 24680},
			fresh: func() packetCodec { return &v26_1.PlayServerboundKeepAlive{} },
		},
		{
			// A particle the schema names no switch case for, which is most of
			// them: 98 of protocol 775's 117 particle types carry no data, and
			// the switch has no default. Erroring on that combination read as
			// strictness and was a decoder that dropped the connection when
			// anything exploded. This fixture is here because the agreement is
			// the thing worth pinning: ProtoDef's compiler, which is what
			// node-minecraft-protocol runs, encodes a void default, so Go and
			// Node have to produce the same empty bytes for this packet.
			state: "play", direction: "toClient", packetName: "world_particles",
			packetLabel: "particle with no data case",
			value: &v26_1.PlayClientboundWorldParticles{
				LongDistance: true, X: 1, Y: 2, Z: 3, Amount: 4,
				Particle: v26_1.Particle{Type: "explosion_emitter"},
			},
			fresh: func() packetCodec { return &v26_1.PlayClientboundWorldParticles{} },
		},
		{
			// The other half of the same switch, so the fixture above cannot
			// pass by the encoder having stopped writing particle data at all.
			state: "play", direction: "toClient", packetName: "world_particles",
			packetLabel: "particle with a data case",
			value: &v26_1.PlayClientboundWorldParticles{
				LongDistance: true, X: 1, Y: 2, Z: 3, Amount: 4,
				Particle: v26_1.Particle{
					Type: "block",
					Data: v26_1.ParticleDataSwitch{Block: 5},
				},
			},
			fresh: func() packetCodec { return &v26_1.PlayClientboundWorldParticles{} },
		},
	}
}
