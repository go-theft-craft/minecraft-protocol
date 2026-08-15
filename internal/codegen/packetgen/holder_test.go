package packetgen

import (
	"strings"
	"testing"
)

// holderFixture exercises the three parameterized natives protocol 775 adds,
// in the shapes the real schema uses them in.
const holderFixture = `{
  "types": {
    "varint": "native",
    "i8": "native",
    "u8": "native",
    "pstring": "native",
    "string": ["pstring", {"countType": "varint"}],
    "container": "native",
    "switch": "native",
    "mapper": "native",
    "array": "native",
    "void": "native",
    "registryEntryHolder": "native",
    "registryEntryHolderSet": "native",
    "topBitSetTerminatedArray": "native",
    "DamageTypeData": ["container", [
      {"name": "exhaustion", "type": "varint"}
    ]]
  },
  "play": {
    "toClient": {
      "types": {
        "packet_damage": ["container", [
          {"name": "source", "type": ["registryEntryHolder", {
            "baseName": "damageTypeId",
            "otherwise": {"name": "data", "type": "DamageTypeData"}
          }]}
        ]],
        "packet_predicate": ["container", [
          {"name": "blocks", "type": ["registryEntryHolderSet", {
            "base": {"name": "name", "type": "string"},
            "otherwise": {"name": "blockIds", "type": "varint"}
          }]}
        ]],
        "packet_equipment": ["container", [
          {"name": "entityId", "type": "varint"},
          {"name": "equipment", "type": ["topBitSetTerminatedArray", {
            "type": ["container", [
              {"name": "slot", "type": "i8"},
              {"name": "itemId", "type": "varint"}
            ]]
          }]}
        ]],
        "packet": ["container", [
          {"name": "name", "type": ["mapper", {"type": "varint", "mappings": {
            "0": "damage", "1": "predicate", "2": "equipment"
          }}]},
          {"name": "params", "type": ["switch", {"compareTo": "name", "fields": {
            "damage": "packet_damage",
            "predicate": "packet_predicate",
            "equipment": "packet_equipment"
          }}]}
        ]]
      }
    }
  }
}`

func TestRegistryEntryHolderCompilesToAGenericHolder(t *testing.T) {
	model, err := Build(parseFixture(t, holderFixture), Options{PackageName: "fixture"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	packet := findModelPacket(t, model, "damage")
	source := findField(t, packet.Fields, "source")

	// The inline branch's compiled type becomes the type parameter, so the
	// registry ID and the inline value stay one field rather than two that can
	// disagree.
	if !strings.HasPrefix(source.GoType, "java.Holder[") {
		t.Errorf("source = %q, want a java.Holder", source.GoType)
	}
	if !strings.HasSuffix(source.GoType, "]") || strings.Contains(source.GoType, "Holder[]") {
		t.Errorf("source = %q, want an instantiated type parameter", source.GoType)
	}
}

func TestRegistryEntryHolderSetCompilesToAGenericHolderSet(t *testing.T) {
	model, err := Build(parseFixture(t, holderFixture), Options{PackageName: "fixture"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	packet := findModelPacket(t, model, "predicate")
	blocks := findField(t, packet.Fields, "blocks")

	if blocks.GoType != "java.HolderSet[int32]" {
		t.Errorf("blocks = %q, want java.HolderSet[int32]", blocks.GoType)
	}
}

func TestTopBitSetTerminatedArrayCompilesToASlice(t *testing.T) {
	model, err := Build(parseFixture(t, holderFixture), Options{PackageName: "fixture"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	packet := findModelPacket(t, model, "equipment")
	equipment := findField(t, packet.Fields, "equipment")

	if !strings.HasPrefix(equipment.GoType, "[]") {
		t.Errorf("equipment = %q, want a slice", equipment.GoType)
	}
}

// TestParameterizedNativesGenerateCompilableCode runs the renderers, because a
// model that builds can still emit Go that does not parse.
func TestParameterizedNativesGenerateCompilableCode(t *testing.T) {
	model, err := Build(parseFixture(t, holderFixture), Options{PackageName: "fixture"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	files, err := Generate(model, Options{PackageName: "fixture"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	codec := string(files["codec.go"])
	for _, want := range []string{
		"ReadHolder(",
		"WriteHolder(",
		"ReadHolderSet(",
		"WriteHolderSet(",
		"PeekTopBitSetContinues(",
		"SetTopBitSetContinues(",
	} {
		if !strings.Contains(codec, want) {
			t.Errorf("generated codec never calls %s", want)
		}
	}
}

func TestParameterizedNativesRejectUnknownArguments(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		to      string
		wantErr string
	}{
		{
			name:    "a holder without baseName",
			from:    `"baseName": "damageTypeId",`,
			to:      ``,
			wantErr: "needs baseName",
		},
		{
			name:    "a holder set whose tag is not a string",
			from:    `"base": {"name": "name", "type": "string"}`,
			to:      `"base": {"name": "name", "type": "varint"}`,
			wantErr: "tags must be strings",
		},
		{
			name: "an argument the native does not accept",
			from: `"type": ["container", [
              {"name": "slot", "type": "i8"},`,
			to: `"endVal": 127, "type": ["container", [
              {"name": "slot", "type": "i8"},`,
			wantErr: "does not accept argument",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Replace(holderFixture, tc.from, tc.to, 1)
			if raw == holderFixture {
				t.Fatalf("fixture does not contain %s", tc.from)
			}

			if _, err := Build(parseFixture(t, raw), Options{PackageName: "fixture"}); err == nil {
				t.Fatal("Build accepted the native")
			} else if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to name %q", err, tc.wantErr)
			}
		})
	}
}
