package packetgen

import (
	"go/parser"
	"go/token"
	"os"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol/internal/codegen/protodef"
)

// protocol775Path is the pinned schema. The test reads the real tree rather
// than a fixture, because the constructs this milestone had to add were all
// found by compiling it and none of them were predicted from a summary.
const protocol775Path = "../../../source/java/26.1/data/protocol.json"

func build775(t *testing.T) *Model {
	t.Helper()

	raw, err := os.ReadFile(protocol775Path)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := protodef.Parse(raw)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	model, err := Build(schema, Options{PackageName: "v26_1"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	return model
}

// TestProtocol775CompilesEveryPacket is the stage's exit criterion: the whole
// schema compiles with no unsupported construct anywhere.
func TestProtocol775CompilesEveryPacket(t *testing.T) {
	model := build775(t)

	// Counted from the pinned protocol.json. The approved design said 242 with
	// a different per-state split; it was written before the tree was pinned
	// and does not describe it. These numbers come from the data.
	want := map[string]map[string]int{
		"handshaking":   {"toClient": 0, "toServer": 2},
		"status":        {"toClient": 2, "toServer": 2},
		"login":         {"toClient": 6, "toServer": 5},
		"configuration": {"toClient": 20, "toServer": 10},
		"play":          {"toClient": 141, "toServer": 69},
	}

	if len(model.States) != len(want) {
		t.Fatalf("states = %d, want %d", len(model.States), len(want))
	}

	total := 0
	for _, state := range model.States {
		expected, ok := want[state.SourceName]
		if !ok {
			t.Errorf("unexpected state %q", state.SourceName)

			continue
		}
		for _, direction := range state.Directions {
			got := len(direction.Packets)
			total += got
			if got != expected[direction.SourceName] {
				t.Errorf("%s.%s = %d packets, want %d",
					state.SourceName, direction.SourceName, got, expected[direction.SourceName])
			}
		}
	}

	if total != 257 {
		t.Errorf("total = %d packets, want 257", total)
	}
}

// TestProtocol775SharesItsRecursiveTypes checks the types M2.5 made sharing
// possible for. Slot reaches itself through SlotComponent, so compiling it
// inline would not terminate.
func TestProtocol775SharesItsRecursiveTypes(t *testing.T) {
	model := build775(t)

	shared := make(map[string]SharedType, len(model.SharedTypes))
	for _, item := range model.SharedTypes {
		shared[item.SchemaName] = item
	}

	for _, name := range []string{
		"Slot",
		"SlotComponent",
		"ItemBlockPredicate",
		"DataComponentMatchers",
		"ExactComponentMatcher",
		"ItemEffectDetail",
	} {
		if _, ok := shared[name]; !ok {
			t.Errorf("%s is not shared, so every use of it expands inline", name)
		}
	}
}

// TestProtocol775GeneratesParseableGo runs the renderers over the real model.
// A model that builds can still emit source that does not parse, and at nearly
// a megabyte of codec there is no reading it by eye.
func TestProtocol775GeneratesParseableGo(t *testing.T) {
	model := build775(t)

	files, err := Generate(model, Options{PackageName: "v26_1"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}

	set := token.NewFileSet()
	for _, name := range []string{"packets.go", "codec.go", "descriptor.go"} {
		body, ok := files[name]
		if !ok {
			t.Errorf("generation produced no %s", name)

			continue
		}
		if _, err := parser.ParseFile(set, name, body, parser.AllErrors); err != nil {
			t.Errorf("%s does not parse as Go: %v", name, err)
		}
	}
}

// TestProtocol775DropsOnlyUnreachableVoidCases pins the one case dropped from
// the schema, so a future drop cannot pass unnoticed.
//
// UntrustedSlot discriminates on a varint and lists both "0" and "false". The
// second can never be selected, and its branch reads nothing, so it is dropped.
// Any other unrepresentable key still fails compilation.
func TestProtocol775DropsOnlyUnreachableVoidCases(t *testing.T) {
	model := build775(t)

	found := 0
	for _, declaration := range model.Declarations {
		if declaration.Kind != DeclarationSwitch || declaration.Switch.CompareTo != "itemCount" {
			continue
		}
		found++
		for _, item := range declaration.Switch.Cases {
			if item.SourceKey == "false" {
				t.Errorf("%s kept the unreachable \"false\" case", declaration.Name)
			}
		}
	}

	// Without this the test would pass by finding nothing to check.
	if found == 0 {
		t.Fatal("no switch discriminates on itemCount, so the dropped case is untested")
	}
}
