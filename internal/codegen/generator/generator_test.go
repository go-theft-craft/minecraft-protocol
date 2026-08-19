package generator

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol/internal/codegen/schema"
	"github.com/go-theft-craft/minecraft-protocol/source/manifest"
)

const sourceDir = "../../../source/java/1.8"

func TestManifestValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
		want   string
	}{
		{
			name: "changed checksum",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				path := filepath.Join(dir, "blocks.json")
				if err := os.WriteFile(path, []byte("[]\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "does not match its recorded checksum",
		},
		{
			name: "missing JSON file",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Remove(filepath.Join(dir, "blocks.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: "dataset blocks",
		},
		{
			name: "extra JSON file",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, "extra.json"), []byte("{}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "extra.json is not recorded in the manifest",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := copySource(t)
			test.mutate(t, dir)
			_, err := validateManifest(dir)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateManifest() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestManifestRejectsInvalidMetadata(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{name: "edition", mutate: func(m map[string]any) { m["edition"] = "bedrock" }, want: "unsupported edition"},
		{name: "manifest version", mutate: func(m map[string]any) { m["manifestVersion"] = float64(1) }, want: "unsupported manifest version"},
		{name: "protocol", mutate: func(m map[string]any) { m["protocol"] = float64(0) }, want: "protocol"},
		{name: "revision", mutate: func(m map[string]any) { m["sourceRevision"] = "unknown" }, want: "sourceRevision"},
		{name: "license", mutate: func(m map[string]any) { m["license"] = "" }, want: "license is required"},
		{name: "checksum", mutate: func(m map[string]any) {
			datasets := m["datasets"].([]any)
			datasets[0].(map[string]any)["sha256"] = "bad"
		}, want: "sha256"},
		{name: "unknown field", mutate: func(m map[string]any) { m["sourcePath"] = "data/pc/1.8" }, want: "parse manifest"},
	}

	// The target Minecraft version and the protocol number are no longer
	// pinned to 1.8.9 and 47 here: the manifest states them, and a second
	// version is the point of this milestone. What is still pinned is that a
	// generation run's version key must agree with the manifest, which
	// TestRunRejectsMismatchedStableVersionKey covers.

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dir := copySource(t)
			path := filepath.Join(dir, "manifest.json")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var document map[string]any
			if err := json.Unmarshal(raw, &document); err != nil {
				t.Fatal(err)
			}
			test.mutate(document)
			raw, err = json.MarshalIndent(document, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err = validateManifest(dir)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validateManifest() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestSchemaParsing(t *testing.T) {
	var drop schema.RawDrop
	if err := json.Unmarshal([]byte(`{"drop":{"id":5,"metadata":2},"minCount":1,"maxCount":3}`), &drop); err != nil {
		t.Fatal(err)
	}
	id, metadata, err := drop.Parse()
	if err != nil {
		t.Fatal(err)
	}
	if id != 5 || metadata != 2 || drop.MinCount == nil || *drop.MinCount != 1 || drop.MaxCount == nil || *drop.MaxCount != 3 {
		t.Fatalf("RawDrop.Parse() = %d, %d, min=%v, max=%v", id, metadata, drop.MinCount, drop.MaxCount)
	}

	blocks, err := loadBlocks([]byte(`[{"id":1,"name":"stone","harvestTools":{"257":true}}]`))
	if err != nil {
		t.Fatal(err)
	}
	parsedBlocks := blocks.([]blockTmpl)
	if got := parsedBlocks[0].HarvestTools[257]; !got {
		t.Fatal("string harvest-tool ID was not parsed")
	}

	ingredient := schema.ParseIngredient(json.RawMessage(`{"id":17,"metadata":null}`))
	if ingredient.Metadata != -1 {
		t.Fatalf("null ingredient metadata = %d, want -1", ingredient.Metadata)
	}
	if got := fixLogMeta(schema.RecipeIngredient{ID: 17, Metadata: 14}); got != 2 {
		t.Fatalf("fixLogMeta() = %d, want 2", got)
	}
}

func TestProtocolParsingAndSortedOutput(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(sourceDir, "protocol.json"))
	if err != nil {
		t.Fatal(err)
	}
	proto, err := loadProtocol(raw)
	if err != nil {
		t.Fatal(err)
	}

	var clientChat, serverChat *packetTmpl
	for _, phase := range proto.Phases {
		for index := range phase.ToClient {
			if phase.ToClient[index].Name == "chat" {
				clientChat = &phase.ToClient[index]
			}
		}
		for index := range phase.ToServer {
			if phase.ToServer[index].Name == "chat" {
				serverChat = &phase.ToServer[index]
			}
		}
	}
	if clientChat == nil || clientChat.ID != 0x02 || len(clientChat.Fields) != 2 {
		t.Fatalf("client chat = %+v", clientChat)
	}
	if serverChat == nil || serverChat.ID != 0x01 || !reflect.DeepEqual(serverChat.Fields, []packetFieldTmpl{{Name: "message", Type: "string"}}) {
		t.Fatalf("server chat = %+v", serverChat)
	}
	var legacyPing *packetTmpl
	for _, phase := range proto.Phases {
		for index := range phase.ToServer {
			if phase.ToServer[index].Name == "legacy_server_list_ping" {
				legacyPing = &phase.ToServer[index]
			}
		}
	}
	if legacyPing == nil || legacyPing.ID != 0xfe || !reflect.DeepEqual(legacyPing.Fields, []packetFieldTmpl{{Name: "payload", Type: "u8"}}) {
		t.Fatalf("legacy server-list ping summary = %+v", legacyPing)
	}
	packetStructs, err := loadPacketStructs(raw)
	if err != nil {
		t.Fatal(err)
	}
	for _, packet := range packetStructs.Packets {
		if packet.StructName == "LegacyServerListPing" {
			t.Fatal("legacy server-list ping was exposed as a normal PacketValue struct")
		}
	}

	materials, err := loadMaterials([]byte(`{"z":{"2":2},"a":{"1":1}}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := []string{materials[0].Name, materials[1].Name}; !reflect.DeepEqual(got, []string{"a", "z"}) {
		t.Fatalf("material order = %v", got)
	}
}

func TestProtocolParsingRejectsMalformedMappingsAndFields(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{
			name: "missing direction",
			mutate: func(document map[string]any) {
				delete(document["play"].(map[string]any), "toServer")
			},
			want: "phase play direction toServer is missing",
		},
		{
			name: "packet type without mapping",
			mutate: func(document map[string]any) {
				types := protocolFixtureTypes(document, "play", "toClient")
				types["packet_extra"] = []any{"container", []any{}}
			},
			want: "phase play direction toClient packet type \"packet_extra\" has no mapping",
		},
		{
			name: "malformed mapper",
			mutate: func(document map[string]any) {
				packet := protocolFixtureTypes(document, "status", "toClient")["packet"].([]any)
				fields := packet[1].([]any)
				fields[0].(map[string]any)["type"] = []any{"array", map[string]any{}}
			},
			want: "phase status direction toClient packet name type is \"array\", want mapper",
		},
		{
			name: "mapping without packet type",
			mutate: func(document map[string]any) {
				mapping, fields := protocolFixtureMapping(document, "status", "toServer")
				mapping["0x02"] = "missing"
				fields["missing"] = "packet_missing"
			},
			want: "phase status direction toServer packet mapping \"missing\" has no packet definition",
		},
		{
			name: "duplicate ID",
			mutate: func(document map[string]any) {
				types := protocolFixtureTypes(document, "login", "toClient")
				types["packet_second"] = []any{"container", []any{}}
				mapping, fields := protocolFixtureMapping(document, "login", "toClient")
				mapping["0x1"] = "second"
				fields["second"] = "packet_second"
			},
			want: "phase login direction toClient packet ID 1 is shared",
		},
		{
			name: "duplicate mapped name",
			mutate: func(document map[string]any) {
				mapping, _ := protocolFixtureMapping(document, "login", "toServer")
				mapping["0x02"] = "example"
			},
			want: "phase login direction toServer packet name \"example\" has duplicate IDs",
		},
		{
			name: "ID below range",
			mutate: func(document map[string]any) {
				mapping, _ := protocolFixtureMapping(document, "handshaking", "toServer")
				delete(mapping, "0x01")
				mapping["-1"] = "example"
			},
			want: "phase handshaking direction toServer packet mapping \"-1\" has an invalid ID",
		},
		{
			name: "ID above range",
			mutate: func(document map[string]any) {
				mapping, _ := protocolFixtureMapping(document, "handshaking", "toClient")
				delete(mapping, "0x01")
				mapping["0x80000000"] = "example"
			},
			want: "phase handshaking direction toClient packet mapping \"0x80000000\" has an invalid ID",
		},
		{
			name: "missing packet field type",
			mutate: func(document map[string]any) {
				types := protocolFixtureTypes(document, "play", "toServer")
				types["packet_example"] = []any{"container", []any{map[string]any{"name": "value"}}}
			},
			want: "phase play direction toServer packet example field \"value\" has no type",
		},
		{
			name: "duplicate packet field name",
			mutate: func(document map[string]any) {
				types := protocolFixtureTypes(document, "play", "toClient")
				types["packet_example"] = []any{"container", []any{
					map[string]any{"name": "value", "type": "varint"},
					map[string]any{"name": "value", "type": "string"},
				}}
			},
			want: "phase play direction toClient packet example has duplicate field \"value\"",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			document := validProtocolFixture()
			test.mutate(document)
			raw, err := json.Marshal(document)
			if err != nil {
				t.Fatal(err)
			}
			_, err = loadProtocol(raw)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("loadProtocol() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func TestGeneratedFilesMatchCheckpoint(t *testing.T) {
	out := t.TempDir()
	if err := Run(Config{SourceDir: sourceDir, OutDir: out, Package: "v1_8", Version: "java/1.8.9"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	wantDir := "../../../generated/java/v1_8"
	want := directoryFiles(t, wantDir)
	got := directoryFiles(t, filepath.Join(out, "v1_8"))
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("generated checkpoint mismatch\ngot:  %v\nwant: %v", got, want)
	}
}

func TestRunGeneratesStatefulPacketCodecInventory(t *testing.T) {
	out := t.TempDir()
	if err := Run(Config{SourceDir: sourceDir, OutDir: out, Package: "v1_8", Version: "java/1.8.9"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	generatedDir := filepath.Join(out, "v1_8")
	wants := map[string][]string{
		"packets.go": {
			"type StatusClientboundServerInfo struct",
			"func (StatusClientboundServerInfo) PacketID() int32",
		},
		"codec.go": {
			"func (packet *StatusClientboundServerInfo) Decode(buffer *java.Buffer) error",
			"func (packet *StatusClientboundServerInfo) Encode(buffer *java.Buffer) error",
		},
		"descriptor.go": {
			"var packetFactories = map[packetKey]packetFactory{",
			"func newPacket(state protocol.State, direction protocol.Direction, id int32) (packetCodec, bool)",
			"func packetKeyForValue(value packetCodec) (packetKey, bool)",
		},
		"protocol.go": {
			"StateHandshaking protocol.State = \"handshaking\"",
			"func Protocol() protocol.Protocol",
			"func (protocolDescriptor) NewSession(role protocol.Role, limits protocol.Limits) (protocol.Session, error)",
			"func (session *protocolSession) SetState(state protocol.State)",
			"func (session *protocolSession) DecodeFrame(framePayload []byte) (protocol.Packet, error)",
			"func (session *protocolSession) EncodeFrame(packet protocol.Packet) ([]byte, error)",
		},
	}
	for name, fragments := range wants {
		raw, err := os.ReadFile(filepath.Join(generatedDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		for _, fragment := range fragments {
			if !strings.Contains(string(raw), fragment) {
				t.Errorf("%s does not contain %q", name, fragment)
			}
		}
		for _, forbidden := range []string{`"reflect"`, "java.Marshal", "java.Unmarshal"} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("%s contains forbidden generated runtime reference %q", name, forbidden)
			}
		}
	}

	packets, err := os.ReadFile(filepath.Join(generatedDir, "packets.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(packets), "`mc:") {
		t.Fatal("packets.go still contains reflection codec tags")
	}

	for _, name := range []string{"packets.go", "codec.go", "descriptor.go"} {
		raw, err := os.ReadFile(filepath.Join(generatedDir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if strings.Contains(string(raw), "LegacyServerListPing") || strings.Contains(string(raw), "legacy_server_list_ping") {
			t.Errorf("%s exposes the unframed legacy server-list ping", name)
		}
		if name == "descriptor.go" {
			if count := strings.Count(string(raw), "func() packetCodec { return new("); count != 111 {
				t.Errorf("descriptor.go contains %d framed packet factories, want 111", count)
			}
		}
	}

	protocolSource, err := os.ReadFile(filepath.Join(generatedDir, "protocol.go"))
	if err != nil {
		t.Fatal(err)
	}
	runtimeSource, inventorySource, found := strings.Cut(string(protocolSource), "func newProtocol() data.Protocol")
	if !found {
		t.Fatal("protocol.go does not contain the data protocol inventory")
	}
	if strings.Contains(runtimeSource, "legacy_server_list_ping") {
		t.Fatal("protocol.go exposes the unframed legacy server-list ping through the framed packet name registry")
	}
	if !strings.Contains(inventorySource, `Name: "legacy_server_list_ping", ID: 254`) {
		t.Fatal("protocol.go omitted the legacy server-list ping from the data protocol inventory")
	}
}

func TestRunRejectsDuplicatePacketRegistryKeys(t *testing.T) {
	source := copySource(t)
	path := filepath.Join(source, "protocol.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	const mapping = `"0x00": "ping_start",`
	if count := bytes.Count(raw, []byte(mapping)); count != 1 {
		t.Fatalf("protocol fixture contains %d copies of %q, want 1", count, mapping)
	}
	raw = bytes.Replace(raw, []byte(mapping), []byte(mapping+"\n                    \"0\": \"ping_start\","), 1)
	rewriteSourceFile(t, source, "protocol", raw)

	out := t.TempDir()
	err = Run(Config{SourceDir: source, OutDir: out, Package: "v1_8", Version: "java/1.8.9"})
	if err == nil || !strings.Contains(err.Error(), "duplicate IDs") {
		t.Fatalf("Run() error = %v, want duplicate registry key", err)
	}
	if _, statErr := os.Stat(filepath.Join(out, "v1_8")); !os.IsNotExist(statErr) {
		t.Fatalf("generated output exists after duplicate registry key: %v", statErr)
	}
}

func TestRunAcceptsStableVersionKey(t *testing.T) {
	out := t.TempDir()
	if err := Run(Config{SourceDir: sourceDir, OutDir: out, Package: "v1_8", Version: "java/1.8.9"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	generatedDir := filepath.Join(out, "v1_8")
	gamedata, err := os.ReadFile(filepath.Join(generatedDir, "gamedata.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(gamedata), `data.Register("java/1.8.9", Data)`) {
		t.Fatalf("gamedata.go does not register the full stable key:\n%s", gamedata)
	}

	version, err := os.ReadFile(filepath.Join(generatedDir, "version.go"))
	if err != nil {
		t.Fatal(err)
	}
	// The gap between the name and its type is gofmt's, and it changes with
	// whatever else is in the const block. Matching it literally made a
	// comment added above VersionName fail this test.
	if !regexp.MustCompile(`VersionName\s+string = "1\.8\.9"`).Match(version) {
		t.Fatalf("version.go does not use the target as the public name:\n%s", version)
	}
}

func TestRunRejectsMismatchedStableVersionKey(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    string
	}{
		{name: "edition", version: "bedrock/1.8.9", want: `version edition "bedrock" does not match manifest edition "java"`},
		{name: "target", version: "java/1.9", want: `version target "1.9" does not match manifest target "1.8.9"`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := t.TempDir()
			err := Run(Config{SourceDir: sourceDir, OutDir: out, Package: "v1_8", Version: test.version})
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Run() error = %v, want containing %q", err, test.want)
			}
			if _, statErr := os.Stat(filepath.Join(out, "v1_8")); !os.IsNotExist(statErr) {
				t.Fatalf("output exists after rejected version key: %v", statErr)
			}
		})
	}
}

func TestRunRejectsInvalidPackageBeforeFilesystemMutation(t *testing.T) {
	tests := []string{"../victim", "type", "a/b", "a.b", "1package", "_"}
	for _, packageName := range tests {
		t.Run(packageName, func(t *testing.T) {
			root := t.TempDir()
			out := filepath.Join(root, "generated")
			victim := filepath.Join(root, "victim")
			if err := os.MkdirAll(victim, 0o755); err != nil {
				t.Fatal(err)
			}
			sentinel := filepath.Join(victim, "sentinel")
			if err := os.WriteFile(sentinel, []byte("keep"), 0o644); err != nil {
				t.Fatal(err)
			}

			config := Config{SourceDir: sourceDir, OutDir: out, Package: packageName, Version: "java/1.8.9"}
			for operationName, operation := range map[string]func(Config) error{"Run": Run, "Check": Check} {
				err := operation(config)
				if err == nil || !strings.Contains(err.Error(), "package") {
					t.Fatalf("%s() error = %v, want invalid package", operationName, err)
				}
			}
			if raw, readErr := os.ReadFile(sentinel); readErr != nil || string(raw) != "keep" {
				t.Fatalf("outside sentinel changed: data=%q error=%v", raw, readErr)
			}
			if _, statErr := os.Stat(out); !os.IsNotExist(statErr) {
				t.Fatalf("output directory was mutated: %v", statErr)
			}
		})
	}
}

func TestRunPreservesLastGoodOutputOnInvalidVerifiedSource(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "malformed verified JSON",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				rewriteSourceFile(t, dir, "blocks", []byte("{"))
			},
		},
		{
			name: "missing inventory file and entry",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Remove(filepath.Join(dir, "blocks.json")); err != nil {
					t.Fatal(err)
				}
				dropDataset(t, dir, "blocks")
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source := copySource(t)
			test.mutate(t, source)
			out := t.TempDir()
			target := filepath.Join(out, "v1_8")
			if err := os.Mkdir(target, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "last-good.go"), []byte("last good\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(target, "data_test.go"), []byte("hand written\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := snapshotTree(t, target)

			if err := Run(Config{SourceDir: source, OutDir: out, Package: "v1_8", Version: "java/1.8.9"}); err == nil {
				t.Fatal("Run() succeeded with invalid source")
			}
			after := snapshotTree(t, target)
			if !reflect.DeepEqual(after, before) {
				t.Fatalf("last good output changed\nbefore: %v\nafter:  %v", before, after)
			}
		})
	}
}

func TestRunPreservesApprovedTestsOnly(t *testing.T) {
	out := t.TempDir()
	target := filepath.Join(out, "v1_8")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	dataTest := []byte("package v1_8\n// hand written\n")
	if err := os.WriteFile(filepath.Join(target, "data_test.go"), dataTest, 0o644); err != nil {
		t.Fatal(err)
	}
	codecTest := []byte("package v1_8\n// hand written codec test\n")
	if err := os.WriteFile(filepath.Join(target, "codec_test.go"), codecTest, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "stale_test.go"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, "stale.go"), []byte("stale\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	config := Config{SourceDir: sourceDir, OutDir: out, Package: "v1_8", Version: "java/1.8.9"}
	if err := Run(config); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(target, "data_test.go")); err != nil || !reflect.DeepEqual(got, dataTest) {
		t.Fatalf("data_test.go = %q, %v", got, err)
	}
	if got, err := os.ReadFile(filepath.Join(target, "codec_test.go")); err != nil || !reflect.DeepEqual(got, codecTest) {
		t.Fatalf("codec_test.go = %q, %v", got, err)
	}
	if err := Check(config); err != nil {
		t.Fatalf("Check() rejected preserved generated tests: %v", err)
	}
	for _, stale := range []string{"stale_test.go", "stale.go"} {
		if _, err := os.Stat(filepath.Join(target, stale)); !os.IsNotExist(err) {
			t.Fatalf("%s survived regeneration: %v", stale, err)
		}
	}
}

func TestReplaceTargetRollsBackFailedInstall(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "v1_8")
	if err := os.Mkdir(target, 0o755); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(target, "sentinel")
	if err := os.WriteFile(sentinel, []byte("last good"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := replaceTarget(target, filepath.Join(root, "missing-staging"))
	if err == nil || !strings.Contains(err.Error(), "install generated package") {
		t.Fatalf("replaceTarget() error = %v", err)
	}
	if raw, readErr := os.ReadFile(sentinel); readErr != nil || string(raw) != "last good" {
		t.Fatalf("rollback did not restore target: data=%q error=%v", raw, readErr)
	}
}

func TestCheckDetectsGeneratedInventoryChanges(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
		want   string
	}{
		{name: "extra", mutate: func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "extra.go"), []byte("extra"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, want: "extra file extra.go"},
		{name: "extra test", mutate: func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "extra_test.go"), []byte("extra"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, want: "extra file extra_test.go"},
		{name: "missing", mutate: func(t *testing.T, dir string) {
			if err := os.Remove(filepath.Join(dir, "blocks.go")); err != nil {
				t.Fatal(err)
			}
		}, want: "missing blocks.go"},
		{name: "changed", mutate: func(t *testing.T, dir string) {
			if err := os.WriteFile(filepath.Join(dir, "blocks.go"), []byte("changed"), 0o644); err != nil {
				t.Fatal(err)
			}
		}, want: "changed blocks.go"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := t.TempDir()
			config := Config{SourceDir: sourceDir, OutDir: out, Package: "v1_8", Version: "java/1.8.9"}
			if err := Run(config); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, filepath.Join(out, "v1_8"))
			err := Check(config)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Check() error = %v, want containing %q", err, test.want)
			}
		})
	}
}

func copySource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(sourceDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func readManifest(t *testing.T, dir string) *manifest.Manifest {
	t.Helper()
	loaded, err := manifest.Load(dir)
	if err != nil {
		t.Fatal(err)
	}
	return loaded
}

func writeManifest(t *testing.T, dir string, loaded *manifest.Manifest) {
	t.Helper()
	raw, err := json.MarshalIndent(loaded, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, manifest.FileName), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

// dropDataset removes a dataset from the manifest, leaving the tree otherwise
// intact. The caller removes the file itself when it wants both gone.
func dropDataset(t *testing.T, dir, name string) {
	t.Helper()
	loaded := readManifest(t, dir)
	kept := loaded.Datasets[:0]
	for _, dataset := range loaded.Datasets {
		if dataset.Name != name {
			kept = append(kept, dataset)
		}
	}
	loaded.Datasets = kept
	writeManifest(t, dir, loaded)
}

// rewriteSourceFile replaces a dataset's bytes and re-records its checksum, so
// the tree still verifies and the generator sees the new content.
func rewriteSourceFile(t *testing.T, dir, name string, raw []byte) {
	t.Helper()
	loaded := readManifest(t, dir)
	dataset, ok := loaded.Dataset(name)
	if !ok {
		t.Fatalf("source tree has no dataset %q", name)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(dataset.File)), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	for index := range loaded.Datasets {
		if loaded.Datasets[index].Name == name {
			loaded.Datasets[index].SHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
		}
	}
	writeManifest(t, dir, loaded)
}

func snapshotTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	if err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(rel)] = raw
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}

func directoryFiles(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	result := make(map[string][]byte)
	err := filepath.WalkDir(dir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || slices.Contains(preservedGeneratedTestNames, entry.Name()) {
			return nil
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		result[filepath.ToSlash(rel)] = raw
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	keys := make([]string, 0, len(result))
	for key := range result {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	if len(keys) == 0 {
		t.Fatalf("%s contains no generated files", dir)
	}
	return result
}

func validProtocolFixture() map[string]any {
	document := map[string]any{"types": map[string]any{"varint": "native"}}
	for _, phase := range []string{"handshaking", "status", "login", "play"} {
		document[phase] = map[string]any{
			"toClient": map[string]any{"types": validProtocolFixtureTypes()},
			"toServer": map[string]any{"types": validProtocolFixtureTypes()},
		}
	}
	return document
}

func validProtocolFixtureTypes() map[string]any {
	return map[string]any{
		"packet_example": []any{"container", []any{map[string]any{"name": "value", "type": "varint"}}},
		"packet": []any{"container", []any{
			map[string]any{"name": "name", "type": []any{"mapper", map[string]any{"type": "varint", "mappings": map[string]any{"0x01": "example"}}}},
			map[string]any{"name": "params", "type": []any{"switch", map[string]any{"compareTo": "name", "fields": map[string]any{"example": "packet_example"}}}},
		}},
	}
}

func protocolFixtureTypes(document map[string]any, phase, direction string) map[string]any {
	return document[phase].(map[string]any)[direction].(map[string]any)["types"].(map[string]any)
}

func protocolFixtureMapping(document map[string]any, phase, direction string) (map[string]any, map[string]any) {
	packet := protocolFixtureTypes(document, phase, direction)["packet"].([]any)
	fields := packet[1].([]any)
	nameType := fields[0].(map[string]any)["type"].([]any)
	paramsType := fields[1].(map[string]any)["type"].([]any)
	mappings := nameType[1].(map[string]any)["mappings"].(map[string]any)
	switchFields := paramsType[1].(map[string]any)["fields"].(map[string]any)
	return mappings, switchFields
}

// TestVerifiedSourceProvidesExtractedPhysics pins the committed 1.8 tree: its
// measured physics dataset verifies against the manifest and reaches the
// generator by name, exactly like an upstream dataset.
func TestVerifiedSourceProvidesExtractedPhysics(t *testing.T) {
	source, err := loadVerifiedSource(copySource(t))
	if err != nil {
		t.Fatalf("loadVerifiedSource: %v", err)
	}

	body, err := source.dataset("physics")
	if err != nil {
		t.Fatalf("dataset(physics): %v", err)
	}
	if len(body) == 0 {
		t.Fatal("physics dataset is empty")
	}

	extracted := source.Manifest.Extracted
	if extracted == nil {
		t.Fatal("the 1.8 tree records no extracted provenance")
	}
	if extracted.MinecraftVersion != "1.8.9" || extracted.Side != "server" {
		t.Fatalf("extracted provenance = %+v", extracted)
	}
	if extracted.License == "" {
		t.Fatal("extracted provenance carries no license")
	}
}

// TestExtractedPhysicsChecksumIsEnforced proves the measured dataset is held to
// the same standard as upstream data: a changed byte fails verification.
func TestExtractedPhysicsChecksumIsEnforced(t *testing.T) {
	dir := copySource(t)
	if err := os.WriteFile(filepath.Join(dir, "physics.json"), []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err := validateManifest(dir)
	if err == nil || !strings.Contains(err.Error(), "does not match its recorded checksum") {
		t.Fatalf("validateManifest() error = %v, want a checksum mismatch", err)
	}
}

// TestRunWithoutPhysicsOmitsTheGeneratedFile pins that measured constants are
// optional. Only versions someone has run mcreference against have them, so a
// tree without a physics dataset must still generate rather than fail on a
// missing render-plan entry.
func TestRunWithoutPhysicsOmitsTheGeneratedFile(t *testing.T) {
	dir := copySource(t)
	// Every measured dataset goes, not only physics: dropping the manifest's
	// extracted section leaves any measured file behind unrecorded, which the
	// manifest is right to refuse.
	for _, measured := range []string{"physics.json", "blockMovement.json"} {
		if err := os.Remove(filepath.Join(dir, measured)); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	delete(manifest, "extracted")
	updated, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(updated, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	out := t.TempDir()
	if err := Run(Config{SourceDir: dir, OutDir: out, Package: "v1_8", Version: "java/1.8.9"}); err != nil {
		t.Fatalf("Run() without physics error = %v", err)
	}

	if _, err := os.Stat(filepath.Join(out, "v1_8", "physics.go")); !os.IsNotExist(err) {
		t.Fatalf("physics.go was generated for a tree with no physics dataset: %v", err)
	}

	gamedata, err := os.ReadFile(filepath.Join(out, "v1_8", "gamedata.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(gamedata), "newPhysics()") {
		t.Fatal("gamedata.go calls newPhysics() but no physics.go was generated")
	}
}

// TestBlockMovementRefusesAStateEncodingItCannotRead pins the one way this
// dataset fails silently.
//
// The measurement declares how a state identifier packs a block identifier,
// and 1.8.9 packs the same fact two ways: chunk data shifts the identifier left
// four, and the game's own getStateId puts the identifier in the low twelve
// bits. A generator that assumed the first and was handed the second would emit
// a registry that resolves every lookup and answers about the wrong block every
// time. Stopping is the only version of that failure anyone would notice.
func TestBlockMovementRefusesAStateEncodingItCannotRead(t *testing.T) {
	document := []byte(`{"version":"1.8.9","side":"server","jarSha256":"",
		"stateEncoding":"id|meta<<12","blocks":[{"id":1,"name":"minecraft:stone","blocksMovement":true}]}`)

	_, err := loadBlockMovement(document)
	if err == nil || !strings.Contains(err.Error(), "id|meta<<12") {
		t.Fatalf("loadBlockMovement() error = %v, want a refusal naming the encoding", err)
	}
}

// TestBlockMovementReadsTheMeasuredTree pins that the committed measurement
// reaches the generator by name and describes every block the version has.
func TestBlockMovementReadsTheMeasuredTree(t *testing.T) {
	source, err := loadVerifiedSource(copySource(t))
	if err != nil {
		t.Fatalf("loadVerifiedSource: %v", err)
	}
	body, err := source.dataset("blockMovement")
	if err != nil {
		t.Fatalf("dataset(blockMovement): %v", err)
	}

	loaded, err := loadBlockMovement(body)
	if err != nil {
		t.Fatalf("loadBlockMovement: %v", err)
	}
	movement, ok := loaded.(*blockMovementTmpl)
	if !ok {
		t.Fatalf("loadBlockMovement returned %T", loaded)
	}
	if got, want := len(movement.Blocks), 198; got != want {
		t.Fatalf("measured blocks = %d, want %d", got, want)
	}
	if movement.StateShift != chunkStateShift {
		t.Fatalf("state shift = %d, want %d", movement.StateShift, chunkStateShift)
	}
	for index := 1; index < len(movement.Blocks); index++ {
		if movement.Blocks[index-1].ID >= movement.Blocks[index].ID {
			t.Fatalf("blocks are not sorted by identifier at index %d", index)
		}
	}
}

// TestStrictDecodingRejectsAnUnknownField pins the rule that a dataset shape
// nobody modelled is an error rather than silence.
//
// It matters because the alternative is invisible. An upstream field this
// repository does not know about would simply not appear in the generated data,
// and nothing would say so — the generated package would look complete and be
// missing whatever that field described.
func TestStrictDecodingRejectsAnUnknownField(t *testing.T) {
	dir := copySource(t)

	raw, err := os.ReadFile(filepath.Join(dir, "biomes.json"))
	if err != nil {
		t.Fatal(err)
	}
	// A field neither supported version declares.
	mutated := bytes.Replace(raw, []byte(`"id":`), []byte(`"unmodelled_field":1,"id":`), 1)
	if bytes.Equal(mutated, raw) {
		t.Fatal("the biomes fixture has no id field to anchor on")
	}
	rewriteSourceFile(t, dir, "biomes", mutated)

	out := t.TempDir()
	err = Run(Config{SourceDir: dir, OutDir: out, Package: "v1_8", Version: "java/1.8.9"})
	if err == nil {
		t.Fatal("generation accepted a field it does not model")
	}
	if !strings.Contains(err.Error(), "biomes") {
		t.Errorf("error = %q, want it to name the dataset", err)
	}
	if !strings.Contains(err.Error(), "unmodelled_field") {
		t.Errorf("error = %q, want it to name the field", err)
	}
}

// TestStrictDecodingAcceptsBothVersionsShapes covers the reason the schema
// types carry the union of what the two versions publish: 1.8 blocks have
// metadata variations, 26.1 blocks have block states, and neither version's
// data may be rejected for lacking the other's fields.
func TestStrictDecodingAcceptsBothVersionsShapes(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		load func([]byte) (any, error)
	}{
		{
			name: "a 1.8 block with metadata variations",
			raw:  `[{"id":1,"name":"stone","displayName":"Stone","variations":[{"metadata":1,"displayName":"Granite"}]}]`,
			load: loadBlocks,
		},
		{
			name: "a 26.1 block with states",
			raw:  `[{"id":1,"name":"stone","displayName":"Stone","defaultState":1,"minStateId":1,"maxStateId":2,"states":[{"name":"snowy","type":"bool","num_values":2}]}]`,
			load: loadBlocks,
		},
		{
			name: "a 1.8 biome with precipitation and rainfall",
			raw:  `[{"id":0,"name":"ocean","displayName":"Ocean","precipitation":"rain","rainfall":0.5,"climates":null}]`,
			load: loadBiomes,
		},
		{
			name: "a 26.1 biome with has_precipitation",
			raw:  `[{"id":0,"name":"ocean","displayName":"Ocean","has_precipitation":true}]`,
			load: loadBiomes,
		},
		{
			name: "a 26.1 entity with metadata keys",
			raw:  `[{"id":0,"name":"allay","displayName":"Allay","type":"mob","metadataKeys":["shared_flags","air_supply"]}]`,
			load: loadEntities,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.load([]byte(test.raw)); err != nil {
				t.Errorf("load: %v", err)
			}
		})
	}
}

// TestBothPinnedTreesDecode loads every dataset the shared loaders cover from
// both pinned trees.
//
// It is the check that strict decoding is a working rule rather than a claim.
// Each loader is the real one, and each tree is the real pinned data, so a
// shape difference between the two versions fails here rather than at the point
// someone tries to generate the second version.
func TestBothPinnedTreesDecode(t *testing.T) {
	loaders := map[string]func([]byte) (any, error){
		"blocks":       loadBlocks,
		"items":        loadItems,
		"entities":     loadEntities,
		"biomes":       loadBiomes,
		"effects":      loadEffects,
		"enchantments": loadEnchantments,
		"foods":        loadFoods,
		"particles":    loadParticles,
		"instruments":  loadInstruments,
		"attributes":   loadAttributes,
		"windows":      loadWindows,
	}

	trees := map[string]string{
		"java 1.8":  sourceDir,
		"java 26.1": "../../../source/java/26.1/data",
	}

	for tree, base := range trees {
		t.Run(tree, func(t *testing.T) {
			for name, load := range loaders {
				raw, err := os.ReadFile(filepath.Join(base, name+".json"))
				if err != nil {
					t.Errorf("%s: %v", name, err)

					continue
				}
				if _, err := load(raw); err != nil {
					t.Errorf("%s: %v", name, err)
				}
			}
		})
	}
}

// TestBlockDropsAcceptBothUpstreamShapes pins the difference directly. Java 1.8
// wraps a drop in an object carrying optional counts; Java 26.1 lists bare item
// IDs.
func TestBlockDropsAcceptBothUpstreamShapes(t *testing.T) {
	cases := []struct {
		name         string
		raw          string
		wantID       int
		wantMetadata int
	}{
		{
			name:   "a 26.1 bare item ID",
			raw:    `[{"id":1,"name":"stone","displayName":"Stone","drops":[35]}]`,
			wantID: 35,
		},
		{
			name:   "a 1.8 wrapped ID",
			raw:    `[{"id":1,"name":"stone","displayName":"Stone","drops":[{"drop":4}]}]`,
			wantID: 4,
		},
		{
			name:         "a 1.8 wrapped object with metadata",
			raw:          `[{"id":1,"name":"stone","displayName":"Stone","drops":[{"drop":{"id":3,"metadata":2}}]}]`,
			wantID:       3,
			wantMetadata: 2,
		},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			loaded, err := loadBlocks([]byte(test.raw))
			if err != nil {
				t.Fatalf("loadBlocks: %v", err)
			}
			blocks := loaded.([]blockTmpl)
			if len(blocks) != 1 || len(blocks[0].Drops) != 1 {
				t.Fatalf("blocks = %+v", blocks)
			}
			drop := blocks[0].Drops[0]
			if drop.ID != test.wantID || drop.Metadata != test.wantMetadata {
				t.Errorf("drop = {ID:%d Metadata:%d}, want {ID:%d Metadata:%d}",
					drop.ID, drop.Metadata, test.wantID, test.wantMetadata)
			}
		})
	}
}

// TestEntityTypesAreAClosedSet keeps the 26.1 classification from becoming
// free text. A value nobody has seen still fails.
func TestEntityTypesAreAClosedSet(t *testing.T) {
	if _, err := loadEntities([]byte(`[{"id":0,"name":"x","displayName":"X","type":"other"}]`)); err != nil {
		t.Errorf(`loadEntities rejected "other", which Java 26.1 uses: %v`, err)
	}

	_, err := loadEntities([]byte(`[{"id":0,"name":"x","displayName":"X","type":"unheard_of"}]`))
	if err == nil {
		t.Fatal("loadEntities accepted a classification nothing declares")
	}
	if !strings.Contains(err.Error(), "unheard_of") {
		t.Errorf("error = %q, want it to name the type", err)
	}
}

// modernSourceDir is the pinned Java 26.1 tree. The tests below read it
// directly rather than through a fixture, because the point of a typed model
// is that it describes what upstream actually published.
const modernSourceDir = "../../../source/java/26.1/data"

func loadModernDataset[T any](t *testing.T, name string, load func([]byte) (any, error)) T {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(modernSourceDir, name+".json"))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	loaded, err := load(raw)
	if err != nil {
		t.Fatalf("load %s: %v", name, err)
	}
	value, ok := loaded.(T)
	if !ok {
		t.Fatalf("load %s returned %T", name, loaded)
	}

	return value
}

// TestModernBlocksCarryStates pins one block against upstream rather than
// against a count. A count survives a template that drops every state; a named
// block with its state range does not.
func TestModernBlocksCarryStates(t *testing.T) {
	blocks := loadModernDataset[[]blockTmpl](t, "blocks", loadBlocks)
	if len(blocks) != 1168 {
		t.Errorf("blocks = %d, want 1168", len(blocks))
	}

	var diorite *blockTmpl
	for index := range blocks {
		if blocks[index].Name == "polished_diorite" {
			diorite = &blocks[index]

			break
		}
	}
	if diorite == nil {
		t.Fatal("blocks has no polished_diorite")
	}
	if diorite.ID != 5 || diorite.MinStateID != 5 || diorite.MaxStateID != 5 {
		t.Errorf("polished_diorite = {ID:%d Min:%d Max:%d}, want {5 5 5}", diorite.ID, diorite.MinStateID, diorite.MaxStateID)
	}
	if len(diorite.States) != 0 {
		t.Errorf("polished_diorite states = %d, want 0", len(diorite.States))
	}
	if len(diorite.HarvestTools) != 7 {
		t.Errorf("polished_diorite harvest tools = %d, want 7", len(diorite.HarvestTools))
	}
}

// TestModernEntitiesCarryMetadataKeys pins the field Java 1.8 has no notion of.
func TestModernEntitiesCarryMetadataKeys(t *testing.T) {
	entities := loadModernDataset[[]entityTmpl](t, "entities", loadEntities)

	for _, entity := range entities {
		if entity.Name != "acacia_boat" {
			continue
		}
		if len(entity.MetadataKeys) != 14 {
			t.Errorf("acacia_boat metadata keys = %d, want 14", len(entity.MetadataKeys))
		}
		if len(entity.MetadataKeys) > 0 && entity.MetadataKeys[0] != "shared_flags" {
			t.Errorf("acacia_boat first metadata key = %q, want shared_flags", entity.MetadataKeys[0])
		}

		return
	}
	t.Fatal("entities has no acacia_boat")
}

// TestModernItemsAndLanguageMatchUpstream keeps the two datasets whose shape
// did not change honest about their contents.
func TestModernItemsAndLanguageMatchUpstream(t *testing.T) {
	items := loadModernDataset[[]schema.Item](t, "items", loadItems)
	if len(items) != 1506 {
		t.Errorf("items = %d, want 1506", len(items))
	}

	raw, err := os.ReadFile(filepath.Join(modernSourceDir, "language.json"))
	if err != nil {
		t.Fatal(err)
	}
	language, err := loadLanguage(raw)
	if err != nil {
		t.Fatalf("loadLanguage: %v", err)
	}
	if len(language) != 7886 {
		t.Errorf("language keys = %d, want 7886", len(language))
	}
}

// TestModernRecipesAreKeyedByResult pins that a recipe result is an item and a
// count. Metadata is what the flattening removed.
func TestModernRecipesAreKeyedByResult(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(modernSourceDir, "recipes.json"))
	if err != nil {
		t.Fatal(err)
	}
	recipes, err := loadRecipes(raw)
	if err != nil {
		t.Fatalf("loadRecipes: %v", err)
	}
	if len(recipes) != 887 {
		t.Errorf("recipe results = %d, want 887", len(recipes))
	}
	first := recipes[0]
	if first.ID != 2 {
		t.Errorf("first recipe result ID = %d, want 2", first.ID)
	}
	if len(first.Recipes) != 1 {
		t.Fatalf("recipes for result 2 = %d, want 1", len(first.Recipes))
	}
	result := first.Recipes[0].Result
	if result.ID != 2 || result.Count != 1 || result.Metadata != 0 {
		t.Errorf("result = %+v, want {ID:2 Count:1 Metadata:0}", result)
	}
}

// TestModernWindowsKeepTheAliasedShape records that windows is not a Java 26.1
// dataset at all: the pinned tree resolves it to Java 1.16.1, whose windows
// carry a namespaced string ID.
func TestModernWindowsKeepTheAliasedShape(t *testing.T) {
	windows := loadModernDataset[[]schema.Window](t, "windows", loadWindows)
	if len(windows) != 14 {
		t.Errorf("windows = %d, want 14", len(windows))
	}

	for _, window := range windows {
		if window.Name == "Anvil" {
			if window.ID != "minecraft:anvil" {
				t.Errorf("anvil window ID = %q, want minecraft:anvil", window.ID)
			}

			return
		}
	}
	t.Fatal("windows has no Anvil")
}

// TestModernSoundsAndMapIcons covers the two datasets with no Java 1.8
// equivalent whose shape is a flat record.
func TestModernSoundsAndMapIcons(t *testing.T) {
	sounds := loadModernDataset[[]schema.Sound](t, "sounds", loadSounds)
	if len(sounds) != 1902 {
		t.Errorf("sounds = %d, want 1902", len(sounds))
	}
	if len(sounds) > 0 && (sounds[0].ID != 1 || sounds[0].Name != "entity.allay.ambient_with_item") {
		t.Errorf("first sound = %+v, want {1 entity.allay.ambient_with_item}", sounds[0])
	}

	icons := loadModernDataset[[]schema.MapIcon](t, "mapIcons", loadMapIcons)
	if len(icons) != 34 {
		t.Errorf("map icons = %d, want 34", len(icons))
	}
	if len(icons) > 0 && (icons[0].Name != "player" || icons[0].VisibleInItemFrame) {
		t.Errorf("first map icon = %+v, want the player marker, not visible in an item frame", icons[0])
	}
}

// TestModernLootTablesCarryConditions pins both the conditions upstream
// attaches to a drop and the open-ended stack size range, which is the shape
// that would otherwise be read as a zero.
func TestModernLootTablesCarryConditions(t *testing.T) {
	blockLoot := loadModernDataset[[]lootTableTmpl](t, "blockLoot", loadBlockLoot)
	if len(blockLoot) != 925 {
		t.Errorf("block loot tables = %d, want 925", len(blockLoot))
	}

	var mushroom *lootTableTmpl
	for index := range blockLoot {
		if blockLoot[index].Subject == "brown_mushroom_block" {
			mushroom = &blockLoot[index]

			break
		}
	}
	if mushroom == nil {
		t.Fatal("block loot has no brown_mushroom_block")
	}
	if len(mushroom.Drops) != 2 {
		t.Fatalf("brown_mushroom_block drops = %d, want 2", len(mushroom.Drops))
	}
	if !mushroom.Drops[0].SilkTouch {
		t.Error("the first brown_mushroom_block drop should require silk touch")
	}
	open := mushroom.Drops[1]
	if !open.NoSilkTouch {
		t.Error("the second brown_mushroom_block drop should forbid silk touch")
	}
	if !open.HasMinStackSize || open.MinStackSize != 0 {
		t.Errorf("open drop min = {%d present:%t}, want 0 present", open.MinStackSize, open.HasMinStackSize)
	}
	if open.HasMaxStackSize {
		t.Error("the open drop should have no maximum stack size")
	}

	entityLoot := loadModernDataset[[]lootTableTmpl](t, "entityLoot", loadEntityLoot)
	if len(entityLoot) != 81 {
		t.Errorf("entity loot tables = %d, want 81", len(entityLoot))
	}
	for _, table := range entityLoot {
		if table.Subject != "blaze" {
			continue
		}
		if len(table.Drops) != 1 || table.Drops[0].Item != "blaze_rod" || !table.Drops[0].PlayerKill {
			t.Errorf("blaze drops = %+v, want one player-kill blaze rod", table.Drops)
		}

		return
	}
	t.Fatal("entity loot has no blaze")
}

// TestModernCommandTree pins the catalogue size and the tree's root, and that
// an argument node carries its parser.
func TestModernCommandTree(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(modernSourceDir, "commands.json"))
	if err != nil {
		t.Fatal(err)
	}
	tree, err := loadCommands(raw)
	if err != nil {
		t.Fatalf("loadCommands: %v", err)
	}
	if len(tree.Parsers) != 75 {
		t.Errorf("parsers = %d, want 75", len(tree.Parsers))
	}
	if tree.Root.Type != "root" || len(tree.Root.Children) == 0 {
		t.Fatalf("root = {Type:%q children:%d}, want a root with children", tree.Root.Type, len(tree.Root.Children))
	}

	var arguments int
	var walk func(commandNodeTmpl)
	walk = func(node commandNodeTmpl) {
		if node.Type == "argument" {
			arguments++
			if node.Parser == nil {
				t.Errorf("argument node %q has no parser", node.Name)
			}
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(tree.Root)
	if arguments == 0 {
		t.Error("the command tree has no argument nodes")
	}
}

// TestModernLoginPacketAndTints covers the two remaining datasets: the sample
// login packet, whose registry payload is kept as bytes, and the tints, whose
// redstone category keys by power level rather than by biome name.
func TestModernLoginPacketAndTints(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(modernSourceDir, "loginPacket.json"))
	if err != nil {
		t.Fatal(err)
	}
	packet, err := loadLoginPacket(raw)
	if err != nil {
		t.Fatalf("loadLoginPacket: %v", err)
	}
	if packet.EntityID != 1 || packet.WorldState.Name != "minecraft:overworld" {
		t.Errorf("login packet = {EntityID:%d World:%q}, want {1 minecraft:overworld}", packet.EntityID, packet.WorldState.Name)
	}
	if packet.HashedSeed != [2]int32{-517260770, -1150015120} {
		t.Errorf("hashed seed = %v, want [-517260770 -1150015120]", packet.HashedSeed)
	}
	if !json.Valid([]byte(packet.DimensionCodec)) {
		t.Error("the compacted dimension codec is not valid JSON")
	}

	raw, err = os.ReadFile(filepath.Join(modernSourceDir, "tints.json"))
	if err != nil {
		t.Fatal(err)
	}
	tints, err := loadTints(raw)
	if err != nil {
		t.Fatalf("loadTints: %v", err)
	}
	names := make([]string, len(tints))
	for index, category := range tints {
		names[index] = category.Name
	}
	want := []string{"constant", "foliage", "grass", "redstone", "water"}
	if !slices.Equal(names, want) {
		t.Errorf("tint categories = %v, want %v", names, want)
	}
	for _, category := range tints {
		if category.Name != "redstone" {
			continue
		}
		if len(category.Data) == 0 || len(category.Data[0].Keys) == 0 {
			t.Fatal("the redstone tint category is empty")
		}
		if _, err := strconv.Atoi(category.Data[0].Keys[0]); err != nil {
			t.Errorf("redstone tint key = %q, want a power level", category.Data[0].Keys[0])
		}
	}
}

// TestBlockMovementReadsAStateKeyedMeasurement pins the second shape this
// dataset comes in, and the template that goes with it.
//
// The encoding decides the whole registry, not a detail of it: one version's
// answer hangs off the block and the other's off the state, so they generate
// different code from different templates. Picking the template from the render
// plan rather than from the document is what would put a state-keyed
// measurement through a block-keyed template, which compiles.
func TestBlockMovementReadsAStateKeyedMeasurement(t *testing.T) {
	document := []byte(`{"version":"26.1.2","side":"server","jarSha256":"",
		"stateEncoding":"block-state-registry","blocks":[
			{"id":0,"name":"minecraft:air","blocksMovement":false,
			 "stateRange":{"from":0,"to":0}},
			{"id":1,"name":"minecraft:wall","blocksMovement":true,
			 "stateRange":{"from":1,"to":4},
			 "stateExceptions":[{"state":3,"blocksMovement":false}]}]}`)

	loaded, err := loadBlockMovement(document)
	if err != nil {
		t.Fatalf("loadBlockMovement: %v", err)
	}
	movement, ok := loaded.(*blockMovementTmpl)
	if !ok {
		t.Fatalf("loadBlockMovement returned %T", loaded)
	}
	if got := movement.templateName(); got != "block_movement_states.go.tmpl" {
		t.Fatalf("template = %q, want the state-keyed one", got)
	}
	if got, want := len(movement.Ranges), 2; got != want {
		t.Fatalf("ranges = %d, want %d", got, want)
	}
	if got, want := len(movement.Exceptions), 1; got != want {
		t.Fatalf("exceptions = %d, want %d", got, want)
	}
	// The block carrying an exception must be marked, or the generated byID
	// table answers for a block whose states disagree.
	if !movement.Blocks[1].Mixed {
		t.Error("a block with a state exception is not marked mixed")
	}
	if movement.Blocks[0].Mixed {
		t.Error("a block with no exceptions is marked mixed")
	}
	if movement.StateShift != 0 {
		t.Errorf("state shift = %d, want none for an encoding with no shift", movement.StateShift)
	}
}

// TestBlockMovementRefusesAGapInTheStates pins the check the generated lookup
// depends on.
//
// The lookup is a binary search over ranges. A gap does not break it; it makes
// a real state report unmeasured, which every consumer reads as "refuse to walk
// here" — a bot stopping in front of nothing, with no error anywhere.
func TestBlockMovementRefusesAGapInTheStates(t *testing.T) {
	document := []byte(`{"version":"26.1.2","side":"server","jarSha256":"",
		"stateEncoding":"block-state-registry","blocks":[
			{"id":0,"name":"minecraft:air","blocksMovement":false,
			 "stateRange":{"from":0,"to":0}},
			{"id":1,"name":"minecraft:stone","blocksMovement":true,
			 "stateRange":{"from":2,"to":2}}]}`)

	_, err := loadBlockMovement(document)
	if err == nil || !strings.Contains(err.Error(), "state 1 is not described") {
		t.Fatalf("loadBlockMovement() error = %v, want it to name the undescribed state", err)
	}
}

// TestBlockMovementRefusesStateDetailForAShiftedEncoding pins the other
// direction: a shifted encoding derives the block by arithmetic, so a range
// would be a second answer to a question that already has one.
func TestBlockMovementRefusesStateDetailForAShiftedEncoding(t *testing.T) {
	document := []byte(`{"version":"1.8.9","side":"server","jarSha256":"",
		"stateEncoding":"id<<4|meta","blocks":[
			{"id":1,"name":"minecraft:stone","blocksMovement":true,
			 "stateRange":{"from":16,"to":31}}]}`)

	_, err := loadBlockMovement(document)
	if err == nil || !strings.Contains(err.Error(), "per-state detail") {
		t.Fatalf("loadBlockMovement() error = %v, want a refusal", err)
	}
}
