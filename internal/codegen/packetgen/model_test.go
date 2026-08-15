package packetgen

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol/internal/codegen/protodef"
)

func TestBuildProducesDeterministicRendererReadyModel(t *testing.T) {
	schema := parseFixture(t, modelFixture)

	model, err := Build(schema, Options{PackageName: "fixture"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	second, err := Build(schema, Options{PackageName: "fixture"})
	if err != nil {
		t.Fatalf("second Build() error = %v", err)
	}
	if !reflect.DeepEqual(model, second) {
		t.Fatal("Build() is not deterministic")
	}

	if model.PackageName != "fixture" {
		t.Fatalf("PackageName = %q, want fixture", model.PackageName)
	}
	if len(model.States) != 1 || model.States[0].SourceName != "play" || model.States[0].GoName != "Play" {
		t.Fatalf("states = %#v", model.States)
	}
	direction := model.States[0].Directions[0]
	if direction.SourceName != "toClient" || direction.GoName != "Clientbound" || direction.ProtocolValue != "protocol.DirectionClientbound" {
		t.Fatalf("direction = %#v", direction)
	}
	if got := packetIDs(direction.Packets); !reflect.DeepEqual(got, []int32{2, 0, 1}) {
		t.Fatalf("packet IDs = %v, want source order [2 0 1]", got)
	}

	packet := findPacket(t, direction.Packets, "complex")
	if packet.GoName != "PlayClientboundComplex" || packet.Path != "play.toClient.complex" {
		t.Fatalf("complex packet identity = %#v", packet)
	}
	wantSourceFields := []string{
		"kind", "count", "fixed", "values", "records", "nested", "", "maybe", "payload", "raw",
		"optionalItems", "choice", "numberChoice", "bits", "flags", "tag", "item", "metadata", "foo_bar", "fooBar",
	}
	if got := sourceFieldNames(packet.Fields); !reflect.DeepEqual(got, wantSourceFields) {
		t.Fatalf("field order = %v, want %v", got, wantSourceFields)
	}
	if got := goFieldNames(packet.Fields[len(packet.Fields)-2:]); !reflect.DeepEqual(got, []string{"FooBar", "FooBar2"}) {
		t.Fatalf("colliding Go field names = %v, want [FooBar FooBar2]", got)
	}
	if field := findField(t, packet.Fields, ""); field.GoName != "AnonymousContainer1" || field.GoType != "PlayClientboundComplexAnonymousContainer1" {
		t.Fatalf("anonymous container field = %#v", field)
	}
	if field := findField(t, packet.Fields, "fixed"); field.GoType != "[2]uint8" {
		t.Fatalf("fixed field type = %q, want [2]uint8", field.GoType)
	}
	if field := findField(t, packet.Fields, "records"); field.GoType != "[]PlayClientboundComplexRecordsItem" {
		t.Fatalf("records field type = %q", field.GoType)
	}
	if field := findField(t, packet.Fields, "maybe"); field.GoType != "*string" {
		t.Fatalf("option field type = %q, want *string", field.GoType)
	}
	if field := findField(t, packet.Fields, "optionalItems"); field.GoType != "*[]uint8" {
		t.Fatalf("option array field type = %q, want *[]uint8", field.GoType)
	}
	if field := findField(t, packet.Fields, "tag"); field.GoType != "java.NBT" {
		t.Fatalf("NBT field type = %q", field.GoType)
	}
	// A schema-defined type compiles to a generated struct, even when a
	// hand-written codec of the same name exists elsewhere. That is the rule:
	// only a name the schema declares native gets a codec.
	if field := findField(t, packet.Fields, "item"); field.GoType != "PlayClientboundComplexItem" {
		t.Fatalf("slot field type = %q, want the compiled struct", field.GoType)
	}
	if field := findField(t, packet.Fields, "metadata"); field.GoType != "[]PlayClientboundComplexMetadataItem" {
		t.Fatalf("metadata field type = %q, want the compiled loop element", field.GoType)
	}

	assertValueOperation(t, findOperation(t, packet.Decode, "packet.Kind"), OpMapper, "ReadVarInt", "play.toClient.complex.kind")
	assertValueOperation(t, findOperation(t, packet.Encode, "packet.Kind"), OpMapper, "WriteVarInt", "play.toClient.complex.kind")

	fixed := findOperation(t, packet.Decode, "packet.Fixed")
	if fixed.Kind != OpArray || fixed.Method != "ValidateCollection" || fixed.Count.Kind != CountFixed || fixed.Count.Fixed != 2 {
		t.Fatalf("fixed array operation = %#v", fixed)
	}
	assertValueOperation(t, fixed.Operations[0], OpValue, "ReadU8", "play.toClient.complex.fixed[]")

	values := findOperation(t, packet.Decode, "packet.Values")
	if values.Kind != OpArray || values.Count.Kind != CountReference || values.Count.Reference.Value != "packet.Count" {
		t.Fatalf("referenced array operation = %#v", values)
	}
	records := findOperation(t, packet.Decode, "packet.Records")
	if records.Method != "ReadCollectionLength" || records.Count.Kind != CountType || records.Count.WireGoType != "int32" {
		t.Fatalf("prefixed array operation = %#v", records)
	}
	if records.Index != "index2" || records.Operations[0].Kind != OpContainer {
		t.Fatalf("nested array operation = %#v", records)
	}

	option := findOperation(t, packet.Decode, "packet.Maybe")
	if option.Kind != OpOption || option.Method != "ReadBool" || option.Operations[0].Method != "ReadString" {
		t.Fatalf("option operation = %#v", option)
	}
	assertValueOperation(t, findOperation(t, packet.Decode, "packet.Payload"), OpValue, "ReadByteArray", "play.toClient.complex.payload")
	raw := findOperation(t, packet.Decode, "packet.Raw")
	if raw.Method != "ReadBuffer" || raw.Count.Kind != CountFixed || raw.Count.Fixed != 3 {
		t.Fatalf("fixed buffer operation = %#v", raw)
	}

	choice := findOperation(t, packet.Decode, "packet.Choice")
	if choice.Kind != OpSwitch || choice.Compare.Value != "packet.Kind" || choice.Compare.Mapper == "" {
		t.Fatalf("mapper switch operation = %#v", choice)
	}
	if choice.Cases[1].SourceKey != "text" || choice.Cases[1].Match != `"text"` || choice.Cases[1].Operations[0].Method != "ReadString" {
		t.Fatalf("mapper switch case = %#v", choice.Cases[1])
	}
	numeric := findOperation(t, packet.Decode, "packet.NumberChoice")
	if numeric.Cases[0].Match != "0" || numeric.Compare.GoType != "int32" {
		t.Fatalf("numeric switch = %#v", numeric)
	}

	bits := findOperation(t, packet.Decode, "packet.Bits")
	if bits.Kind != OpBitField || bits.Method != "ReadU8" || bits.Declaration == "" {
		t.Fatalf("bitfield operation = %#v", bits)
	}
	bitsDeclaration := findDeclaration(t, packet.Declarations, bits.Declaration)
	if bitsDeclaration.BitField.WireGoType != "uint8" || bitsDeclaration.BitField.Fields[0].Shift != 5 || bitsDeclaration.BitField.Fields[1].GoType != "int8" {
		t.Fatalf("bitfield declaration = %#v", bitsDeclaration)
	}
	flags := findOperation(t, packet.Decode, "packet.Flags")
	if flags.Kind != OpBitFlags || flags.Method != "ReadU8" {
		t.Fatalf("bitflags operation = %#v", flags)
	}
	assertValueOperation(t, findOperation(t, packet.Decode, "packet.Tag"), OpValue, "ReadNBT", "play.toClient.complex.tag")
	// A schema-defined slot is a container operation, not a codec call.
	if item := findOperation(t, packet.Decode, "packet.Item"); item.Kind != OpContainer {
		t.Fatalf("slot operation kind = %q, want %q", item.Kind, OpContainer)
	}
	// A terminated loop carries the schema's own terminator.
	metadata := findOperation(t, packet.Decode, "packet.Metadata")
	if metadata.Kind != OpTerminatedLoop {
		t.Fatalf("metadata operation kind = %q, want %q", metadata.Kind, OpTerminatedLoop)
	}
	if metadata.Terminator != 127 {
		t.Fatalf("metadata terminator = %d, want 127", metadata.Terminator)
	}

	customPayload := findPacket(t, direction.Packets, "custom_payload")
	assertValueOperation(t, findOperation(t, customPayload.Decode, "packet.Data"), OpValue, "ReadPluginPayload", "play.toClient.custom_payload.data")
	assertValueOperation(t, findOperation(t, customPayload.Encode, "packet.Data"), OpValue, "WritePluginPayload", "play.toClient.custom_payload.data")

	if len(model.Factories) != 3 || model.Factories[0].ID != 2 || model.Factories[1].PacketType != packet.GoName {
		t.Fatalf("factories = %#v", model.Factories)
	}
	if len(model.Mappers) != 1 || model.Mappers[0].ReadTable == model.Mappers[0].WriteTable {
		t.Fatalf("mappers = %#v", model.Mappers)
	}
	wantMappings := []MapperEntry{{SourceKey: "0", WireValue: "0", Symbol: "empty"}, {SourceKey: "1", WireValue: "1", Symbol: "text"}}
	if !reflect.DeepEqual(model.Mappers[0].Entries, wantMappings) {
		t.Fatalf("mapper entries = %#v, want %#v", model.Mappers[0].Entries, wantMappings)
	}
}

func TestBuildParenthesizesOptionArrayElementValues(t *testing.T) {
	model, err := Build(parseFixture(t, modelFixture), Options{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	packet := findPacket(t, model.States[0].Directions[0].Packets, "complex")

	decodeOption := findOperation(t, packet.Decode, "packet.OptionalItems")
	if decodeOption.Kind != OpOption || len(decodeOption.Operations) != 1 {
		t.Fatalf("decode option operation = %#v", decodeOption)
	}
	decodeArray := decodeOption.Operations[0]
	if decodeArray.Kind != OpArray || len(decodeArray.Operations) != 1 {
		t.Fatalf("decode array operation = %#v", decodeArray)
	}
	if got, want := decodeArray.Operations[0].Value, "(*(packet.OptionalItems))[index3]"; got != want {
		t.Fatalf("decode element value = %q, want %q", got, want)
	}

	encodeOption := findOperation(t, packet.Encode, "packet.OptionalItems")
	if encodeOption.Kind != OpOption || len(encodeOption.Operations) != 1 {
		t.Fatalf("encode option operation = %#v", encodeOption)
	}
	encodeArray := encodeOption.Operations[0]
	if encodeArray.Kind != OpArray || len(encodeArray.Operations) != 1 {
		t.Fatalf("encode array operation = %#v", encodeArray)
	}
	if got, want := encodeArray.Operations[0].Value, "(*(packet.OptionalItems))[index3]"; got != want {
		t.Fatalf("encode element value = %q, want %q", got, want)
	}
}

func TestBuildPinnedJava18Protocol(t *testing.T) {
	raw, err := os.ReadFile("../../../source/java/1.8/protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	schema, err := protodef.Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	model, err := Build(schema, Options{PackageName: "v1_8"})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if len(model.States) != 4 || len(model.Factories) != 112 {
		t.Fatalf("model has %d states and %d factories, want 4 and 112", len(model.States), len(model.Factories))
	}

	var foundPlayerInfo bool
	var foundI32Count bool
	for _, state := range model.States {
		for _, direction := range state.Directions {
			for _, packet := range direction.Packets {
				if len(packet.Decode) != len(packet.Encode) {
					t.Fatalf("%s decode operations = %d, encode operations = %d", packet.Path, len(packet.Decode), len(packet.Encode))
				}
				assertExplicitOperations(t, packet.Decode)
				assertExplicitOperations(t, packet.Encode)
				if packet.SourceName == "player_info" {
					foundPlayerInfo = true
					if !hasAnonymousSwitch(packet.Declarations) {
						t.Fatalf("%s has no modeled anonymous switch", packet.Path)
					}
				}
				if packet.SourceName == "update_attributes" {
					properties := findOperation(t, packet.Decode, "packet.Properties")
					if properties.Method != "ReadI32" || properties.Count.WireGoType != "int32" || properties.Count.ValidateMethod != "ValidateCollection" {
						t.Fatalf("%s properties count = %#v", packet.Path, properties.Count)
					}
					foundI32Count = true
				}
			}
		}
	}
	if !foundPlayerInfo {
		t.Fatal("player_info packet not found")
	}
	if !foundI32Count {
		t.Fatal("update_attributes packet not found")
	}
}

func TestBuildRejectsUnsupportedReachableNodeWithSourceContext(t *testing.T) {
	schema := parseFixture(t, strings.Replace(modelFixture, `{"name":"value","type":"i32"}`, `{"name":"value","type":"mystery"}`, 1))

	_, err := Build(schema, Options{})
	if err == nil {
		t.Fatal("Build() error = nil")
	}
	if got := err.Error(); !strings.Contains(got, "play.toClient.second.value") || !strings.Contains(got, `no hand-written codec for native "mystery"`) {
		t.Fatalf("Build() error = %q", got)
	}
}

func TestBuildRejectsUnsupportedPrefixedBufferCount(t *testing.T) {
	schema := parseFixture(t, strings.Replace(modelFixture, `{"name":"payload","type":["buffer",{"countType":"varint"}]}`, `{"name":"payload","type":["buffer",{"countType":"i32"}]}`, 1))

	_, err := Build(schema, Options{})
	if err == nil {
		t.Fatal("Build() error = nil")
	}
	if got := err.Error(); !strings.Contains(got, "play.toClient.complex.payload") || !strings.Contains(got, "prefixed buffer count must use varint") {
		t.Fatalf("Build() error = %q", got)
	}
}

func TestBuildIgnoresUnsupportedUnreachableDefinitions(t *testing.T) {
	schema := parseFixture(t, strings.Replace(modelFixture, `"mystery":"native",`, `"mystery":"native","unused":"mystery",`, 1))

	if _, err := Build(schema, Options{}); err != nil {
		t.Fatalf("Build() error = %v", err)
	}
}

func TestBuildRejectsNilSchema(t *testing.T) {
	_, err := Build(nil, Options{})
	if err == nil || err.Error() != "packetgen: nil ProtoDef schema" {
		t.Fatalf("Build(nil) error = %v", err)
	}
}

func parseFixture(t *testing.T, raw string) *protodef.Schema {
	t.Helper()
	schema, err := protodef.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	return schema
}

func packetIDs(packets []Packet) []int32 {
	ids := make([]int32, len(packets))
	for index := range packets {
		ids[index] = packets[index].ID
	}
	return ids
}

func sourceFieldNames(fields []Field) []string {
	names := make([]string, len(fields))
	for index := range fields {
		names[index] = fields[index].SourceName
	}
	return names
}

func goFieldNames(fields []Field) []string {
	names := make([]string, len(fields))
	for index := range fields {
		names[index] = fields[index].GoName
	}
	return names
}

func findPacket(t *testing.T, packets []Packet, sourceName string) Packet {
	t.Helper()
	for _, packet := range packets {
		if packet.SourceName == sourceName {
			return packet
		}
	}
	t.Fatalf("packet %q not found", sourceName)
	return Packet{}
}

func findField(t *testing.T, fields []Field, sourceName string) Field {
	t.Helper()
	for _, field := range fields {
		if field.SourceName == sourceName {
			return field
		}
	}
	t.Fatalf("field %q not found", sourceName)
	return Field{}
}

func findOperation(t *testing.T, operations []Operation, value string) Operation {
	t.Helper()
	for _, operation := range operations {
		if operation.Value == value {
			return operation
		}
	}
	t.Fatalf("operation for %q not found", value)
	return Operation{}
}

func findDeclaration(t *testing.T, declarations []Declaration, name string) Declaration {
	t.Helper()
	for _, declaration := range declarations {
		if declaration.Name == name {
			return declaration
		}
	}
	t.Fatalf("declaration %q not found", name)
	return Declaration{}
}

func assertValueOperation(t *testing.T, operation Operation, kind OperationKind, method, path string) {
	t.Helper()
	if operation.Kind != kind || operation.Method != method || operation.Path != path {
		t.Fatalf("operation = %#v, want kind %q, method %q, path %q", operation, kind, method, path)
	}
}

func assertExplicitOperations(t *testing.T, operations []Operation) {
	t.Helper()
	for _, operation := range operations {
		if operation.Path == "" || operation.Value == "" {
			t.Fatalf("operation lacks a stable path or value expression: %#v", operation)
		}
		switch operation.Kind {
		case OpValue, OpMapper, OpArray, OpOption, OpBitField, OpBitFlags:
			if operation.Method == "" {
				t.Fatalf("%s operation has no java.Buffer method: %#v", operation.Kind, operation)
			}
		case OpContainer, OpSwitch:
		case OpTerminatedLoop:
			// A terminated loop names its sentinel rather than a buffer
			// method: the element decides what it reads.
			if operation.Terminator == 0 {
				t.Fatalf("terminated loop has no terminator: %#v", operation)
			}
		case OpVoid:
			if operation.Method != "" {
				t.Fatalf("void operation has method %q", operation.Method)
			}
		default:
			t.Fatalf("unknown operation kind %q", operation.Kind)
		}
		assertExplicitOperations(t, operation.Operations)
		for _, item := range operation.Cases {
			assertExplicitOperations(t, item.Operations)
		}
		if operation.HasDefault {
			assertExplicitOperations(t, operation.Default.Operations)
		}
	}
}

func hasAnonymousSwitch(declarations []Declaration) bool {
	for _, declaration := range declarations {
		if declaration.Kind == DeclarationSwitch && strings.Contains(declaration.Name, "AnonymousSwitch") {
			return true
		}
	}
	return false
}

const modelFixture = `{
  "types": {
    "varint":"native", "u8":"native", "i8":"native", "i16":"native", "i32":"native", "bool":"native",
    "pstring":"native", "container":"native", "array":"native", "switch":"native", "option":"native",
    "buffer":"native", "mapper":"native", "bitfield":"native", "bitflags":"native", "restBuffer":"native",
    "nbt":"native", "optionalNbt":"native", "entityMetadataLoop":"native", "void":"native", "mystery":"native",
    "string":["pstring",{"countType":"varint"}],
    "slot":["container",[
      {"name":"blockId","type":"i16"},
      {"anon":true,"type":["switch",{"compareTo":"blockId","fields":{"-1":"void"},"default":["container",[
        {"name":"itemCount","type":"i8"},
        {"name":"nbtData","type":"optionalNbt"}
      ]]}]}
    ]],
    "metadataItem":["switch",{"compareTo":"$compareTo","fields":{"0":"i8","1":"i16"}}],
    "entityMetadata":["entityMetadataLoop",{"endVal":127,"type":["container",[
      {"anon":true,"type":["bitfield",[
        {"name":"type","size":3,"signed":false},
        {"name":"key","size":5,"signed":false}
      ]]},
      {"name":"value","type":["metadataItem",{"compareTo":"type"}]}
    ]]}]
  },
  "play": {
    "toClient": {
      "types": {
        "packet_second":["container",[
          {"name":"value","type":"i32"}
        ]],
        "packet_complex":["container",[
          {"name":"kind","type":["mapper",{"type":"varint","mappings":{"0":"empty","1":"text"}}]},
          {"name":"count","type":"varint"},
          {"name":"fixed","type":["array",{"count":2,"type":"u8"}]},
          {"name":"values","type":["array",{"count":"count","type":"i16"}]},
          {"name":"records","type":["array",{"countType":"varint","type":["container",[
            {"name":"name","type":"string"}
          ]]}]},
          {"name":"nested","type":["container",[
            {"name":"enabled","type":"bool"}
          ]]},
          {"anon":true,"type":["container",[
            {"name":"anonymous_value","type":"i8"}
          ]]},
          {"name":"maybe","type":["option","string"]},
		  {"name":"payload","type":["buffer",{"countType":"varint"}]},
		  {"name":"raw","type":["buffer",{"count":3}]},
		  {"name":"optionalItems","type":["option",["array",{"countType":"varint","type":"u8"}]]},
		  {"name":"choice","type":["switch",{"compareTo":"kind","fields":{"empty":"void","text":"string"}}]},
          {"name":"numberChoice","type":["switch",{"compareTo":"count","fields":{"0":"bool"},"default":"void"}]},
          {"name":"bits","type":["bitfield",[
            {"name":"high","size":3,"signed":false},
            {"name":"low","size":5,"signed":true}
          ]]},
          {"name":"flags","type":["bitflags",{"type":"u8","flags":["first","second"]}]},
          {"name":"tag","type":"nbt"},
          {"name":"item","type":"slot"},
          {"name":"metadata","type":"entityMetadata"},
          {"name":"foo_bar","type":"bool"},
          {"name":"fooBar","type":"bool"}
        ]],
        "packet_custom_payload":["container",[
          {"name":"channel","type":"string"},
          {"name":"data","type":"restBuffer"}
        ]],
        "packet":["container",[
          {"name":"name","type":["mapper",{"type":"varint","mappings":{"2":"second","0":"complex","1":"custom_payload"}}]},
          {"name":"params","type":["switch",{"compareTo":"name","fields":{
            "second":"packet_second", "complex":"packet_complex", "custom_payload":"packet_custom_payload"
          }}]}
        ]]
      }
    }
  }
}`
