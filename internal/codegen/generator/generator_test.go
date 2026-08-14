package generator

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol/internal/codegen/schema"
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
			want: "checksum mismatch for blocks.json",
		},
		{
			name: "missing JSON file",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Remove(filepath.Join(dir, "blocks.json")); err != nil {
					t.Fatal(err)
				}
			},
			want: "missing manifest file blocks.json",
		},
		{
			name: "extra JSON file",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(dir, "extra.json"), []byte("{}\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			want: "unexpected JSON file extra.json",
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
		{name: "target version", mutate: func(m map[string]any) { m["targetMinecraftVersion"] = "1.9" }, want: "unsupported target Minecraft version"},
		{name: "source version", mutate: func(m map[string]any) { m["sourceMinecraftVersion"] = "1.8.9" }, want: "unsupported source Minecraft version"},
		{name: "protocol", mutate: func(m map[string]any) { m["protocol"] = float64(48) }, want: "unsupported protocol"},
		{name: "repository", mutate: func(m map[string]any) { m["sourceRepository"] = "https://example.invalid" }, want: "unsupported source repository"},
		{name: "revision", mutate: func(m map[string]any) { m["sourceRevision"] = "unknown" }, want: "unsupported source revision"},
		{name: "source path", mutate: func(m map[string]any) { m["sourcePath"] = "data/pc/latest" }, want: "unsupported source path"},
		{name: "license", mutate: func(m map[string]any) { m["license"] = "unknown" }, want: "unsupported source license"},
		{name: "checksum", mutate: func(m map[string]any) {
			files := m["files"].(map[string]any)
			files["blocks.json"] = "bad"
		}, want: "malformed checksum for blocks.json"},
	}

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
			"func (codec *protocolCodec) SetState(state protocol.State) error",
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
	rewriteSourceFile(t, source, "protocol.json", raw)

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
	if !strings.Contains(string(version), `VersionName     string = "1.8.9"`) {
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
				rewriteSourceFile(t, dir, "blocks.json", []byte("{"))
			},
		},
		{
			name: "missing inventory file and entry",
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Remove(filepath.Join(dir, "blocks.json")); err != nil {
					t.Fatal(err)
				}
				manifest := readManifest(t, dir)
				delete(manifest.Files, "blocks.json")
				writeManifest(t, dir, manifest)
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

func readManifest(t *testing.T, dir string) sourceManifest {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest sourceManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func writeManifest(t *testing.T, dir string, manifest sourceManifest) {
	t.Helper()
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func rewriteSourceFile(t *testing.T, dir, name string, raw []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := readManifest(t, dir)
	manifest.Files[name] = fmt.Sprintf("%x", sha256.Sum256(raw))
	writeManifest(t, dir, manifest)
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
