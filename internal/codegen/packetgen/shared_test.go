package packetgen

import (
	"strings"
	"testing"
)

// sharedFixture is a protocol with two packets, so a type can be used by one
// or by both and the reuse threshold is observable.
func sharedFixture(types, firstField, secondField string) string {
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
        "packet_first":["container",[
          ` + firstField + `
        ]],
        "packet_second":["container",[
          ` + secondField + `
        ]],
        "packet":["container",[
          {"name":"name","type":["mapper",{"type":"varint","mappings":{"0":"first","1":"second"}}]},
          {"name":"params","type":["switch",{"compareTo":"name","fields":{"first":"packet_first","second":"packet_second"}}]}
        ]]
      }
    }
  }
}`
}

func buildSharedFixture(t *testing.T, types, firstField, secondField string) *Model {
	t.Helper()

	model, err := Build(parseFixture(t, sharedFixture(types, firstField, secondField)), Options{})
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}

	return model
}

func sharedNames(model *Model) []string {
	names := make([]string, 0, len(model.SharedTypes))
	for _, shared := range model.SharedTypes {
		names = append(names, shared.SchemaName)
	}

	return names
}

func findShared(t *testing.T, model *Model, schemaName string) SharedType {
	t.Helper()

	for _, shared := range model.SharedTypes {
		if shared.SchemaName == schemaName {
			return shared
		}
	}
	t.Fatalf("shared type %q not found in %v", schemaName, sharedNames(model))

	return SharedType{}
}

const pointType = `, "point":["container",[
      {"name":"x","type":"i32"},
      {"name":"y","type":"i32"}
    ]]`

// TestTypeUsedByTwoPacketsIsShared is the reuse threshold.
func TestTypeUsedByTwoPacketsIsShared(t *testing.T) {
	t.Parallel()

	model := buildSharedFixture(
		t, pointType,
		`{"name":"where","type":"point"}`,
		`{"name":"also","type":"point"}`,
	)

	shared := findShared(t, model, "point")
	if shared.GoName != "Point" {
		t.Fatalf("shared Go name = %q, want Point", shared.GoName)
	}
	if shared.Recursive {
		t.Fatal("point is not recursive")
	}
}

// TestTypeUsedByOnePacketIsInlined is the other side of the threshold: naming
// a type nothing else can hold adds a Go type without adding meaning.
func TestTypeUsedByOnePacketIsInlined(t *testing.T) {
	t.Parallel()

	model := buildSharedFixture(
		t, pointType,
		`{"name":"where","type":"point"}`,
		`{"name":"value","type":"i32"}`,
	)

	if names := sharedNames(model); len(names) != 0 {
		t.Fatalf("shared types = %v, want none", names)
	}
}

// TestSelfRecursiveTypeIsSharedAndTerminates is the case inlining cannot
// express at all: a type that contains itself expands forever.
func TestSelfRecursiveTypeIsSharedAndTerminates(t *testing.T) {
	t.Parallel()

	const branch = `, "branch":["container",[
      {"name":"value","type":"i32"},
      {"name":"child","type":["option","branch"]}
    ]]`

	model := buildSharedFixture(
		t, branch,
		`{"name":"root","type":"branch"}`,
		`{"name":"value","type":"i32"}`,
	)

	shared := findShared(t, model, "branch")
	if !shared.Recursive {
		t.Fatal("a self-referential type must be marked recursive")
	}
	// Sharing is what makes it compile even though only one packet uses it.
	if len(model.SharedTypes) != 1 {
		t.Fatalf("shared types = %v, want only branch", sharedNames(model))
	}
}

// TestMutuallyRecursiveTypesAreAllSharedAndTerminate is protocol 775's slot,
// component, and predicate cycle in miniature.
func TestMutuallyRecursiveTypesAreAllSharedAndTerminate(t *testing.T) {
	t.Parallel()

	const cycle = `, "alpha":["container",[
      {"name":"value","type":"i32"},
      {"name":"next","type":["option","beta"]}
    ]],
    "beta":["container",[
      {"name":"value","type":"i32"},
      {"name":"next","type":["option","gamma"]}
    ]],
    "gamma":["container",[
      {"name":"value","type":"i32"},
      {"name":"next","type":["option","alpha"]}
    ]]`

	model := buildSharedFixture(
		t, cycle,
		`{"name":"root","type":"alpha"}`,
		`{"name":"value","type":"i32"}`,
	)

	for _, name := range []string{"alpha", "beta", "gamma"} {
		if shared := findShared(t, model, name); !shared.Recursive {
			t.Fatalf("%s participates in a cycle and must be recursive", name)
		}
	}
}

// TestSharedTypeOrderIsDeterministic states that the output does not depend on
// map iteration or on the order packets happen to appear in.
func TestSharedTypeOrderIsDeterministic(t *testing.T) {
	t.Parallel()

	const twoTypes = pointType + `, "label":["container",[
      {"name":"text","type":"string"}
    ]]`

	first := buildSharedFixture(
		t, twoTypes,
		`{"name":"where","type":"point"},{"name":"tag","type":"label"}`,
		`{"name":"also","type":"point"},{"name":"other","type":"label"}`,
	)

	for range 5 {
		again := buildSharedFixture(
			t, twoTypes,
			`{"name":"where","type":"point"},{"name":"tag","type":"label"}`,
			`{"name":"also","type":"point"},{"name":"other","type":"label"}`,
		)
		if got, want := sharedNames(again), sharedNames(first); strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("shared order = %v, want %v", got, want)
		}
	}

	// Sorted by schema name, not by first use.
	if got := sharedNames(first); got[0] != "label" || got[1] != "point" {
		t.Fatalf("shared order = %v, want label before point", got)
	}
}

// TestSharedDecoderCountsNestingDepth covers the guard that turns a hostile
// recursive input into a decode error rather than a stack overflow.
func TestSharedDecoderCountsNestingDepth(t *testing.T) {
	t.Parallel()

	const branch = `, "branch":["container",[
      {"name":"value","type":"i32"},
      {"name":"child","type":["option","branch"]}
    ]]`

	model := buildSharedFixture(
		t, branch,
		`{"name":"root","type":"branch"}`,
		`{"name":"value","type":"i32"}`,
	)

	files, err := Generate(model, Options{PackageName: "shared"})
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	codec := string(files["codec.go"])

	if !strings.Contains(codec, "func (shared *Branch) Decode(") {
		t.Fatal("the shared type has no Decode method")
	}
	if !strings.Contains(codec, "buffer.EnterNested(\"branch\")") {
		t.Fatal("a shared decoder must count nesting depth")
	}
	if !strings.Contains(codec, "defer buffer.LeaveNested()") {
		t.Fatal("a shared decoder must release its depth on every exit path")
	}
}

// TestRecursiveSharedTypeIsHeldThroughAPointer is the difference between a
// type that parses and a type that exists. A struct that contains itself by
// value has no size, so the Go compiler rejects it — which is what protocol
// 775's slot display does through a switch on its own kind.
func TestRecursiveSharedTypeIsHeldThroughAPointer(t *testing.T) {
	t.Parallel()

	const branch = `, "branch":["container",[
      {"name":"kind","type":"varint"},
      {"name":"data","type":["switch",{"compareTo":"kind","fields":{"1":"branch"},"default":"void"}]}
    ]]`

	model := buildSharedFixture(
		t, branch,
		`{"name":"root","type":"branch"}`,
		`{"name":"value","type":"i32"}`,
	)

	files, err := Generate(model, Options{PackageName: "fixture"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	packets := string(files["packets.go"])
	if !strings.Contains(packets, "*Branch") {
		t.Errorf("packets.go holds the recursive type by value:\n%s", packets)
	}

	codec := string(files["codec.go"])
	if !strings.Contains(codec, "= new(Branch)") {
		t.Errorf("codec.go never allocates the recursive type:\n%s", codec)
	}
}

// TestSharedMapperConvertsToItsOwnType covers the other half: a shared mapper
// compiles to a named string type, and the lookup tables hold plain strings.
// Without a conversion the generated code parses and does not type-check.
func TestSharedMapperConvertsToItsOwnType(t *testing.T) {
	t.Parallel()

	const kind = `, "kind":["mapper",{"type":"varint","mappings":{"0":"stone","1":"dirt"}}]`

	model := buildSharedFixture(
		t, kind,
		`{"name":"first","type":"kind"}`,
		`{"name":"second","type":"kind"}`,
	)

	shared := findShared(t, model, "kind")
	if shared.Mapper == "" {
		t.Fatal("the shared type lost its mapper")
	}

	files, err := Generate(model, Options{PackageName: "fixture"})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	codec := string(files["codec.go"])
	if !strings.Contains(codec, "= "+shared.GoName+"(mapped") {
		t.Errorf("codec.go assigns a plain string to %s:\n%s", shared.GoName, codec)
	}
	if !strings.Contains(codec, "[string((*shared))]") {
		t.Errorf("codec.go indexes the mapper table with a named type:\n%s", codec)
	}
}
