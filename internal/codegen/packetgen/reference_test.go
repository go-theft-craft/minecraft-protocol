package packetgen

import (
	"strings"
	"testing"

	"github.com/go-theft-craft/minecraft-protocol/internal/codegen/protodef"
)

// referenceFixture is the shape protocol 775 introduced: a bitflags field whose
// individual bits gate later fields, a bitfield whose segments do the same, and
// a nested container that discriminates on a field two scopes out.
const referenceFixture = `{
  "types": {
    "varint": "native",
    "u8": "native",
    "i32": "native",
    "string": ["pstring", {"countType": "varint"}],
    "pstring": "native",
    "container": "native",
    "switch": "native",
    "option": "native",
    "bitflags": "native",
    "bitfield": "native",
    "mapper": "native",
    "bool": "native",
    "void": "native"
  },
  "play": {
    "toClient": {
      "types": {
        "packet_node": ["container", [
          {"name": "label", "type": "varint"},
          {"name": "flags", "type": ["bitfield", [
            {"name": "unused", "size": 4, "signed": false},
            {"name": "has_custom_suggestions", "size": 1, "signed": false},
            {"name": "has_redirect_node", "size": 1, "signed": false},
            {"name": "command_node_type", "size": 2, "signed": false}
          ]]},
          {"name": "redirect", "type": ["switch", {
            "compareTo": "flags/has_redirect_node",
            "fields": {"1": "varint"},
            "default": "void"
          }]},
          {"name": "suggestions", "type": ["switch", {
            "compareTo": "flags/has_custom_suggestions",
            "fields": {"1": "string"},
            "default": "void"
          }]}
        ]],
        "packet_actions": ["container", [
          {"name": "action", "type": ["bitflags", {
            "type": "u8",
            "flags": ["add_player", "update_latency"]
          }]},
          {"name": "latency", "type": ["switch", {
            "compareTo": "action/update_latency",
            "fields": {"true": "varint"},
            "default": "void"
          }]}
        ]],
        "packet_nested": ["container", [
          {"name": "kind", "type": "varint"},
          {"name": "body", "type": ["container", [
            {"name": "payload", "type": ["switch", {
              "compareTo": "kind",
              "fields": {"1": "varint"},
              "default": "void"
            }]}
          ]]}
        ]],
        "packet": ["container", [
          {"name": "name", "type": ["mapper", {"type": "varint", "mappings": {
            "0": "node", "1": "actions", "2": "nested"
          }}]},
          {"name": "params", "type": ["switch", {"compareTo": "name", "fields": {
            "node": "packet_node", "actions": "packet_actions", "nested": "packet_nested"
          }}]}
        ]]
      }
    }
  }
}`

// buildReferenceFixture parses and builds, returning the first error from
// either layer. References are checked twice: the parser verifies the field a
// reference names is in scope, and the model verifies the member within it,
// because only the model knows a field's type.
func buildReferenceFixture(t *testing.T, raw string) (*Model, error) {
	t.Helper()

	schema, err := protodef.Parse([]byte(raw))
	if err != nil {
		return nil, err
	}

	return Build(schema, Options{PackageName: "fixture"})
}

func TestReferenceResolvesIntoBitFieldMembers(t *testing.T) {
	model, err := buildReferenceFixture(t, referenceFixture)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	packet := findModelPacket(t, model, "node")
	redirect := findSwitchDeclaration(t, packet, "Redirect")

	// The switch reads the bit, not the packed integer it came in.
	if redirect.Switch.CompareTo != "flags/has_redirect_node" {
		t.Errorf("compareTo = %q", redirect.Switch.CompareTo)
	}
	if redirect.Switch.CompareType != "uint8" {
		t.Errorf("compare type = %q, want the member's type rather than the whole bitfield", redirect.Switch.CompareType)
	}
}

func TestReferenceResolvesIntoBitFlagsMembers(t *testing.T) {
	model, err := buildReferenceFixture(t, referenceFixture)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	packet := findModelPacket(t, model, "actions")
	latency := findSwitchDeclaration(t, packet, "Latency")

	if latency.Switch.CompareType != "bool" {
		t.Errorf("compare type = %q, want bool for a bitflags member", latency.Switch.CompareType)
	}
}

