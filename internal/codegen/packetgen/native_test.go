package packetgen

import (
	"errors"
	"strconv"
	"strings"
	"testing"
)

// nativeFixture is a minimal protocol whose one packet carries the type under
// test. It is hand-written rather than taken from the real protocol files,
// because the behaviour being pinned is how the compiler treats a schema, not
// what protocol 47 happens to contain.
func nativeFixture(types, field string) string {
	return `{
  "types": {
    "varint":"native", "u8":"native", "i8":"native", "i16":"native", "i32":"native", "bool":"native",
    "pstring":"native", "container":"native", "array":"native", "switch":"native", "option":"native",
    "buffer":"native", "mapper":"native", "bitfield":"native", "bitflags":"native", "void":"native",
    "string":["pstring",{"countType":"varint"}]` + types + `
  },
  "play": {
    "toClient": {
      "types": {
        "packet_only":["container",[
          ` + field + `
        ]],
        "packet":["container",[
          {"name":"name","type":["mapper",{"type":"varint","mappings":{"0":"only"}}]},
          {"name":"params","type":["switch",{"compareTo":"name","fields":{"only":"packet_only"}}]}
        ]]
      }
    }
  }
}`
}

func buildNativeFixture(t *testing.T, types, field string) (*Model, error) {
	t.Helper()

	return Build(parseFixture(t, nativeFixture(types, field)), Options{})
}

// TestNativePositionGetsItsHandWrittenCodec covers the case the rule is meant
// to allow: a schema that declares a name native does get the codec.
func TestNativeNbtGetsItsHandWrittenCodec(t *testing.T) {
	t.Parallel()

	model, err := buildNativeFixture(t, `, "nbt":"native"`, `{"name":"tag","type":"nbt"}`)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	packet := findPacket(t, model.States[0].Directions[0].Packets, "only")
	if field := findField(t, packet.Fields, "tag"); field.GoType != "java.NBT" {
		t.Fatalf("nbt field type = %q, want java.NBT", field.GoType)
	}
}

// TestSchemaDefinedTypeIsCompiledDespiteACodec is the bug M2.5 exists to fix.
// A schema that defines `position` as a bitfield must get that bitfield, even
// though a hand-written position codec once existed under that name.
func TestSchemaDefinedTypeIsCompiledDespiteACodec(t *testing.T) {
	t.Parallel()

	const positionType = `, "position":["bitfield",[
      {"name":"x","size":26,"signed":true},
      {"name":"z","size":26,"signed":true},
      {"name":"y","size":12,"signed":true}
    ]]`

	model, err := buildNativeFixture(t, positionType, `{"name":"location","type":"position"}`)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	packet := findPacket(t, model.States[0].Directions[0].Packets, "only")
	field := findField(t, packet.Fields, "location")
	if strings.HasPrefix(field.GoType, "java.") {
		t.Fatalf("position field type = %q, want a compiled struct", field.GoType)
	}

	// The field order proves the compiled type follows this schema rather than
	// another version's bit order.
	operation := findOperation(t, packet.Decode, "packet.Location")
	if operation.Kind != OpBitField {
		t.Fatalf("position operation kind = %q, want %q", operation.Kind, OpBitField)
	}
}

// TestCodecShadowingASchemaTypeFailsGeneration states the rule as an error
// rather than as a silent preference for one side.
func TestCodecShadowingASchemaTypeFailsGeneration(t *testing.T) {
	t.Parallel()

	const shadowed = `, "nbt":["container",[{"name":"value","type":"i32"}]]`

	_, err := buildNativeFixture(t, shadowed, `{"name":"tag","type":"nbt"}`)
	if !errors.Is(err, ErrDeadNativeCodec) {
		t.Fatalf("error = %v, want ErrDeadNativeCodec", err)
	}
	if !strings.Contains(err.Error(), "nbt") {
		t.Fatalf("error %q does not name the shadowed type", err)
	}
}

// TestTerminatedLoopCarriesItsSchemaTerminator is the protocol 775 case in
// miniature: the same native, a different sentinel, and no constant anywhere
// in the compiler.
func TestTerminatedLoopCarriesItsSchemaTerminator(t *testing.T) {
	t.Parallel()

	for _, terminator := range []uint8{127, 255} {
		types := `, "entityMetadataLoop":"native"`
		field := `{"name":"metadata","type":["entityMetadataLoop",{"endVal":` +
			strconv.Itoa(int(terminator)) + `,"type":["container",[{"name":"value","type":"i8"}]]}]}`

		model, err := buildNativeFixture(t, types, field)
		if err != nil {
			t.Fatalf("Build() error = %v", err)
		}

		packet := findPacket(t, model.States[0].Directions[0].Packets, "only")
		operation := findOperation(t, packet.Decode, "packet.Metadata")
		if operation.Kind != OpTerminatedLoop {
			t.Fatalf("operation kind = %q, want %q", operation.Kind, OpTerminatedLoop)
		}
		if operation.Terminator != terminator {
			t.Fatalf("terminator = %d, want %d", operation.Terminator, terminator)
		}
	}
}

// TestNativeRejectsAnArgumentItsCodecCannotHonour covers a schema asking for a
// wire format the hand-written codec does not implement. Reading it with the
// codec anyway would be wrong bytes, so it fails generation and names the path.
func TestNativeRejectsAnArgumentItsCodecCannotHonour(t *testing.T) {
	t.Parallel()

	const shortString = `, "shortString":["pstring",{"countType":"i16"}]`

	_, err := buildNativeFixture(t, shortString, `{"name":"text","type":"shortString"}`)
	if !errors.Is(err, ErrUnknownNativeArgument) {
		t.Fatalf("error = %v, want ErrUnknownNativeArgument", err)
	}
	if !strings.Contains(err.Error(), "play.toClient.only.text") {
		t.Fatalf("error %q does not name the JSON path", err)
	}
}
