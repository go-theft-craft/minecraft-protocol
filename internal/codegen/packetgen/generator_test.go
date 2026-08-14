package packetgen

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGenerateProducesExactFormattedSource(t *testing.T) {
	model := scalarGenerationModel()

	files, err := Generate(model, Options{PackageName: "fixture"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	want := map[string]string{
		"packets.go":    scalarPacketsGolden,
		"codec.go":      scalarCodecGolden,
		"descriptor.go": scalarDescriptorGolden,
	}
	if len(files) != len(want) {
		t.Fatalf("Generate() returned %d files, want %d", len(files), len(want))
	}
	for name, expected := range want {
		actual, ok := files[name]
		if !ok {
			t.Errorf("Generate() omitted %q", name)
			continue
		}
		if string(actual) != expected {
			t.Errorf("%s mismatch\n--- got ---\n%s\n--- want ---\n%s", name, actual, expected)
		}
	}
}

func TestGenerateRendersEveryModelConstruct(t *testing.T) {
	model, err := Build(parseFixture(t, modelFixture), Options{PackageName: "fixture"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	files, err := Generate(model, Options{})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	joined := string(files["packets.go"]) + string(files["codec.go"]) + string(files["descriptor.go"])

	for _, exact := range []string{
		"type PlayClientboundComplexNested struct {\n\tEnabled bool\n}",
		"type PlayClientboundComplexChoiceSwitch struct {\n\tText string\n}",
		"type PlayClientboundComplexBitsBits struct {\n\tHigh uint8\n\tLow  int8\n}",
		"type PlayClientboundComplexFlagsFlags struct {\n\tFirst  bool\n\tSecond bool\n}",
		`value1, err := buffer.ReadVarInt("play.toClient.complex.kind")`,
		`return fmt.Errorf("field play.toClient.complex.kind: unknown mapper wire value %v", value1)`,
		`return fmt.Errorf("field play.toClient.complex.kind: unknown mapper symbol %q", packet.Kind)`,
		`count8, err := buffer.ReadCollectionLength("play.toClient.complex.records")`,
		`if err := buffer.ValidateCollection("play.toClient.complex.values", count6); err != nil {`,
		`present12, err := buffer.ReadBool("play.toClient.complex.maybe")`,
		`switch packet.Kind {`,
		`case "text":`,
		`packed22, err := buffer.ReadU8("play.toClient.complex.bits")`,
		`packet.Flags.First = packed25&uint8(0x1) != 0`,
		`return fmt.Errorf("field play.toClient.complex.bits.high: bitfield value %v does not fit 3 bits", packet.Bits.High)`,
		`if err := buffer.WriteCollectionLength("play.toClient.complex.records", count4); err != nil {`,
		`if err := buffer.WriteI32("play.toClient.second.value", packet.Value); err != nil {`,
		`{State: protocol.State("play"), Direction: protocol.DirectionClientbound, ID: 0}: func() packetCodec { return new(PlayClientboundComplex) },`,
	} {
		if !strings.Contains(joined, exact) {
			t.Errorf("generated source does not contain exact construct:\n%s", exact)
		}
	}
}

func TestGenerateRendersTypedScalarCollectionCounts(t *testing.T) {
	model, err := Build(
		parseFixture(t, strings.Replace(modelFixture, `"countType":"varint","type":["container"`, `"countType":"i32","type":["container"`, 1)),
		Options{PackageName: "fixture"},
	)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	files, err := Generate(model, Options{})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	codec := string(files["codec.go"])
	for _, exact := range []string{
		`wireCount8, err := buffer.ReadI32("play.toClient.complex.records")`,
		`if err := buffer.ValidateCollection("play.toClient.complex.records", count9); err != nil {`,
		`if err := buffer.WriteI32("play.toClient.complex.records", int32(count4)); err != nil {`,
	} {
		if !strings.Contains(codec, exact) {
			t.Errorf("generated codec does not contain typed count operation:\n%s", exact)
		}
	}
}

func TestGenerateCompilesRepresentativePackageWithoutReflection(t *testing.T) {
	model, err := Build(
		parseFixture(t, strings.Replace(modelFixture, `"countType":"varint","type":["container"`, `"countType":"i32","type":["container"`, 1)),
		Options{PackageName: "fixture"},
	)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	files, err := Generate(model, Options{})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	for name, source := range files {
		for _, forbidden := range []string{"reflect", "java.Marshal", "java.Unmarshal"} {
			if bytes.Contains(source, []byte(forbidden)) {
				t.Errorf("%s contains forbidden reference %q", name, forbidden)
			}
		}
	}
	compileGeneratedPackage(t, files)
}

func compileGeneratedPackage(t *testing.T, files map[string][]byte) {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	temporary := t.TempDir()
	module := fmt.Sprintf("module packetgentest\n\ngo 1.26.5\n\nrequire github.com/go-theft-craft/minecraft-protocol v0.0.0\n\nreplace github.com/go-theft-craft/minecraft-protocol => %s\n", filepath.ToSlash(root))
	if err := os.WriteFile(filepath.Join(temporary, "go.mod"), []byte(module), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, source := range files {
		if err := os.WriteFile(filepath.Join(temporary, name), source, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	command := exec.Command("go", "test", "./...")
	command.Dir = temporary
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("generated package did not compile: %v\n%s", err, output)
	}
}

func TestGeneratePinnedJava18Protocol(t *testing.T) {
	raw, err := os.ReadFile("../../../source/java/1.8/protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	model, err := Build(parseFixture(t, string(raw)), Options{PackageName: "v1_8"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	files, err := Generate(model, Options{})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("Generate() returned %d files, want 3", len(files))
	}
	compileGeneratedPackage(t, files)
	codec := string(files["codec.go"])
	for _, exact := range []string{
		`buffer.ReadI32("play.toClient.update_attributes.properties")`,
		`buffer.ValidateCollection("play.toClient.update_attributes.properties",`,
		`buffer.WriteI32("play.toClient.update_attributes.properties", int32(`,
		`buffer.ReadI16("play.toClient.window_items.items")`,
		`buffer.ValidateCollection("play.toClient.window_items.items",`,
		`buffer.WriteI16("play.toClient.window_items.items", int16(`,
	} {
		if !strings.Contains(codec, exact) {
			t.Errorf("generated Java 1.8 codec does not contain typed count operation:\n%s", exact)
		}
	}
}

func TestGenerateRejectsDuplicateFactoryKeys(t *testing.T) {
	model := scalarGenerationModel()
	model.Factories = append(model.Factories, model.Factories[0])

	_, err := Generate(model, Options{PackageName: "fixture"})
	if err == nil {
		t.Fatal("Generate() error = nil")
	}
	if got := err.Error(); !strings.Contains(got, "duplicate packet factory") || !strings.Contains(got, "play") || !strings.Contains(got, "0x01") {
		t.Fatalf("Generate() error = %q", got)
	}
}

func TestGenerateRejectsLogicalDuplicateFactoryWithEquivalentExpressions(t *testing.T) {
	model := scalarGenerationModel()
	duplicate := model.Factories[0]
	duplicate.StateValue = `"pl" + "ay"`
	duplicate.DirectionValue = `protocol.Direction(1)`
	model.Factories = append(model.Factories, duplicate)

	_, err := Generate(model, Options{PackageName: "fixture"})
	if err == nil {
		t.Fatal("Generate() error = nil")
	}
	if got := err.Error(); !strings.Contains(got, "duplicate packet factory") || !strings.Contains(got, "play") || !strings.Contains(got, "toClient") || !strings.Contains(got, "0x01") {
		t.Fatalf("Generate() error = %q", got)
	}
}

func TestGenerateRejectsInvalidFactoryExpressions(t *testing.T) {
	t.Run("state", func(t *testing.T) {
		model := scalarGenerationModel()
		model.Factories[0].StateValue = `protocol.State(`

		_, err := Generate(model, Options{PackageName: "fixture"})
		if err == nil || !strings.Contains(err.Error(), "invalid state expression") {
			t.Fatalf("Generate() error = %v", err)
		}
	})

	t.Run("direction", func(t *testing.T) {
		model := scalarGenerationModel()
		model.Factories[0].DirectionValue = `protocol.Direction(`

		_, err := Generate(model, Options{PackageName: "fixture"})
		if err == nil || !strings.Contains(err.Error(), "invalid direction expression") {
			t.Fatalf("Generate() error = %v", err)
		}
	})
}

func TestGenerateRejectsInvalidInput(t *testing.T) {
	tests := []struct {
		name    string
		model   *Model
		options Options
		want    string
	}{
		{name: "nil model", options: Options{PackageName: "fixture"}, want: "nil model"},
		{name: "missing package", model: &Model{}, want: "missing package name"},
		{name: "invalid package", model: &Model{}, options: Options{PackageName: "not-valid"}, want: "invalid package name"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := Generate(test.model, test.options)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("Generate() error = %v, want text %q", err, test.want)
			}
		})
	}
}

func scalarGenerationModel() *Model {
	packet := Packet{
		ID:         1,
		SourceID:   "0x01",
		SourceName: "ping",
		GoName:     "PlayClientboundPing",
		Path:       "play.toClient.ping",
		Fields:     []Field{{SourceName: "value", GoName: "Value", GoType: "int32", Path: "play.toClient.ping.value"}},
		Decode: []Operation{{
			Kind: OpValue, Method: "ReadI32", Value: "packet.Value", GoType: "int32", WireGoType: "int32", Path: "play.toClient.ping.value",
		}},
		Encode: []Operation{{
			Kind: OpValue, Method: "WriteI32", Value: "packet.Value", GoType: "int32", WireGoType: "int32", Path: "play.toClient.ping.value",
		}},
	}
	return &Model{
		PackageName: "fixture",
		States: []State{{
			SourceName: "play",
			GoName:     "Play",
			Directions: []Direction{{
				SourceName:    "toClient",
				GoName:        "Clientbound",
				ProtocolValue: "protocol.DirectionClientbound",
				Packets:       []Packet{packet},
			}},
		}},
		Factories: []Factory{{
			State: "play", StateValue: `"play"`, Direction: "toClient", DirectionValue: "protocol.DirectionClientbound",
			ID: 1, SourceID: "0x01", PacketName: "ping", PacketType: "PlayClientboundPing",
		}},
	}
}

const scalarPacketsGolden = `// Code generated by packetgen. DO NOT EDIT.

package fixture

type PlayClientboundPing struct {
	Value int32
}

func (PlayClientboundPing) PacketID() int32 { return 0x01 }
`

const scalarCodecGolden = `// Code generated by packetgen. DO NOT EDIT.

package fixture

import (
	"fmt"

	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

func (packet *PlayClientboundPing) Decode(buffer *java.Buffer) error {
	if packet == nil {
		return fmt.Errorf("decode PlayClientboundPing: nil packet")
	}
	target := packet
	packet = new(PlayClientboundPing)
	value1, err := buffer.ReadI32("play.toClient.ping.value")
	if err != nil {
		return err
	}
	packet.Value = value1
	*target = *packet
	return nil
}

func (packet *PlayClientboundPing) Encode(buffer *java.Buffer) error {
	if packet == nil {
		return fmt.Errorf("encode PlayClientboundPing: nil packet")
	}
	if err := buffer.WriteI32("play.toClient.ping.value", packet.Value); err != nil {
		return err
	}
	return nil
}
`

const scalarDescriptorGolden = `// Code generated by packetgen. DO NOT EDIT.

package fixture

import (
	"github.com/go-theft-craft/minecraft-protocol"
	"github.com/go-theft-craft/minecraft-protocol/wire/java"
)

type packetCodec interface {
	java.PacketValue
	Decode(*java.Buffer) error
	Encode(*java.Buffer) error
}

type packetKey struct {
	State     protocol.State
	Direction protocol.Direction
	ID        int32
}

type packetFactory func() packetCodec

var packetFactories = map[packetKey]packetFactory{
	{State: protocol.State("play"), Direction: protocol.DirectionClientbound, ID: 0x01}: func() packetCodec { return new(PlayClientboundPing) },
}

func newPacket(state protocol.State, direction protocol.Direction, id int32) (packetCodec, bool) {
	factory, ok := packetFactories[packetKey{State: state, Direction: direction, ID: id}]
	if !ok {
		return nil, false
	}
	return factory(), true
}
`