// TestReferenceResolvesOutwardFromANestedScope covers what protocol 775's
// DebugSubscriptionUpdate needs: a bare name naming a field of an enclosing
// container, with nothing of that name in the inner scope.
func TestReferenceResolvesOutwardFromANestedScope(t *testing.T) {
	model, err := buildReferenceFixture(t, referenceFixture)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	packet := findModelPacket(t, model, "nested")
	if len(packet.Declarations) == 0 {
		t.Fatal("the nested container produced no declarations")
	}

	var found bool
	for _, declaration := range packet.Declarations {
		if declaration.Kind == DeclarationSwitch && declaration.Switch.CompareTo == "kind" {
			found = true
			if declaration.Switch.CompareType != "int32" {
				t.Errorf("compare type = %q, want the outer field's type", declaration.Switch.CompareType)
			}
		}
	}
	if !found {
		t.Error("the inner switch did not resolve against the outer field")
	}
}

func TestReferenceRejects(t *testing.T) {
	cases := []struct {
		name    string
		from    string
		to      string
		wantErr string
	}{
		{
			name:    "a member of a field that has none",
			from:    `"compareTo": "flags/has_redirect_node"`,
			to:      `"compareTo": "label/has_redirect_node"`,
			wantErr: "no members to address",
		},
		{
			name:    "a member the bitfield does not declare",
			from:    `"compareTo": "flags/has_redirect_node"`,
			to:      `"compareTo": "flags/no_such_bit"`,
			wantErr: `no member "no_such_bit"`,
		},
		{
			name:    "a three-segment path",
			from:    `"compareTo": "flags/has_redirect_node"`,
			to:      `"compareTo": "flags/has_redirect_node/deeper"`,
			wantErr: "only 2 are supported",
		},
		{
			name:    "an empty segment",
			from:    `"compareTo": "flags/has_redirect_node"`,
			to:      `"compareTo": "flags/"`,
			wantErr: "empty segment",
		},
		{
			name:    "a parent hop past the packet root",
			from:    `"compareTo": "kind"`,
			to:      `"compareTo": "../../../kind"`,
			wantErr: `"../../../kind"`,
		},
		{
			name:    "a name that is nowhere in the scope chain",
			from:    `"compareTo": "kind"`,
			to:      `"compareTo": "absent"`,
			wantErr: `"absent"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Replace(referenceFixture, tc.from, tc.to, 1)
			if raw == referenceFixture {
				t.Fatalf("fixture does not contain %s", tc.from)
			}

			_, err := buildReferenceFixture(t, raw)
			if err == nil {
				t.Fatal("Build accepted the reference")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to name %q", err, tc.wantErr)
			}
		})
	}
}

// TestExplicitParentHopDoesNotWalkFurther pins the difference between the two
// forms: "../name" names one exact level, so it fails rather than quietly
// finding a field further out that a bare name would have reached.
func TestExplicitParentHopDoesNotWalkFurther(t *testing.T) {
	raw := strings.Replace(referenceFixture, `"compareTo": "kind"`, `"compareTo": "../../kind"`, 1)

	_, err := buildReferenceFixture(t, raw)
	if err == nil {
		t.Fatal("an explicit hop past the binding scope was accepted")
	}
}

// findModelPacket searches every state and direction, because the fixture's
// packets all live in one direction but the search should not assume it.
func findModelPacket(t *testing.T, model *Model, sourceName string) Packet {
	t.Helper()

	for _, state := range model.States {
		for _, direction := range state.Directions {
			for _, packet := range direction.Packets {
				if packet.SourceName == sourceName {
					return packet
				}
			}
		}
	}
	t.Fatalf("model has no packet %q", sourceName)

	return Packet{}
}

func findSwitchDeclaration(t *testing.T, packet Packet, contains string) Declaration {
	t.Helper()

	for _, declaration := range packet.Declarations {
		if declaration.Kind == DeclarationSwitch && strings.Contains(declaration.Name, contains) {
			return declaration
		}
	}
	t.Fatalf("packet %s has no switch declaration naming %q", packet.SourceName, contains)

	return Declaration{}
}
