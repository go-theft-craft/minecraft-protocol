package protodef

import (
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestParseOperators(t *testing.T) {
	raw, err := os.ReadFile("testdata/operators.json")
	if err != nil {
		t.Fatal(err)
	}

	schema, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	if got := schema.Types["varint"]; got.Kind != KindNative || got.Name != "varint" {
		t.Fatalf("native type = %#v", got)
	}
	if got := schema.Types["alias"]; got.Kind != KindAlias || got.Name != "name" || got.Target != schema.Types["name"] {
		t.Fatalf("alias type = %#v", got)
	}

	parameterized := schema.Types["parameterizedAlias"]
	if parameterized.Kind != KindAlias || parameterized.Name != "alias" || len(parameterized.Arguments) != 1 {
		t.Fatalf("parameterized alias = %#v", parameterized)
	}
	if got := parameterized.Arguments[0]; got.Name != "compareTo" || got.String != "$compareTo" {
		t.Fatalf("parameterized alias argument = %#v", got)
	}

	name := schema.Types["name"]
	if name.Kind != KindNative || name.Name != "pstring" || name.Count == nil || name.Count.Kind != CountType {
		t.Fatalf("pstring node = %#v", name)
	}
	if name.Target != schema.Types["pstring"] {
		t.Fatalf("pstring target = %#v, want %#v", name.Target, schema.Types["pstring"])
	}
	if name.Count.Type.Kind != KindPrimitive || name.Count.Type.Name != "varint" || name.Count.Type.Target != schema.Types["varint"] {
		t.Fatalf("pstring count type = %#v", name.Count.Type)
	}

	record := schema.Types["record"]
	if record.Kind != KindContainer {
		t.Fatalf("record kind = %q", record.Kind)
	}
	wantFields := []string{"count", "values", "fixed", "maybe", "payload", "choice", "mapped", "", "flags"}
	gotFields := make([]string, len(record.Fields))
	for i := range record.Fields {
		gotFields[i] = record.Fields[i].Name
	}
	if !reflect.DeepEqual(gotFields, wantFields) {
		t.Fatalf("field order = %v, want %v", gotFields, wantFields)
	}
	if !record.Fields[7].Anonymous {
		t.Fatal("anonymous bitfield was not retained")
	}

	values := record.Fields[1].Type
	if values.Kind != KindArray || values.Count.Kind != CountReference || values.Count.Reference != "count" {
		t.Fatalf("reference-count array = %#v", values)
	}
	if values.Element.Kind != KindPrimitive || values.Element.Name != "u8" {
		t.Fatalf("array element = %#v", values.Element)
	}
	fixed := record.Fields[2].Type
	if fixed.Count.Kind != CountFixed || fixed.Count.Fixed != 2 {
		t.Fatalf("fixed-count array = %#v", fixed)
	}
	option := record.Fields[3].Type
	if option.Kind != KindOption || option.Element.Kind != KindPrimitive || option.Element.Name != "bool" {
		t.Fatalf("option = %#v", option)
	}
	buffer := record.Fields[4].Type
	if buffer.Kind != KindBuffer || buffer.Count.Kind != CountType || buffer.Count.Type.Name != "varint" {
		t.Fatalf("buffer = %#v", buffer)
	}

	switchNode := record.Fields[5].Type
	if switchNode.Kind != KindSwitch || switchNode.CompareTo != "count" {
		t.Fatalf("switch = %#v", switchNode)
	}
	if got := []string{switchNode.Cases[0].Key, switchNode.Cases[1].Key}; !reflect.DeepEqual(got, []string{"0", "ready"}) {
		t.Fatalf("switch keys = %v", got)
	}
	if switchNode.Default.Kind != KindPrimitive || switchNode.Default.Name != "u8" {
		t.Fatalf("switch default = %#v", switchNode.Default)
	}

	mapper := record.Fields[6].Type
	if mapper.Kind != KindMapper || mapper.Element.Name != "varint" {
		t.Fatalf("mapper = %#v", mapper)
	}
	gotMappings := make([][2]string, len(mapper.Mappings))
	for i, mapping := range mapper.Mappings {
		gotMappings[i] = [2]string{mapping.Key, mapping.Value}
	}
	wantMappings := [][2]string{{"0x01", "one"}, {"2", "two"}}
	if !reflect.DeepEqual(gotMappings, wantMappings) {
		t.Fatalf("mapper mappings = %#v, want %#v", gotMappings, wantMappings)
	}

	bitfield := record.Fields[7].Type
	wantBits := []BitField{{Name: "high", Size: 3}, {Name: "low", Size: 5, Signed: true}}
	if bitfield.Kind != KindBitField || !reflect.DeepEqual(bitfield.Bits, wantBits) {
		t.Fatalf("bitfield = %#v", bitfield)
	}
	bitflags := record.Fields[8].Type
	if bitflags.Kind != KindBitFlags || bitflags.Element.Name != "u8" || !reflect.DeepEqual(bitflags.Flags, []string{"first", "second"}) {
		t.Fatalf("bitflags = %#v", bitflags)
	}

	metadata := schema.Types["metadata"]
	if metadata.Kind != KindNative || metadata.Name != "entityMetadataLoop" || len(metadata.Arguments) != 2 {
		t.Fatalf("protocol-native node = %#v", metadata)
	}
	if metadata.Arguments[0].Name != "endVal" || metadata.Arguments[0].Number != "127" {
		t.Fatalf("native numeric argument = %#v", metadata.Arguments[0])
	}
	if metadata.Arguments[1].Name != "type" || metadata.Arguments[1].Type.Kind != KindAlias || metadata.Arguments[1].Type.Target != record {
		t.Fatalf("native type argument = %#v", metadata.Arguments[1])
	}
}

func TestParseErrorsIncludeJSONPath(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{
			name: "malformed node",
			raw:  `{"types":{"bad":["array"]}}`,
			want: `$.types.bad: type node must be a string or a two-element array`,
		},
		{
			name: "missing switch fields",
			raw:  `{"types":{"switch":"native","varint":"native","bad":["switch",{"compareTo":"value"}]}}`,
			want: `$.types.bad[1].fields: required`,
		},
		{
			name: "invalid count reference",
			raw:  `{"types":{"container":"native","array":"native","u8":"native","bad":["container",[{"name":"items","type":["array",{"count":"missing","type":"u8"}]}]]}}`,
			want: `$.types.bad[1][0].type[1].count: unknown field reference "missing"`,
		},
		{
			name: "invalid switch compareTo reference",
			raw:  `{"types":{"container":"native","switch":"native","u8":"native","bad":["container",[{"name":"value","type":"u8"},{"name":"choice","type":["switch",{"compareTo":"missing","fields":{}}]}]]}}`,
			want: `$.types.bad[1][1].type[1].compareTo: unknown field reference "missing"`,
		},
		{
			name: "qualified compareTo names an exact level",
			raw:  `{"types":{"container":"native","switch":"native","u8":"native","bad":["container",[{"name":"value","type":"u8"},{"name":"child","type":["container",[{"name":"choice","type":["switch",{"compareTo":"../../value","fields":{}}]}]]}]]}}`,
			want: `$.types.bad[1][1].type[1][0].type[1].compareTo: unknown field reference "../../value"`,
		},
		{
			name: "invalid native compareTo reference",
			raw:  `{"types":{"container":"native","custom":"native","u8":"native","bad":["container",[{"name":"value","type":"u8"},{"name":"choice","type":["custom",{"compareTo":"missing"}]}]]}}`,
			want: `$.types.bad[1][1].type[1].compareTo: unknown field reference "missing"`,
		},
		{
			name: "invalid alias compareTo reference",
			raw:  `{"types":{"container":"native","u8":"native","custom":"u8","bad":["container",[{"name":"value","type":"u8"},{"name":"choice","type":["custom",{"compareTo":"missing"}]}]]}}`,
			want: `$.types.bad[1][1].type[1].compareTo: unknown field reference "missing"`,
		},
		{
			name: "duplicate fields",
			raw:  `{"types":{"container":"native","u8":"native","bad":["container",[{"name":"same","type":"u8"},{"name":"same","type":"u8"}]]}}`,
			want: `$.types.bad[1][1].name: duplicate field "same"`,
		},
		{
			name: "unresolved named type",
			raw:  `{"types":{"bad":"missing"}}`,
			want: `$.types.bad: unresolved named type "missing"`,
		},
		{
			name: "alias cycle",
			raw:  `{"types":{"a":"b","b":"a"}}`,
			want: `$.types.a: alias cycle: a -> b -> a`,
		},
		{
			name: "duplicate packet IDs",
			raw:  packetFixture(`{"0":"first","0x00":"second"}`, `{"first":"packet_first","second":"packet_second"}`),
			want: `$.play.toClient.types.packet[1][0].type[1].mappings["0x00"]: duplicate packet ID 0`,
		},
		{
			name: "duplicate packet ID key",
			raw:  packetFixture(`{"0":"first","0":"second"}`, `{"first":"packet_first","second":"packet_second"}`),
			want: `$.play.toClient.types.packet[1][0].type[1].mappings["0"]: duplicate packet ID 0`,
		},
		{
			name: "duplicate packet names",
			raw:  packetFixture(`{"0":"same","1":"same"}`, `{"same":"packet_same"}`),
			want: `$.play.toClient.types.packet[1][0].type[1].mappings["1"]: duplicate packet name "same"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Parse([]byte(tt.raw))
			if err == nil {
				t.Fatal("Parse() error = nil")
			}
			if err.Error() != tt.want {
				t.Fatalf("Parse() error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestParsePreservesPacketSourceOrder(t *testing.T) {
	raw := packetFixture(
		`{"2":"second","0":"first","1":"same"}`,
		`{"second":"packet_second","first":"packet_first","same":"packet_same"}`,
	)

	schema, err := Parse([]byte(raw))
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	packets := schema.States[0].Directions[0].Packets
	got := make([]int, len(packets))
	for i, packet := range packets {
		got[i] = packet.ID
	}
	if want := []int{2, 0, 1}; !reflect.DeepEqual(got, want) {
		t.Fatalf("packet IDs = %v, want source order %v", got, want)
	}
}

func TestParsePinnedJava18Protocol(t *testing.T) {
	raw, err := os.ReadFile("../../../source/java/1.8/protocol.json")
	if err != nil {
		t.Fatal(err)
	}

	schema, err := Parse(raw)
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}

	wantStates := []string{"handshaking", "status", "login", "play"}
	wantPacketCounts := [][]int{{0, 2}, {2, 2}, {4, 2}, {74, 26}}
	if len(schema.States) != len(wantStates) {
		t.Fatalf("states = %d, want %d", len(schema.States), len(wantStates))
	}
	for i, state := range schema.States {
		if state.Name != wantStates[i] {
			t.Fatalf("state %d = %q, want %q", i, state.Name, wantStates[i])
		}
		if len(state.Directions) != 2 {
			t.Fatalf("%s directions = %d, want 2", state.Name, len(state.Directions))
		}
		for j, direction := range state.Directions {
			wantDirection := []string{"toClient", "toServer"}[j]
			if direction.Name != wantDirection {
				t.Fatalf("%s direction %d = %q, want %q", state.Name, j, direction.Name, wantDirection)
			}
			if direction.PacketMap == nil || direction.PacketMap.Kind != KindMapper {
				t.Fatalf("%s.%s packet map = %#v", state.Name, direction.Name, direction.PacketMap)
			}
			if direction.PacketSwitch == nil || direction.PacketSwitch.Kind != KindSwitch {
				t.Fatalf("%s.%s packet switch = %#v", state.Name, direction.Name, direction.PacketSwitch)
			}
			if len(direction.Packets) != wantPacketCounts[i][j] {
				t.Fatalf("%s.%s packets = %d, want %d", state.Name, direction.Name, len(direction.Packets), wantPacketCounts[i][j])
			}
			for _, packet := range direction.Packets {
				if packet.Type == nil || packet.Type.Target == nil {
					t.Fatalf("%s.%s packet %s has unresolved type %#v", state.Name, direction.Name, packet.Name, packet.Type)
				}
			}
		}
	}
}

func packetFixture(mappings, fields string) string {
	return strings.NewReplacer("MAPPINGS", mappings, "FIELDS", fields).Replace(`{
  "types": {
    "varint": "native",
    "container": "native",
    "mapper": "native",
    "switch": "native"
  },
  "play": {
    "toClient": {
      "types": {
        "packet_first": ["container", []],
        "packet_second": ["container", []],
        "packet_same": ["container", []],
        "packet": ["container", [
          {"name":"name","type":["mapper",{"type":"varint","mappings":MAPPINGS}]},
          {"name":"params","type":["switch",{"compareTo":"name","fields":FIELDS}]}
        ]]
      }
    }
  }
}`)
}

// TestUnqualifiedReferenceResolvesOutward pins the scoping rule protocol 775
// forced.
//
// A bare name resolves lexically: the innermost scope first, then outward. The
// parser used to require it in the innermost scope alone, which matches
// node-protodef's getField literally. Protocol 775's DebugSubscriptionUpdate
// discriminates a nested switch on "type", a field two containers out, and
// that switch declares no default — so under the literal rule the packet
// cannot be decoded by anything, this generator included.
//
// The outward walk only ever accepts more than the old rule did. A reference
// that resolved before still resolves to the same field, because the innermost
// scope is searched first, so no wire format can change underneath it.
func TestUnqualifiedReferenceResolvesOutward(t *testing.T) {
	raw := `{"types":{"container":"native","switch":"native","u8":"native","void":"native",` +
		`"good":["container",[{"name":"value","type":"u8"},` +
		`{"name":"child","type":["container",[` +
		`{"name":"choice","type":["switch",{"compareTo":"value","fields":{"1":"u8"},"default":"void"}]}` +
		`]]}]]}}`

	if _, err := Parse([]byte(raw)); err != nil {
		t.Fatalf("Parse() error = %v, want a reference resolved in the enclosing container", err)
	}
}
